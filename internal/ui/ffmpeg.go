package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"pcloud-desktop/internal/hls"
)

// iso639 convertit un code de langue HLS (« fr », « en-US », « fra ») en code à
// trois lettres, seul accepté par le conteneur MP4. Retourne "" si inconnu.
func iso639(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if i := strings.IndexAny(lang, "-_"); i > 0 {
		lang = lang[:i]
	}
	if len(lang) == 3 {
		return lang
	}
	two := map[string]string{
		"fr": "fra", "en": "eng", "es": "spa", "de": "deu", "it": "ita", "pt": "por",
		"nl": "nld", "ar": "ara", "ru": "rus", "ja": "jpn", "zh": "zho", "ko": "kor",
		"tr": "tur", "pl": "pol", "sv": "swe", "hi": "hin", "he": "heb", "el": "ell",
		"da": "dan", "fi": "fin", "no": "nor", "cs": "ces", "hu": "hun", "ro": "ron",
		"uk": "ukr", "th": "tha", "vi": "vie", "id": "ind", "ca": "cat",
	}
	return two[lang]
}

// ffmpegArgs construit la commande de fusion. files[0] est la vidéo, suivie
// des pistes audio puis des sous-titres, dans le même ordre que audio et subs.
func ffmpegArgs(files []string, audio, subs []hls.Rendition, out string) []string {
	args := []string{"-y", "-hide_banner", "-loglevel", "error"}
	for _, f := range files {
		args = append(args, "-i", f)
	}
	if len(audio) > 0 {
		// L'audio vient des pistes choisies : on ne garde que la vidéo de l'entrée 0.
		args = append(args, "-map", "0:v?")
	} else {
		args = append(args, "-map", "0")
	}
	for i := range audio {
		args = append(args, "-map", fmt.Sprintf("%d:a", 1+i))
	}
	for i := range subs {
		args = append(args, "-map", fmt.Sprintf("%d:s", 1+len(audio)+i))
	}
	args = append(args, "-c", "copy", "-c:s", "mov_text")
	meta := func(kind string, i int, r hls.Rendition) {
		if l := iso639(r.Language); l != "" {
			args = append(args, fmt.Sprintf("-metadata:s:%s:%d", kind, i), "language="+l)
		}
		if r.Name != "" {
			args = append(args, fmt.Sprintf("-metadata:s:%s:%d", kind, i), "handler_name="+r.Name)
		}
	}
	for i, r := range audio {
		meta("a", i, r)
	}
	for i, r := range subs {
		meta("s", i, r)
	}
	return append(args, "-movflags", "+faststart", out)
}

// runHLSMerged télécharge vidéo, audio et sous-titres dans un dossier
// temporaire, puis les assemble en un seul MP4 avec ffmpeg (sans ré-encodage).
// En cas d'échec de ffmpeg, les fichiers téléchargés sont conservés.
func runHLSMerged(ctx context.Context, dl *hls.Downloader, ffmpeg, outPath string,
	video *hls.Media, audio, subs []hls.Rendition, report func(string, float64), done func([]string)) error {

	tmp, err := os.MkdirTemp("", "pcloud-hls-")
	if err != nil {
		return err
	}
	vpath := filepath.Join(tmp, "video"+hlsExt(video, false))
	vf, err := os.Create(vpath)
	if err != nil {
		return err
	}
	defer vf.Close()

	var files []string
	err = runHLS(ctx, dl, vf, vpath, video, audio, subs,
		func(text string, frac float64) { report(text, frac*0.95) },
		func(f []string) { files = f })
	if cerr := vf.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}

	report("Assemblage en MP4 (ffmpeg)…", 0.96)
	cmd := exec.CommandContext(ctx, ffmpeg, ffmpegArgs(files, audio, subs, outPath)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			_ = os.RemoveAll(tmp)
			return ctx.Err()
		}
		msg := strings.TrimSpace(string(out))
		if len(msg) > 600 {
			msg = msg[len(msg)-600:]
		}
		return fmt.Errorf("ffmpeg a échoué : %v\n%s\n\nFichiers conservés dans %s", err, msg, tmp)
	}
	_ = os.RemoveAll(tmp)
	done([]string{outPath})
	return nil
}
