package ui

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestISO639(t *testing.T) {
	for in, want := range map[string]string{"fr": "fra", "en-US": "eng", "FRA": "fra", "pt_BR": "por", "xx": "", "": ""} {
		if got := iso639(in); got != want {
			t.Errorf("iso639(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFFmpegArgs(t *testing.T) {
	audio := []hls.Rendition{{Language: "fr", Name: "Français"}, {Language: "en", Name: "English"}}
	subs := []hls.Rendition{{Language: "fr"}}
	got := ffmpegArgs([]string{"v.ts", "a1.ts", "a2.ts", "s.vtt"}, audio, subs, "out.mp4")
	joined := strings.Join(got, " ")
	for _, want := range []string{
		"-i v.ts -i a1.ts -i a2.ts -i s.vtt",
		"-map 0:v? -map 1:a -map 2:a -map 3:s",
		"-c copy -c:s mov_text",
		"-metadata:s:a:0 language=fra", "-metadata:s:a:1 language=eng", "-metadata:s:s:0 language=fra",
		"-metadata:s:a:0 handler_name=Français",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("manque %q dans : %s", want, joined)
		}
	}
	if got[len(got)-1] != "out.mp4" {
		t.Errorf("la sortie doit être en dernier : %v", got)
	}
	// Sans piste séparée : toutes les pistes de la vidéo sont conservées.
	if j := strings.Join(ffmpegArgs([]string{"v.ts"}, nil, nil, "o.mp4"), " "); !strings.Contains(j, "-map 0 ") {
		t.Errorf("sans audio séparé : -map 0 attendu : %s", j)
	}
}

// Intégration réelle : nécessite ffmpeg et ffprobe, sinon le test est sauté.
func TestRunHLSMergedWithRealFFmpeg(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg absent")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe absent")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(ffmpeg, append([]string{"-y", "-loglevel", "error"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg %v : %v\n%s", args, err, out)
		}
	}
	run("-f", "lavfi", "-i", "testsrc=duration=2:size=160x120:rate=10", "-c:v", "libx264", "-f", "mpegts", filepath.Join(dir, "v0.ts"))
	run("-f", "lavfi", "-i", "sine=frequency=440:duration=2", "-c:a", "aac", "-f", "mpegts", filepath.Join(dir, "a0.ts"))
	run("-f", "lavfi", "-i", "sine=frequency=880:duration=2", "-c:a", "aac", "-f", "mpegts", filepath.Join(dir, "a1.ts"))
	if err := os.WriteFile(filepath.Join(dir, "s0.vtt"), []byte("WEBVTT\n\n00:00:00.000 --> 00:00:01.500\nBonjour\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pl := func(name, seg string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#EXTM3U\n#EXTINF:2,\n"+seg+"\n#EXT-X-ENDLIST\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	dl := hls.NewDownloader()
	_, video, err := dl.FetchPlaylist(context.Background(), pl("v.m3u8", "v0.ts"))
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "final.mp4")
	var files []string
	err = runHLSMerged(context.Background(), dl, ffmpeg, out, video,
		[]hls.Rendition{
			{Type: "AUDIO", Language: "fr", Name: "Français", URI: pl("a0.m3u8", "a0.ts")},
			{Type: "AUDIO", Language: "en", Name: "English", URI: pl("a1.m3u8", "a1.ts")},
		},
		[]hls.Rendition{{Type: "SUBTITLES", Language: "fr", URI: pl("s.m3u8", "s0.vtt")}},
		func(string, float64) {}, func(f []string) { files = f })
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != out {
		t.Fatalf("fichiers = %v", files)
	}
	probe, err := exec.Command(ffprobe, "-v", "error", "-show_entries", "stream=codec_type:stream_tags=language",
		"-of", "csv=p=0", out).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe : %v\n%s", err, probe)
	}
	got := strings.Fields(strings.ReplaceAll(string(probe), "\r", ""))
	want := []string{"video,und", "audio,fra", "audio,eng", "subtitle,fra"}
	// ffprobe peut omettre la balise si absente : on compare sur le type + langue.
	if len(got) != len(want) {
		t.Fatalf("flux = %v, attendus %v", got, want)
	}
	for i := range want {
		if !strings.HasPrefix(got[i], strings.Split(want[i], ",")[0]) {
			t.Errorf("flux %d = %q, attendu %q", i, got[i], want[i])
		}
	}
	if !strings.Contains(string(probe), "audio,fra") || !strings.Contains(string(probe), "audio,eng") ||
		!strings.Contains(string(probe), "subtitle,fra") {
		t.Errorf("langues manquantes : %s", probe)
	}
	if entries, _ := filepath.Glob(filepath.Join(os.TempDir(), "pcloud-hls-*")); len(entries) > 0 {
		t.Logf("dossiers temporaires restants (peut venir d'autres exécutions) : %v", entries)
	}
}

func TestOutputBaseName(t *testing.T) {
	cases := []struct{ source, hint, want string }{
		{"https://et3-c.pcloud.com/VcWZwjNxzy7ZXvls2G7Zilu0ZYZ/master.m3u8", "Scary.Movie.2.2001.MULTi.1080p.WEB.H265-TyHD.mkv", "Scary.Movie.2.2001.MULTi.1080p.WEB.H265-TyHD"},
		{"https://h/x/master.m3u8", "Vacances été 2024.MP4", "Vacances été 2024"},
		{"https://h/x/master.m3u8", "Film.sans.extension.video", "Film.sans.extension.video"}, // « .video » n'est pas une extension connue
		{"https://h/vacances/plage/master.m3u8", "", "plage"},
		{"https://h/x/master.m3u8", "a/b", "x"}, // nom invalide : repli sur l'URL
	}
	for _, c := range cases {
		if got := outputBaseName(c.source, c.hint); got != c.want {
			t.Errorf("outputBaseName(%q, %q) = %q, want %q", c.source, c.hint, got, c.want)
		}
	}
}

func TestAudioVersionsAreAllChoosable(t *testing.T) {
	all := []hls.Rendition{
		{GroupID: "v3a", Name: "French, high", Language: "fr", Default: true},
		{GroupID: "v1a", Name: "English", Language: "en"},
		{GroupID: "v1a", Name: "French, low", Language: "fr", Default: true},
		{GroupID: "v3a", Name: "English", Language: "en"},
	}
	sorted := sortRenditions(all)
	if sorted[0].Language != "en" || sorted[0].GroupID != "v1a" || sorted[1].GroupID != "v3a" {
		t.Fatalf("tri = %+v", sorted)
	}
	if all[0].GroupID != "v3a" {
		t.Fatal("l'entrée ne doit pas être modifiée")
	}
	if got := hlsAudioLabel(all[0]); got != "French, high (fr) · groupe v3a · par défaut" {
		t.Fatalf("libellé = %q", got)
	}
	// La variante liée au groupe v1a présélectionne le français « low », pas le « high ».
	def := defaultRenditions(sorted, "v1a")
	if len(def) != 1 || def[0].Name != "French, low" {
		t.Fatalf("présélection v1a = %+v", def)
	}
	if def := defaultRenditions(sorted, "inconnu"); len(def) != 1 {
		t.Fatalf("repli sur la première piste attendu : %+v", def)
	}
	if defaultRenditions(nil, "v1a") != nil {
		t.Fatal("aucune piste : nil attendu")
	}
}

func TestSuffixForDisambiguatesSameLanguage(t *testing.T) {
	fr1 := hls.Rendition{Language: "fr", GroupID: "v1a"}
	fr3 := hls.Rendition{Language: "fr", GroupID: "v3a"}
	en := hls.Rendition{Language: "en", GroupID: "v3a"}
	both := []hls.Rendition{fr1, fr3, en}
	if got := suffixFor(fr1, both); got != "fr-v1a" {
		t.Errorf("fr1 = %q", got)
	}
	if got := suffixFor(fr3, both); got != "fr-v3a" {
		t.Errorf("fr3 = %q", got)
	}
	if got := suffixFor(en, both); got != "en" {
		t.Errorf("une seule piste anglaise : %q", got)
	}
	if got := keepExisting([]string{"a", "x", "b"}, []string{"b", "a"}); len(got) != 2 {
		t.Errorf("keepExisting = %v", got)
	}
}
