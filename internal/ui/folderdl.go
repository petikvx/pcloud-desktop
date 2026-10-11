package ui

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"pcloud-desktop/internal/hls"
	"pcloud-desktop/internal/pcloud"
	"pcloud-desktop/internal/queue"
)

// Clés de préférences du téléchargement de dossier.
const (
	prefFolderHeight  = "folder.height"
	prefFolderLangs   = "folder.langs"
	prefFolderQuality = "folder.audioquality"
	prefFolderSubs    = "folder.subs"
)

var audioQualityLabels = []string{"Meilleure version", "Version standard", "Version légère"}

const bestHeightLabel = "Meilleure qualité"

// downloadFolder explore un dossier pCloud, demande qualité vidéo et langues
// une seule fois, puis télécharge tout son contenu dans un dossier local.
func (b *browser) downloadFolder(folder pcloud.Item) {
	b.status.SetText("Analyse du dossier « " + folder.Name + " »…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		entries, err := scanFolder(ctx, b.c, folder.FolderID)

		// Master de référence : celui de la première vidéo, pour proposer les
		// qualités et les langues réellement disponibles.
		var ref *hls.Master
		var refNote string
		if err == nil {
			for _, e := range entries {
				if !isVideo(e.Item) {
					continue
				}
				link, isHLS, lerr := b.c.MediaTranscodeLink(ctx, e.Item.FileID)
				switch {
				case lerr != nil:
					refNote = "Flux de « " + e.Item.Name + " » indisponible : " + lerr.Error()
				case !isHLS:
					refNote = "« " + e.Item.Name + " » n'a pas de flux HLS : téléchargement du fichier tel quel."
				default:
					m, _, ferr := hls.NewDownloader().FetchPlaylist(ctx, link)
					if ferr != nil {
						refNote = "Playlist de « " + e.Item.Name + " » illisible : " + ferr.Error()
					} else {
						ref = m
					}
				}
				break
			}
		}
		fyne.Do(func() {
			b.status.SetText(summary(b.view))
			if err != nil {
				b.fail(err)
				return
			}
			if len(entries) == 0 {
				dialog.ShowInformation("Dossier vide", "« "+folder.Name+" » ne contient aucun fichier.", b.w)
				return
			}
			b.showFolderConfig(folder, entries, ref, refNote)
		})
	}()
}

