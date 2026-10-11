package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"pcloud-desktop/internal/hls"
	"pcloud-desktop/internal/pcloud"
	"pcloud-desktop/internal/queue"
)

type fakeLister map[uint64][]pcloud.Item

func (f fakeLister) ListFolder(_ context.Context, id uint64) ([]pcloud.Item, error) {
	items, ok := f[id]
	if !ok {
		return nil, errors.New("dossier inconnu")
	}
	return append([]pcloud.Item(nil), items...), nil
}

func TestScanFolderRecursiveStableOrder(t *testing.T) {
	l := fakeLister{
		1: {
			{Name: "b.mkv", FileID: 11},
			{Name: "Saison 2", IsFolder: true, FolderID: 3},
			{Name: "a.mkv", FileID: 10},
			{Name: "Saison 1", IsFolder: true, FolderID: 2},
		},
		2: {{Name: "e01.mkv", FileID: 20}},
		3: {{Name: "e01.mkv", FileID: 30}, {Name: "x/y", IsFolder: true, FolderID: 4}},
		4: {{Name: "deep.mkv", FileID: 40}},
	}
	got, err := scanFolder(context.Background(), l, 1)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range got {
		names = append(names, e.RelDir+"|"+e.Item.Name)
	}
	want := []string{
		"Saison 1|e01.mkv",
		"Saison 2/x_y|deep.mkv", // « / » neutralisé ; les sous-dossiers passent avant les fichiers
		"Saison 2|e01.mkv",
		"|a.mkv",
		"|b.mkv",
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("got  %v\nwant %v", names, want)
	}
	if _, err := scanFolder(context.Background(), fakeLister{}, 9); err == nil {
		t.Fatal("erreur de listing attendue")
	}
}

func TestLocalNameAndBuildTasks(t *testing.T) {
	for in, want := range map[string]string{"a/b": "a_b", `a\b`: "a_b", "..": "_", " ": "_", "ok.mkv": "ok.mkv"} {
		if got := localName(in); got != want {
			t.Errorf("localName(%q) = %q, want %q", in, got, want)
		}
	}
	entries := []entry{
		{Item: pcloud.Item{Name: "v.mkv", Category: pcloud.CategoryVideo}, RelDir: "S1"},
		{Item: pcloud.Item{Name: "cover.jpg", Category: pcloud.CategoryImage}, RelDir: "S1"},
	}
	tasks, kept := buildTasks(entries, false)
	if len(tasks) != 1 || len(kept) != 1 || tasks[0].Name != "S1/v.mkv" {
		t.Fatalf("sans les autres : %+v", tasks)
	}
	tasks, kept = buildTasks(entries, true)
	if len(tasks) != 2 || len(kept) != 2 {
		t.Fatalf("avec les autres : %+v", tasks)
	}
}

