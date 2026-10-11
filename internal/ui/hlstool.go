package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"pcloud-desktop/internal/hls"
)

// prefHLSParallel est la clé de préférence du nombre de connexions parallèles.
const prefHLSParallel = "hls.parallel"

// ---------------------------------------------------------------- logique pure

// hlsVariantLabel décrit une variante : « 1280×720 · 2.5 Mb/s · 30 i/s · avc1…,mp4a… ».
func hlsVariantLabel(v hls.Variant) string {
	var parts []string
	if v.Width > 0 && v.Height > 0 {
		parts = append(parts, fmt.Sprintf("%d×%d", v.Width, v.Height))
	}
	if v.Bandwidth > 0 {
		parts = append(parts, fmt.Sprintf("%.1f Mb/s", float64(v.Bandwidth)/1e6))
	}
	if v.FrameRate > 0 {
		parts = append(parts, fmt.Sprintf("%.0f i/s", v.FrameRate))
	}
	if v.Codecs != "" {
		parts = append(parts, v.Codecs)
	}
	if len(parts) == 0 {
		return "Flux"
	}
	return strings.Join(parts, " · ")
}

// hlsRenditionLabel décrit une piste : « Français (fr) · par défaut ».
func hlsRenditionLabel(r hls.Rendition) string {
	name := r.Name
	if name == "" {
		name = r.Language
	}
	if name == "" {
		name = "Piste"
	}
	if r.Language != "" && !strings.EqualFold(r.Language, name) {
		name += " (" + r.Language + ")"
	}
	if r.Default {
		name += " · par défaut"
	}
	return name
}

// hlsAudioLabel ajoute le groupe au libellé, pour distinguer les versions
// d'une même langue : « French, high (fr) · groupe v3a · par défaut ».
func hlsAudioLabel(r hls.Rendition) string {
	label := hlsRenditionLabel(r)
	if r.GroupID == "" {
		return label
	}
	// Le marqueur « par défaut » reste en fin de libellé.
	label = strings.TrimSuffix(label, " · par défaut")
	label += " · groupe " + r.GroupID
	if r.Default {
		label += " · par défaut"
	}
	return label
}

// sortRenditions trie par langue, puis nom, puis groupe (stable).
func sortRenditions(rs []hls.Rendition) []hls.Rendition {
	out := append([]hls.Rendition(nil), rs...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Language != b.Language {
			return a.Language < b.Language
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.GroupID < b.GroupID
	})
	return out
}

// defaultRenditions retourne les pistes à présélectionner : celles par défaut
// du groupe de la variante, sinon la première de ce groupe, sinon la première.
func defaultRenditions(all []hls.Rendition, group string) []hls.Rendition {
	var inGroup, defaults []hls.Rendition
	for _, r := range all {
		if group != "" && r.GroupID == group {
			inGroup = append(inGroup, r)
			if r.Default {
				defaults = append(defaults, r)
			}
		}
	}
	switch {
	case len(defaults) > 0:
		return defaults
	case len(inGroup) > 0:
		return inGroup[:1]
	case len(all) > 0:
		return all[:1]
	}
	return nil
}

