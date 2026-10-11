package ui

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"pcloud-desktop/internal/hls"
)

func TestHLSVariantLabelAndSort(t *testing.T) {
	v := hls.Variant{Width: 1280, Height: 720, Bandwidth: 2500000, FrameRate: 29.97, Codecs: "avc1,mp4a"}
	if got, want := hlsVariantLabel(v), "1280×720 · 2.5 Mb/s · 30 i/s · avc1,mp4a"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if hlsVariantLabel(hls.Variant{}) != "Flux" {
		t.Fatal("variante vide : « Flux » attendu")
	}
	in := []hls.Variant{{Bandwidth: 800000}, {Bandwidth: 5000000}, {Bandwidth: 2500000}}
	out := sortVariants(in)
	if out[0].Bandwidth != 5000000 || out[2].Bandwidth != 800000 {
		t.Fatalf("tri = %+v", out)
	}
	if in[0].Bandwidth != 800000 {
		t.Fatal("l'entrée ne doit pas être modifiée")
	}
}

func TestHLSRenditionLabel(t *testing.T) {
	cases := []struct {
		r    hls.Rendition
		want string
	}{
		{hls.Rendition{Name: "Français", Language: "fr", Default: true}, "Français (fr) · par défaut"},
		{hls.Rendition{Name: "en", Language: "en"}, "en"},
		{hls.Rendition{Language: "de"}, "de"},
		{hls.Rendition{}, "Piste"},
	}
	for _, c := range cases {
		if got := hlsRenditionLabel(c.r); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

func TestHLSExt(t *testing.T) {
	mk := func(uri string, init bool) *hls.Media {
		m := &hls.Media{Segments: []hls.Segment{{URI: uri}}}
		if init {
			m.Init = &hls.Segment{URI: "init.mp4"}
		}
		return m
	}
	cases := []struct {
		m     *hls.Media
		audio bool
		want  string
	}{
		{mk("https://h/a/seg0.ts?token=1", false), false, ".ts"},
		{mk("https://h/a/seg0.m4s", true), false, ".mp4"},
		{mk("https://h/a/seg0.m4s", true), true, ".m4a"},
		{mk("https://h/a/seg0.aac", false), true, ".aac"},
		{mk("https://h/a/seg0.vtt", false), false, ".vtt"},
		{mk("https://h/a/seg0", false), false, ".ts"},
	}
	for _, c := range cases {
		if got := hlsExt(c.m, c.audio); got != c.want {
			t.Errorf("%s : got %q, want %q", c.m.Segments[0].URI, got, c.want)
		}
	}
}

func TestHLSBaseName(t *testing.T) {
	cases := map[string]string{
		"https://h.example/vacances/plage/master.m3u8": "plage",
		"https://h.example/Plage-2024.m3u8?x=1":        "Plage-2024",
		"/home/me/videos/noel.m3u8":                    "noel",
		"https://h.example/master.m3u8":                "video",
	}
	for in, want := range cases {
		if got := hlsBaseName(in); got != want {
			t.Errorf("hlsBaseName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSafeSuffixAndUniquePath(t *testing.T) {
	if got := safeSuffix("fr-CA / Québec"); got != "fr-CA___Qu_bec" {
		t.Fatalf("got %q", got)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "v.fr.ts")
	if uniquePath(p) != p {
		t.Fatal("fichier absent : chemin inchangé")
	}
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := uniquePath(p), filepath.Join(dir, "v.fr_2.ts"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRunHLSWritesVideoAudioAndSubtitles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("v0.ts", "V0")
	write("v1.ts", "V1")
	write("a0.ts", "A0")
	write("s0.vtt", "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHello\n\n")
	write("s1.vtt", "WEBVTT\n\n00:00:03.000 --> 00:00:04.000\nWorld\n\n")
	pl := func(name string, segs ...string) string {
		text := "#EXTM3U\n"
		for _, s := range segs {
			text += "#EXTINF:1,\n" + s + "\n"
		}
		return write(name, text+"#EXT-X-ENDLIST\n")
	}
	vp := pl("video.m3u8", "v0.ts", "v1.ts")
	ap := pl("audio_fr.m3u8", "a0.ts")
	sp := pl("sub_fr.m3u8", "s0.vtt", "s1.vtt")

	dl := hls.NewDownloader()
	_, video, err := dl.FetchPlaylist(context.Background(), vp)
	if err != nil {
		t.Fatal(err)
	}
	// Un fichier du même nom existe déjà : il ne doit pas être écrasé.
	write("Plage.fr.ts", "EXISTANT")

	var out bytes.Buffer
	var files []string
	var lastFrac float64
	err = runHLS(context.Background(), dl, &out, filepath.Join(dir, "Plage.ts"), video,
		[]hls.Rendition{{Type: "AUDIO", Language: "fr", Name: "Français", URI: ap}},
		[]hls.Rendition{{Type: "SUBTITLES", Language: "fr", URI: sp}},
		func(_ string, f float64) { lastFrac = f },
		func(f []string) { files = f })
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != "V0V1" {
		t.Fatalf("vidéo = %q", out.String())
	}
	if len(files) != 3 {
		t.Fatalf("fichiers = %v", files)
	}
	audio, _ := os.ReadFile(files[1])
	if string(audio) != "A0" || filepath.Base(files[1]) != "Plage.fr_2.ts" {
		t.Fatalf("audio = %q dans %s", audio, files[1])
	}
	existing, _ := os.ReadFile(filepath.Join(dir, "Plage.fr.ts"))
	if string(existing) != "EXISTANT" {
		t.Fatal("un fichier existant a été écrasé")
	}
	sub, _ := os.ReadFile(files[2])
	if filepath.Ext(files[2]) != ".vtt" || bytes.Count(sub, []byte("WEBVTT")) != 1 ||
		!bytes.Contains(sub, []byte("Hello")) || !bytes.Contains(sub, []byte("World")) {
		t.Fatalf("sous-titres = %q", sub)
	}
	if lastFrac < 0.99 {
		t.Fatalf("progression finale = %v", lastFrac)
	}
}