func (b *browser) showFolderConfig(folder pcloud.Item, entries []entry, ref *hls.Master, refNote string) {
	prefs := b.a.Preferences()

	var nVideos, nOthers int
	var sizeOthers, sizeVideos int64
	for _, e := range entries {
		if isVideo(e.Item) {
			nVideos++
			sizeVideos += e.Item.Size
		} else {
			nOthers++
			sizeOthers += e.Item.Size
		}
	}

	intro := widget.NewLabel(fmt.Sprintf("« %s » : %d vidéo(s) (%s d'origine)", folder.Name, nVideos, humanSize(sizeVideos)))
	intro.Wrapping = fyne.TextWrapWord

	// --- Qualité vidéo
	heightOpts := []string{bestHeightLabel}
	if ref != nil {
		seen := map[int]bool{}
		for _, v := range sortVariants(ref.Variants) {
			if v.Height > 0 && !seen[v.Height] {
				seen[v.Height] = true
				heightOpts = append(heightOpts, fmt.Sprintf("%dp", v.Height))
			}
		}
	}
	heightSel := widget.NewSelect(heightOpts, nil)
	heightSel.SetSelected(bestHeightLabel)
	if saved := prefs.String(prefFolderHeight); saved != "" {
		for _, o := range heightOpts {
			if o == saved {
				heightSel.SetSelected(o)
			}
		}
	}

	// --- Langues audio et sous-titres
	var audioLangs, subLangs []hls.LangInfo
	if ref != nil {
		audioLangs = hls.Languages(ref.AllRenditions("AUDIO"))
		subLangs = hls.Languages(ref.AllRenditions("SUBTITLES"))
	}
	labelsOf := func(ls []hls.LangInfo) ([]string, map[string]string) {
		labels := make([]string, len(ls))
		keys := map[string]string{}
		for i, l := range ls {
			labels[i] = l.Label
			keys[l.Label] = l.Key
		}
		return labels, keys
	}
	audioLabels, audioKeys := labelsOf(audioLangs)
	subLabels, subKeys := labelsOf(subLangs)

	pick := func(ls []hls.LangInfo, saved string, fallbackDefault bool) []string {
		want := map[string]bool{}
		for _, k := range strings.Split(saved, ",") {
			if k != "" {
				want[k] = true
			}
		}
		var out []string
		for _, l := range ls {
			if want[l.Key] || (len(want) == 0 && fallbackDefault && l.Default) {
				out = append(out, l.Label)
			}
		}
		if len(out) == 0 && fallbackDefault && len(ls) > 0 && len(want) == 0 {
			out = []string{ls[0].Label}
		}
		return out
	}
	audioChecks := widget.NewCheckGroup(audioLabels, nil)
	audioChecks.SetSelected(pick(audioLangs, prefs.String(prefFolderLangs), true))
	subChecks := widget.NewCheckGroup(subLabels, nil)
	subChecks.SetSelected(pick(subLangs, prefs.String(prefFolderSubs), false))

	multiVersion := false
	for _, l := range audioLangs {
		if l.Versions > 1 {
			multiVersion = true
		}
	}
	qualitySel := widget.NewSelect(audioQualityLabels, nil)
	qualitySel.SetSelected(audioQualityLabels[int(prefs.IntWithFallback(prefFolderQuality, 0))%len(audioQualityLabels)])

	// --- Autres options
	othersCheck := widget.NewCheck(fmt.Sprintf("Inclure aussi les %d autre(s) fichier(s) non vidéo (%s)", nOthers, humanSize(sizeOthers)), nil)
	if nOthers == 0 {
		othersCheck.Disable()
	}
	ffmpeg, _ := exec.LookPath("ffmpeg")
	mergeCheck := widget.NewCheck("Un seul MP4 par vidéo, audio et sous-titres fusionnés (ffmpeg)", nil)
	if ffmpeg != "" {
		mergeCheck.SetChecked(true)
	} else {
		mergeCheck.Disable()
		mergeCheck.SetText(mergeCheck.Text + " — ffmpeg introuvable")
	}
	parallelSel := widget.NewSelect([]string{"1", "2", "4", "6", "8", "12", "16"}, nil)
	parallelSel.SetSelected(fmt.Sprint(prefs.IntWithFallback(prefHLSParallel, hls.DefaultParallel)))

	section := func(title string) fyne.CanvasObject {
		return widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	}
	items := []fyne.CanvasObject{intro}
	if refNote != "" {
		n := widget.NewLabel(refNote)
		n.Wrapping = fyne.TextWrapWord
		items = append(items, n)
	}
	if ref != nil {
		items = append(items, section("Qualité vidéo"), heightSel)
		if len(audioLabels) > 0 {
			items = append(items, section("Langues audio"), audioChecks)
			if multiVersion {
				items = append(items, section("Version de l'audio"), qualitySel)
			}
		}
		if len(subLabels) > 0 {
			items = append(items, section("Sous-titres"), subChecks)
		}
	}
	items = append(items, widget.NewSeparator(), othersCheck, mergeCheck,
		container.NewBorder(nil, nil, widget.NewLabel("Connexions parallèles"), nil, parallelSel))

	content := container.NewGridWrap(fyne.NewSize(560, 520), container.NewVScroll(container.NewVBox(items...)))
	d := dialog.NewCustomConfirm("Télécharger le dossier", "Choisir la destination…", "Annuler", content, func(ok bool) {
		if !ok {
			return
		}
		var p hls.Preferences
		if h := strings.TrimSuffix(heightSel.Selected, "p"); heightSel.Selected != bestHeightLabel {
			p.Height = atoiOr(h, 0)
		}
		for _, l := range audioChecks.Selected {
			p.AudioLangs = append(p.AudioLangs, audioKeys[l])
		}
		for _, l := range subChecks.Selected {
			p.SubLangs = append(p.SubLangs, subKeys[l])
		}
		qi := 0
		for i, l := range audioQualityLabels {
			if l == qualitySel.Selected {
				qi = i
			}
		}
		p.AudioQuality = hls.Quality(qi)

		prefs.SetString(prefFolderHeight, heightSel.Selected)
		prefs.SetString(prefFolderLangs, strings.Join(p.AudioLangs, ","))
		prefs.SetString(prefFolderSubs, strings.Join(p.SubLangs, ","))
		prefs.SetInt(prefFolderQuality, qi)
		parallel := atoiOr(parallelSel.Selected, hls.DefaultParallel)
		prefs.SetInt(prefHLSParallel, parallel)

		merge := ""
		if mergeCheck.Checked {
			merge = ffmpeg
		}
		b.chooseDestination(folder, entries, p, othersCheck.Checked, merge, parallel)
	}, b.w)
	d.Show()
}

