package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"pcloud-desktop/internal/pcloud"
)

const customLabel = "Personnalisé (transcodage par pCloud)"

var (
	customResolutions = []string{"Résolution d'origine", "1280x720", "854x480", "640x360", "426x240"}
	customVideoRates  = []string{"500", "1000", "2000", "4000"}
	customAudioRates  = []string{"64", "128", "192", "320"}
)

// formatDuration affiche des secondes en h:mm:ss ou m:ss.
func formatDuration(sec float64) string {
	if sec <= 0 {
		return ""
	}
	s := int(sec + 0.5)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s%3600/60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// variantLabel décrit une variante : « 1280×720 · h264/aac · 1500+128 kb/s · 2:05 (original) ».
func variantLabel(v pcloud.VideoVariant) string {
	parts := []string{fmt.Sprintf("%d×%d", v.Width, v.Height)}
	if v.VideoCodec != "" || v.AudioCodec != "" {
		parts = append(parts, strings.Trim(v.VideoCodec+"/"+v.AudioCodec, "/"))
	}
	if v.VideoBitrate > 0 || v.AudioBitrate > 0 {
		parts = append(parts, fmt.Sprintf("%d+%d kb/s", v.VideoBitrate, v.AudioBitrate))
	}
	if d := formatDuration(v.DurationSeconds()); d != "" {
		parts = append(parts, d)
	}
	label := strings.Join(parts, " · ")
	if v.IsOriginal {
		label += " (original)"
	}
	return label
}

// streamFileName propose un nom de fichier : « Plage - 720p.mp4 ».
func streamFileName(name string, height int) string {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if height > 0 {
		return fmt.Sprintf("%s - %dp.mp4", base, height)
	}
	return base + " - streaming.mp4"
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

// downloadStream récupère le lien de streaming d'une vidéo. Il essaie d'abord
// la méthode utilisée par le site web de pCloud (souvent un master.m3u8, ouvert
// dans l'outil HLS), puis se rabat sur la liste des variantes de l'API publique.
func (b *browser) downloadStream(it pcloud.Item) {
	b.status.SetText("Recherche du flux de streaming…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()

		link, isHLS, webErr := b.c.MediaTranscodeLink(ctx, it.FileID)
		var variants []pcloud.VideoVariant
		var apiErr error
		if webErr != nil && !pcloud.IsAuthError(webErr) {
			variants, apiErr = b.c.VideoVariants(ctx, it.FileID)
		}
		fyne.Do(func() {
			b.status.SetText(summary(b.view))
			switch {
			case webErr == nil && (isHLS || looksLikeHLS(link)):
				b.openHLSToolWith(link, it.Name)
			case webErr == nil:
				b.saveStream(it, streamFileName(it.Name, 0), func(context.Context) (string, error) { return link, nil })
			case pcloud.IsAuthError(webErr):
				b.fail(webErr)
			case apiErr == nil:
				b.showStreamChoice(it, variants)
			default:
				dialog.ShowError(fmt.Errorf("aucune méthode de streaming n'a fonctionné pour « %s ».\n\n"+
					"getmediatranscodelink : %v\n\ngetvideolinks : %v", it.Name, webErr, apiErr), b.w)
			}
		})
	}()
}

// looksLikeHLS devine qu'une URL de streaming est une playlist HLS.
func looksLikeHLS(u string) bool {
	return strings.Contains(strings.ToLower(u), ".m3u8")
}

func (b *browser) showStreamChoice(it pcloud.Item, variants []pcloud.VideoVariant) {
	options := make([]string, 0, len(variants)+1)
	for _, v := range variants {
		options = append(options, variantLabel(v))
	}
	options = append(options, customLabel)

	resolution := widget.NewSelect(customResolutions, nil)
	resolution.SetSelected(customResolutions[1])
	vrate := widget.NewSelect(customVideoRates, nil)
	vrate.SetSelected("2000")
	arate := widget.NewSelect(customAudioRates, nil)
	arate.SetSelected("128")
	customForm := widget.NewForm(
		widget.NewFormItem("Résolution", resolution),
		widget.NewFormItem("Débit vidéo (kb/s)", vrate),
		widget.NewFormItem("Débit audio (kb/s)", arate),
	)
	customForm.Hide()

	radio := widget.NewRadioGroup(options, func(s string) {
		if s == customLabel {
			customForm.Show()
		} else {
			customForm.Hide()
		}
	})
	// Par défaut : l'original s'il existe, sinon la première variante.
	pick := 0
	for i, v := range variants {
		if v.IsOriginal {
			pick = i
			break
		}
	}
	radio.SetSelected(options[pick])

	intro := "Versions disponibles en streaming pour « " + it.Name + " » :"
	if len(variants) == 0 {
		intro = "Aucune version streaming prête pour « " + it.Name + " » : transcodage personnalisé uniquement."
	}
	note := widget.NewLabel("La langue (piste audio) n'est pas exposée par l'API pCloud : seule la piste par défaut est fournie.\nLe fichier original conserve toutes ses pistes.")
	note.Wrapping = fyne.TextWrapWord
	label := widget.NewLabel(intro)
	label.Wrapping = fyne.TextWrapWord

	content := container.NewGridWrap(fyne.NewSize(520, 360),
		container.NewVScroll(container.NewVBox(label, radio, customForm, widget.NewSeparator(), note)))

	d := dialog.NewCustomConfirm("Version streaming", "Télécharger", "Annuler", content, func(ok bool) {
		if !ok {
			return
		}
		sel := radio.Selected
		for i, v := range variants {
			if options[i] == sel {
				b.saveStream(it, streamFileName(it.Name, v.Height), func(ctx context.Context) (string, error) {
					return b.c.VariantURL(v)
				})
				return
			}
		}
		if sel != customLabel {
			return
		}
		opts := pcloud.VideoOptions{
			VideoBitrate: atoiOr(vrate.Selected, 0),
			AudioBitrate: atoiOr(arate.Selected, 0),
		}
		height := 0
		if resolution.Selected != customResolutions[0] {
			opts.Resolution = resolution.Selected
			if _, h, ok := strings.Cut(opts.Resolution, "x"); ok {
				height = atoiOr(h, 0)
			}
		}
		b.saveStream(it, streamFileName(it.Name, height), func(ctx context.Context) (string, error) {
			return b.c.VideoLink(ctx, it.FileID, opts)
		})
	}, b.w)
	d.Show()
}

// saveStream demande où enregistrer, puis télécharge l'URL fournie par link.
// L'URL est demandée au dernier moment : les liens pCloud expirent.
func (b *browser) saveStream(it pcloud.Item, fileName string, link func(ctx context.Context) (string, error)) {
	d := dialog.NewFileSave(func(wc fyne.URIWriteCloser, err error) {
		if err != nil {
			dialog.ShowError(err, b.w)
			return
		}
		if wc == nil {
			return
		}
		b.transfer("Téléchargement du streaming", func(ctx context.Context, report func(string, float64)) error {
			defer wc.Close()
			report(fileName+" · préparation…", 0)
			u, err := link(ctx)
			if err != nil {
				return err
			}
			meter := newSpeedMeter()
			return b.c.DownloadURL(ctx, u, wc, func(done, total int64) {
				meter.Set(done)
				report(progressText(fileName, done, total, meter.Rate()), fraction(done, total))
			})
		})
	}, b.w)
	d.SetFileName(fileName)
	d.Show()
}