// fakePCloud simule l'API pCloud et un CDN HLS : un film en HLS (2 qualités,
// audio français low/high et anglais), une vidéo sans HLS, un fichier simple.
func fakePCloud(t *testing.T) *pcloud.Client {
	t.Helper()
	var host string
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }

	mux.HandleFunc("/getmediatranscodelink", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("fileid") == "1" {
			reply(w, map[string]any{"result": 0, "variants": []map[string]any{
				{"transcodetype": "hls", "path": "/hls/master.m3u8", "hosts": []string{host}},
				{"transcodetype": "original", "isoriginal": true, "path": "/dl/orig", "hosts": []string{host}},
			}})
			return
		}
		reply(w, map[string]any{"result": 0, "variants": []map[string]any{
			{"transcodetype": "original", "isoriginal": true, "path": "/dl/orig", "hosts": []string{host}},
		}})
	})
	mux.HandleFunc("/getfilelink", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"result": 0, "hosts": []string{host}, "path": "/dl/plain"})
	})
	mux.HandleFunc("/dl/plain", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "13")
		_, _ = w.Write([]byte("PLAIN-CONTENT"))
	})
	mux.HandleFunc("/hls/master.m3u8", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="a1",NAME="French, low",LANGUAGE="fr",URI="a/frl.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="a3",NAME="French, high",LANGUAGE="fr",DEFAULT=YES,URI="a/frh.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="a3",NAME="English",LANGUAGE="en",URI="a/en.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=1280x720,AUDIO="a3"
720/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1920x1080,AUDIO="a3"
1080/index.m3u8
`))
	})
	media := func(prefix string, n int) {
		mux.HandleFunc("/hls/"+prefix+".m3u8", func(w http.ResponseWriter, r *http.Request) {
			var sb strings.Builder
			sb.WriteString("#EXTM3U\n")
			for i := 0; i < n; i++ {
				fmt.Fprintf(&sb, "#EXTINF:2,\n%s_%d.ts\n", strings.ReplaceAll(prefix, "/", "_"), i)
			}
			sb.WriteString("#EXT-X-ENDLIST\n")
			_, _ = w.Write([]byte(sb.String()))
		})
		for i := 0; i < n; i++ {
			name := fmt.Sprintf("%s_%d.ts", strings.ReplaceAll(prefix, "/", "_"), i)
			content := fmt.Sprintf("[%s]", name)
			mux.HandleFunc("/hls/"+path0(prefix)+name, func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(content))
			})
		}
	}
	media("720/index", 2)
	media("1080/index", 2)
	media("a/frl", 1)
	media("a/frh", 1)
	media("a/en", 1)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	host = strings.TrimPrefix(srv.URL, "http://")
	c := pcloud.New()
	c.SetBaseURL(srv.URL)
	return c
}

// path0 retourne le dossier d'une playlist (« 720/ », « a/ »).
func path0(prefix string) string {
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		return prefix[:i+1]
	}
	return ""
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func noReport(string, float64) {}

func TestFolderJobAppliesPreferencesAndSkipsExisting(t *testing.T) {
	c := fakePCloud(t)
	root := t.TempDir()
	job := &folderJob{
		c: c, dl: hls.NewDownloader(), root: root,
		prefs: hls.Preferences{Height: 720, AudioLangs: []string{"fr", "en"}, AudioQuality: hls.QualityLowest},
	}
	film := entry{RelDir: "Saison 1", Item: pcloud.Item{Name: "Film.2024.MULTi.mkv", FileID: 1, Category: pcloud.CategoryVideo}}

	if err := job.Run(context.Background(), film, noReport); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "Saison 1")
	// Vidéo 720p : segments de la bonne qualité, dans l'ordre.
	if got := read(t, filepath.Join(dir, "Film.2024.MULTi.ts")); got != "[720_index_0.ts][720_index_1.ts]" {
		t.Fatalf("vidéo = %q", got)
	}
	// Audio : français version légère, et anglais.
	if got := read(t, filepath.Join(dir, "Film.2024.MULTi.fr.ts")); got != "[a_frl_0.ts]" {
		t.Fatalf("audio fr = %q", got)
	}
	if got := read(t, filepath.Join(dir, "Film.2024.MULTi.en.ts")); got != "[a_en_0.ts]" {
		t.Fatalf("audio en = %q", got)
	}
	// Pas de fichier partiel laissé.
	matches, _ := filepath.Glob(filepath.Join(dir, "*.part"))
	if len(matches) != 0 {
		t.Fatalf("fichiers partiels : %v", matches)
	}
	// Relance : ignoré.
	if err := job.Run(context.Background(), film, noReport); !errors.Is(err, queue.ErrSkipped) {
		t.Fatalf("relance : err = %v, attendu ErrSkipped", err)
	}
}

func TestFolderJobBestQualityAndPlainFiles(t *testing.T) {
	c := fakePCloud(t)
	root := t.TempDir()
	job := &folderJob{
		c: c, dl: hls.NewDownloader(), root: root,
		prefs: hls.Preferences{AudioLangs: []string{"fr"}, AudioQuality: hls.QualityBest},
	}
	film := entry{Item: pcloud.Item{Name: "Film.mkv", FileID: 1, Category: pcloud.CategoryVideo}}
	if err := job.Run(context.Background(), film, noReport); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(root, "Film.ts")); got != "[1080_index_0.ts][1080_index_1.ts]" {
		t.Fatalf("meilleure qualité attendue : %q", got)
	}
	if got := read(t, filepath.Join(root, "Film.fr.ts")); got != "[a_frh_0.ts]" {
		t.Fatalf("meilleure version audio attendue : %q", got)
	}

	// Fichier non vidéo : téléchargement simple, puis ignoré à la relance.
	plain := entry{RelDir: "docs", Item: pcloud.Item{Name: "notes.txt", FileID: 2, Size: 13}}
	if err := job.Run(context.Background(), plain, noReport); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(root, "docs", "notes.txt")); got != "PLAIN-CONTENT" {
		t.Fatalf("fichier simple = %q", got)
	}
	if err := job.Run(context.Background(), plain, noReport); !errors.Is(err, queue.ErrSkipped) {
		t.Fatalf("relance : %v", err)
	}

	// Vidéo sans HLS (original seul) : repli sur le fichier tel quel.
	raw := entry{Item: pcloud.Item{Name: "Clip.mp4", FileID: 3, Size: 13, Category: pcloud.CategoryVideo}}
	if err := job.Run(context.Background(), raw, noReport); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(root, "Clip.mp4")); got != "PLAIN-CONTENT" {
		t.Fatalf("repli = %q", got)
	}
}

func TestFolderJobThroughQueue(t *testing.T) {
	c := fakePCloud(t)
	root := t.TempDir()
	job := &folderJob{c: c, dl: hls.NewDownloader(), root: root, prefs: hls.Preferences{Height: 720, AudioLangs: []string{"fr"}}}
	entries := []entry{
		{Item: pcloud.Item{Name: "Film.mkv", FileID: 1, Category: pcloud.CategoryVideo}},
		{Item: pcloud.Item{Name: "notes.txt", FileID: 2, Size: 13}},
		{Item: pcloud.Item{Name: "cassé.mkv", FileID: 99, Category: pcloud.CategoryVideo, Size: 5}}, // lien sans HLS puis getfilelink OK → ok
	}
	tasks, kept := buildTasks(entries, true)
	sum := queue.Run(context.Background(), tasks, func(ctx context.Context, i int, report func(string, float64)) error {
		return job.Run(ctx, kept[i], report)
	}, nil)
	if sum.Done+sum.Failed != 3 || sum.Done < 2 {
		t.Fatalf("résumé = %+v", sum)
	}
}