// chooseDestination demande le dossier local parent, y crée le dossier du lot
// puis lance la file d'attente.
func (b *browser) chooseDestination(folder pcloud.Item, entries []entry, p hls.Preferences, others bool, merge string, parallel int) {
	d := dialog.NewFolderOpen(func(lu fyne.ListableURI, err error) {
		if err != nil {
			dialog.ShowError(err, b.w)
			return
		}
		if lu == nil {
			return
		}
		root := filepath.Join(lu.Path(), localName(folder.Name))
		dl := hls.NewDownloader()
		dl.Parallel = parallel
		job := &folderJob{c: b.c, dl: dl, ffmpeg: merge, prefs: p, root: root}
		b.runFolderQueue(folder.Name, root, entries, others, job)
	}, b.w)
	d.Show()
}

// runFolderQueue ouvre la fenêtre de file d'attente et exécute les téléchargements.
func (b *browser) runFolderQueue(name, root string, entries []entry, others bool, job *folderJob) {
	tasks, kept := buildTasks(entries, others)
	if len(tasks) == 0 {
		dialog.ShowInformation("Rien à télécharger", "Aucune vidéo dans ce dossier. Cochez « Inclure aussi les autres fichiers ».", b.w)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	win := b.a.NewWindow("Téléchargement : " + name)
	win.Resize(fyne.NewSize(720, 520))

	snapshot := append([]queue.Task(nil), tasks...)
	header := widget.NewLabel("")
	header.Wrapping = fyne.TextWrapWord
	overall := widget.NewProgressBar()

	list := widget.NewList(
		func() int { return len(snapshot) },
		func() fyne.CanvasObject {
			n := widget.NewLabel("")
			n.Truncation = fyne.TextTruncateEllipsis
			d := widget.NewLabel("")
			d.Truncation = fyne.TextTruncateEllipsis
			return container.NewVBox(n, d, widget.NewProgressBar())
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i >= len(snapshot) {
				return
			}
			t := snapshot[i]
			row := o.(*fyne.Container)
			row.Objects[0].(*widget.Label).SetText(t.Name)
			detail := t.State.String()
			switch {
			case t.State == queue.Failed && t.Err != nil:
				detail += " : " + t.Err.Error()
			case t.Text != "" && t.State == queue.Running:
				detail += " · " + t.Text
			}
			row.Objects[1].(*widget.Label).SetText(detail)
			row.Objects[2].(*widget.ProgressBar).SetValue(t.Frac)
		},
	)

	update := func(ts []queue.Task) {
		snapshot = ts
		done := 0
		for _, t := range ts {
			if t.State != queue.Pending && t.State != queue.Running {
				done++
			}
		}
		header.SetText(fmt.Sprintf("%d / %d fichier(s) traité(s) → %s", done, len(ts), root))
		overall.SetValue(float64(done) / float64(len(ts)))
		list.Refresh()
	}
	update(snapshot)

	cancelBtn := widget.NewButton("Annuler", func() { cancel() })
	openBtn := widget.NewButton("Ouvrir le dossier", func() {
		_ = b.a.OpenURL(&url.URL{Scheme: "file", Path: root})
	})
	openBtn.Disable()
	closeBtn := widget.NewButton("Fermer", func() { win.Close() })
	win.SetOnClosed(cancel)
	win.SetContent(container.NewBorder(
		container.NewVBox(header, overall),
		container.NewHBox(cancelBtn, openBtn, closeBtn),
		nil, nil, list))
	win.Show()

	go func() {
		sum := queue.Run(ctx, tasks, func(ctx context.Context, i int, report func(string, float64)) error {
			return job.Run(ctx, kept[i], report)
		}, func(ts []queue.Task) {
			fyne.Do(func() { update(ts) })
		})
		fyne.Do(func() {
			cancelBtn.Disable()
			openBtn.Enable()
			header.SetText(fmt.Sprintf("Terminé : %d téléchargé(s), %d ignoré(s) (déjà présents), %d échec(s), %d annulé(s) → %s",
				sum.Done, sum.Skipped, sum.Failed, sum.Canceled, root))
			if sum.Failed == 0 && sum.Canceled == 0 {
				overall.SetValue(1)
			}
		})
	}()
}