// keepExisting garde, parmi sel, les libellés toujours présents dans options.
func keepExisting(sel, options []string) []string {
	var out []string
	for _, s := range sel {
		for _, o := range options {
			if s == o {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// sortVariants trie par débit décroissant (meilleure qualité d'abord).
func sortVariants(vs []hls.Variant) []hls.Variant {
	out := append([]hls.Variant(nil), vs...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Bandwidth != out[j].Bandwidth {
			return out[i].Bandwidth > out[j].Bandwidth
		}
		return out[i].Height > out[j].Height
	})
	return out
}

// hlsExt choisit l'extension du fichier d'après le premier segment.
func hlsExt(m *hls.Media, audio bool) string {
	ext := ""
	if len(m.Segments) > 0 {
		if u, err := url.Parse(m.Segments[0].URI); err == nil {
			ext = strings.ToLower(path.Ext(u.Path))
		}
	}
	switch {
	case ext == ".vtt" || ext == ".webvtt":
		return ".vtt"
	case ext == ".aac":
		return ".aac"
	case m.Init != nil || ext == ".mp4" || ext == ".m4s" || ext == ".m4a" || ext == ".m4v" || ext == ".cmfv" || ext == ".cmfa":
		if audio {
			return ".m4a"
		}
		return ".mp4"
	}
	return ".ts"
}

// hlsBaseName propose un nom de fichier à partir de l'URL ou du chemin saisi.
func hlsBaseName(src string) string {
	p := src
	if u, err := url.Parse(src); err == nil && u.Path != "" {
		p = u.Path
	}
	name := strings.TrimSuffix(path.Base(p), path.Ext(p))
	switch strings.ToLower(name) {
	case "", ".", "/", "master", "index", "playlist", "main":
		// nom peu parlant : on prend le dossier parent s'il en a un
		if dir := path.Base(path.Dir(p)); dir != "." && dir != "/" && dir != "" {
			return dir
		}
		return "video"
	}
	return name
}

// videoExts : extensions retirées d'un nom de fichier pCloud pour en faire la
// base du nom de sortie (« Film.mkv » → « Film »).
var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".avi": true, ".mov": true, ".m4v": true, ".ts": true,
	".webm": true, ".wmv": true, ".flv": true, ".mpg": true, ".mpeg": true, ".m2ts": true,
}

// outputBaseName choisit la base du nom de sortie : le nom du fichier pCloud
// s'il est connu (hint), sinon un nom déduit de l'URL ou du chemin saisi.
func outputBaseName(source, hint string) string {
	if hint = strings.TrimSpace(hint); hint != "" {
		if ext := filepath.Ext(hint); videoExts[strings.ToLower(ext)] {
			hint = strings.TrimSuffix(hint, ext)
		}
		if validName(hint) {
			return hint
		}
	}
	return hlsBaseName(source)
}

// safeSuffix rend une langue ou un nom utilisable dans un nom de fichier.
func safeSuffix(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, s)
	return strings.Trim(s, "_")
}

// ---------------------------------------------------------------- interface

// openHLSTool ouvre l'outil « Flux HLS » : lit un master.m3u8 (URL ou fichier),
// propose qualité, pistes audio et sous-titres, puis télécharge.
func (b *browser) openHLSTool() { b.openHLSToolWith("", "") }

// openHLSToolWith ouvre l'outil ; si src n'est pas vide, il est analysé aussitôt.
// name (facultatif) est le nom du fichier pCloud d'origine, utilisé pour nommer
// le résultat.
func (b *browser) openHLSToolWith(src, name string) {
	dl := hls.NewDownloader()

	var (
		master   *hls.Master
		single   *hls.Media // playlist média donnée directement
		source   string
		variants []hls.Variant
		labels   []string // libellés des variantes, dans l'ordre de variants
		audioOps []hls.Rendition
		subOps   []hls.Rendition
	)

	entry := widget.NewEntry()
	entry.SetPlaceHolder("https://…/master.m3u8  ou  /chemin/vers/master.m3u8")
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord

	variantRadio := widget.NewRadioGroup(nil, nil)
	audioChecks := widget.NewCheckGroup(nil, nil)
	subChecks := widget.NewCheckGroup(nil, nil)
	audioNote := widget.NewLabel("")
	audioNote.Wrapping = fyne.TextWrapWord
	subNote := widget.NewLabel("")
	subNote.Wrapping = fyne.TextWrapWord

	choices := container.NewVBox(
		widget.NewLabelWithStyle("Qualité vidéo", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), variantRadio,
		widget.NewLabelWithStyle("Pistes audio (langues)", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), audioChecks, audioNote,
		widget.NewLabelWithStyle("Sous-titres", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), subChecks, subNote,
	)
	choices.Hide()

	selectedVariant := func() (hls.Variant, bool) {
		for i, l := range labels {
			if l == variantRadio.Selected {
				return variants[i], true
			}
		}
		return hls.Variant{}, false
	}

	refreshGroups := func() {
		audioOps, subOps = nil, nil
		var preselect []string
		if v, ok := selectedVariant(); ok && master != nil {
			// Toutes les versions d'audio, tous groupes confondus : l'audio se choisit
			// indépendamment de la qualité vidéo. Le groupe associé à la variante
			// fournit la présélection.
			audioOps = sortRenditions(master.AllRenditions("AUDIO"))
			subOps = sortRenditions(master.AllRenditions("SUBTITLES"))
			for _, r := range defaultRenditions(audioOps, v.Audio) {
				preselect = append(preselect, hlsAudioLabel(r))
			}
		}
		alabels := make([]string, len(audioOps))
		for i, r := range audioOps {
			alabels[i] = hlsAudioLabel(r)
		}
		// Un changement de qualité vidéo conserve les cases que l'utilisateur a cochées.
		keep := audioChecks.Selected
		audioChecks.Options = alabels
		if len(keep) > 0 {
			audioChecks.SetSelected(keepExisting(keep, alabels))
		} else {
			audioChecks.SetSelected(preselect)
		}
		audioChecks.Refresh()
		if len(alabels) == 0 {
			audioNote.SetText("Aucune piste audio séparée : l'audio est inclus dans la vidéo.")
			audioChecks.Hide()
		} else {
			audioNote.SetText("Chaque piste cochée est enregistrée dans son propre fichier (ou fusionnée dans le MP4). " +
				"Les groupes (v1a, v2a…) sont les versions d'audio déclarées par le flux, par exemple low / normal / high.")
			audioChecks.Show()
		}

		slabels := make([]string, len(subOps))
		for i, r := range subOps {
			slabels[i] = hlsAudioLabel(r)
		}
		subChecks.Options = slabels
		subChecks.SetSelected(keepExisting(subChecks.Selected, slabels))
		subChecks.Refresh()
		if len(slabels) == 0 {
			subNote.SetText("Aucun sous-titre séparé dans ce flux.")
			subChecks.Hide()
		} else {
			subNote.SetText("")
			subChecks.Show()
		}
	}
	variantRadio.OnChanged = func(string) { refreshGroups() }

	analyse := func() {
		src := strings.TrimSpace(entry.Text)
		if src == "" {
			status.SetText("Saisissez l'URL ou le chemin d'un master.m3u8.")
			return
		}
		status.SetText("Analyse de la playlist…")
		choices.Hide()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			m, md, err := dl.FetchPlaylist(ctx, src)
			fyne.Do(func() {
				if err != nil {
					master, single, variants, labels = nil, nil, nil, nil
					status.SetText("Échec : " + err.Error())
					return
				}
				source, master, single = src, m, md
				if m == nil {
					variants, labels = nil, []string{fmt.Sprintf("Playlist média unique · %d segments · %s", len(md.Segments), formatDuration(md.Duration))}
					variantRadio.Options = labels
					variantRadio.Refresh()
					variantRadio.SetSelected(labels[0])
					status.SetText("Playlist média (pas de master) : un seul flux.")
				} else {
					variants = sortVariants(m.Variants)
					labels = make([]string, len(variants))
					for i, v := range variants {
						labels[i] = fmt.Sprintf("%d. %s", i+1, hlsVariantLabel(v))
					}
					variantRadio.Options = labels
					variantRadio.Refresh()
					variantRadio.SetSelected(labels[0])
					status.SetText(fmt.Sprintf("%d variante(s) · %d piste(s) alternative(s).", len(m.Variants), len(m.Renditions)))
				}
				audioChecks.SetSelected(nil)
				subChecks.SetSelected(nil)
				refreshGroups()
				choices.Show()
			})
		}()
	}
	entry.OnSubmitted = func(string) { analyse() }
	analyseBtn := widget.NewButton("Analyser", analyse)
	pickFile := widget.NewButton("Fichier…", func() {
		fd := dialog.NewFileOpen(func(rc fyne.URIReadCloser, err error) {
			if err != nil || rc == nil {
				return
			}
			entry.SetText(rc.URI().Path())
			_ = rc.Close()
			analyse()
		}, b.w)
		fd.Show()
	})

	parallelOptions := []string{"1", "2", "4", "6", "8", "12", "16"}
	parallel := widget.NewSelect(parallelOptions, func(v string) {
		b.a.Preferences().SetInt(prefHLSParallel, atoiOr(v, hls.DefaultParallel))
	})
	parallel.SetSelected(fmt.Sprint(b.a.Preferences().IntWithFallback(prefHLSParallel, hls.DefaultParallel)))
	if parallel.Selected == "" {
		parallel.SetSelected(fmt.Sprint(hls.DefaultParallel))
	}
	parallelRow := container.NewBorder(nil, nil, widget.NewLabel("Connexions parallèles (comme yt-dlp -N)"), nil, parallel)

	ffmpeg, _ := exec.LookPath("ffmpeg")
	convert := widget.NewCheck("Convertir en MP4 et fusionner audio / sous-titres (ffmpeg)", nil)
	note := widget.NewLabel("")
	note.Wrapping = fyne.TextWrapWord
	if ffmpeg != "" {
		convert.SetChecked(true)
		note.SetText("Sans ré-encodage : quelques secondes de plus. Décoché, vidéo, audio et sous-titres sont " +
			"enregistrés en fichiers séparés (.ts / .vtt), le conteneur d'origine du flux.")
	} else {
		convert.Disable()
		note.SetText("ffmpeg est introuvable : vidéo, audio et sous-titres sont enregistrés en fichiers séparés " +
			"(.ts / .vtt). Installez ffmpeg pour obtenir un MP4 unique.")
	}

	content := container.NewGridWrap(fyne.NewSize(600, 480), container.NewVScroll(container.NewVBox(
		container.NewBorder(nil, nil, nil, container.NewHBox(pickFile, analyseBtn), entry),
		status, choices, widget.NewSeparator(), parallelRow, convert, note,
	)))

	d := dialog.NewCustomConfirm("Lire un flux HLS (master.m3u8)", "Télécharger", "Fermer", content, func(ok bool) {
		if !ok {
			return
		}
		if choices.Hidden || (master == nil && single == nil) {
			dialog.ShowInformation("Flux HLS", "Analysez d'abord une playlist.", b.w)
			return
		}
		var chosenAudio, chosenSubs []hls.Rendition
		for i, r := range audioOps {
			if i < len(audioChecks.Options) && contains(audioChecks.Selected, audioChecks.Options[i]) {
				chosenAudio = append(chosenAudio, r)
			}
		}
		for i, r := range subOps {
			if i < len(subChecks.Options) && contains(subChecks.Selected, subChecks.Options[i]) {
				chosenSubs = append(chosenSubs, r)
			}
		}
		var videoURL string
		if single == nil {
			v, ok := selectedVariant()
			if !ok {
				return
			}
			videoURL = v.URI
		}
		dl.Parallel = atoiOr(parallel.Selected, hls.DefaultParallel)
		merge := ""
		if convert.Checked {
			merge = ffmpeg
		}
		b.startHLS(dl, outputBaseName(source, name), videoURL, single, chosenAudio, chosenSubs, merge)
	}, b.w)
	d.Show()
	if src != "" {
		entry.SetText(src)
		analyse()
	} else {
		b.w.Canvas().Focus(entry)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// startHLS prépare le téléchargement : récupère la playlist vidéo (pour
// connaître l'extension), demande l'emplacement, puis télécharge vidéo, audio
// et sous-titres sélectionnés.
// merge non vide : chemin de ffmpeg, pour assembler le tout en un MP4.
func (b *browser) startHLS(dl *hls.Downloader, baseName, videoURL string, single *hls.Media, audio, subs []hls.Rendition, merge string) {
	b.status.SetText("Lecture de la playlist vidéo…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		video := single
		var err error
		if video == nil {
			var nested *hls.Master
			nested, video, err = dl.FetchPlaylist(ctx, videoURL)
			if err == nil && nested != nil {
				err = errors.New("playlist inattendue : un master a été renvoyé à la place d'une playlist média")
			}
		}
		fyne.Do(func() {
			b.status.SetText(summary(b.view))
			if err != nil {
				dialog.ShowError(err, b.w)
				return
			}
			b.saveHLS(dl, baseName, video, audio, subs, merge)
		})
	}()
}

func (b *browser) saveHLS(dl *hls.Downloader, baseName string, video *hls.Media, audio, subs []hls.Rendition, merge string) {
	ext := hlsExt(video, false)
	if merge != "" {
		ext = ".mp4"
	}
	d := dialog.NewFileSave(func(wc fyne.URIWriteCloser, err error) {
		if err != nil {
			dialog.ShowError(err, b.w)
			return
		}
		if wc == nil {
			return
		}
		b.transfer("Téléchargement HLS", func(ctx context.Context, report func(string, float64)) error {
			finished := func(files []string) {
				fyne.Do(func() {
					dialog.ShowInformation("Téléchargement terminé", "Fichiers enregistrés :\n"+strings.Join(files, "\n"), b.w)
				})
			}
			if merge != "" {
				// ffmpeg écrit lui-même le fichier choisi (déjà confirmé dans la boîte de dialogue).
				path := wc.URI().Path()
				_ = wc.Close()
				return runHLSMerged(ctx, dl, merge, path, video, audio, subs, report, finished)
			}
			defer wc.Close()
			return runHLS(ctx, dl, wc, wc.URI().Path(), video, audio, subs, report, finished)
		})
	}, b.w)
	d.SetFileName(baseName + ext)
	d.Show()
}

// runHLS télécharge la vidéo dans videoW, puis chaque piste audio et chaque
// sous-titre dans un fichier voisin (<base>.<langue>.<ext>) qui n'écrase jamais
// un fichier existant.
func runHLS(ctx context.Context, dl *hls.Downloader, videoW io.Writer, videoPath string,
	video *hls.Media, audio, subs []hls.Rendition, report func(string, float64), done func([]string)) error {

	type job struct {
		label string
		media *hls.Media // nil : à récupérer via uri
		uri   string
		stem  string // vide : vidéo principale
		audio bool
		opt   hls.Options
	}
	base := strings.TrimSuffix(videoPath, filepath.Ext(videoPath))
	jobs := []job{{label: "Vidéo", media: video}}
	for _, r := range audio {
		jobs = append(jobs, job{label: "Audio · " + hlsAudioLabel(r), uri: r.URI, audio: true,
			stem: base + "." + suffixFor(r, audio)})
	}
	for _, r := range subs {
		jobs = append(jobs, job{label: "Sous-titres · " + hlsAudioLabel(r), uri: r.URI,
			stem: base + "." + suffixFor(r, subs), opt: hls.Options{WebVTT: true}})
	}

	meter := newSpeedMeter()
	var files []string
	for i, j := range jobs {
		m := j.media
		if m == nil {
			nested, md, err := dl.FetchPlaylist(ctx, j.uri)
			if err != nil {
				return fmt.Errorf("%s : %w", j.label, err)
			}
			if nested != nil || md == nil {
				return fmt.Errorf("%s : playlist média attendue", j.label)
			}
			m = md
		}

		w, outPath := videoW, videoPath
		var f *os.File
		if j.stem != "" {
			var err error
			outPath = uniquePath(j.stem + hlsExt(m, j.audio))
			if f, err = os.Create(outPath); err != nil {
				return err
			}
			w = f
		}
		opt := j.opt
		opt.OnBytes = func(n int) { meter.Add(int64(n)) }
		startBytes := meter.Total()
		err := dl.Download(ctx, m, w, opt, func(n, total int) {
			frac := (float64(i) + float64(n)/float64(total)) / float64(len(jobs))
			report(hlsProgressText(j.label, n, total, meter.Total(), meter.Total()-startBytes, meter.Rate()), frac)
		})
		if f != nil {
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}
		if err != nil {
			if f != nil {
				_ = os.Remove(outPath) // pas de fichier partiel pour les pistes annexes
			}
			return fmt.Errorf("%s : %w", j.label, err)
		}
		files = append(files, outPath)
	}
	done(files)
	return nil
}

// hlsProgressText : « Vidéo · segment 12/240 · 120 Mo · 5.1 Mo/s · reste ~3 min ».
// jobBytes : octets reçus pour cette piste ; sert à estimer le temps restant à
// partir de la taille moyenne des segments déjà reçus.
func hlsProgressText(label string, seg, total int, allBytes, jobBytes int64, rate float64) string {
	s := fmt.Sprintf("%s · segment %d/%d · %s", label, seg, total, humanSize(allBytes))
	if r := formatRate(rate); r != "" {
		s += " · " + r
		if seg > 0 && total > seg {
			remaining := float64(jobBytes) / float64(seg) * float64(total-seg)
			if eta := formatETA(remaining / rate); eta != "" {
				s += " · reste " + eta
			}
		}
	}
	return s
}

// uniquePath retourne p, ou « nom_2.ext », « nom_3.ext »… si p existe déjà.
func uniquePath(p string) string {
	if _, err := os.Stat(p); err != nil {
		return p
	}
	ext := filepath.Ext(p)
	stem := strings.TrimSuffix(p, ext)
	for n := 2; ; n++ {
		c := fmt.Sprintf("%s_%d%s", stem, n, ext)
		if _, err := os.Stat(c); err != nil {
			return c
		}
	}
}

// suffixFor construit le « .<langue> » d'un fichier de piste (sans extension).
// Quand plusieurs pistes de de même type partagent la langue (versions low / high…),
// le groupe est ajouté pour les distinguer : « fr-v3a ».
func suffixFor(r hls.Rendition, same []hls.Rendition) string {
	s := safeSuffix(r.Language)
	if s == "" {
		s = safeSuffix(r.Name)
	}
	if s == "" {
		s = "piste"
	}
	n := 0
	for _, o := range same {
		if o.Language == r.Language {
			n++
		}
	}
	if g := safeSuffix(r.GroupID); n > 1 && g != "" {
		s += "-" + g
	}
	return s
}
