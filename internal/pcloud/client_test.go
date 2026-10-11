package pcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeAPI démarre un serveur qui répond selon le chemin et mémorise la
// dernière requête.
type fakeAPI struct {
	srv  *httptest.Server
	last *http.Request
	form map[string][]string
	body []byte
}

func newFake(t *testing.T, routes map[string]any) (*fakeAPI, *Client) {
	t.Helper()
	f := &fakeAPI{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.last = r
		f.body, _ = io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(f.body))
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			_ = r.ParseForm()
			f.form = r.PostForm
		}
		resp, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		switch v := resp.(type) {
		case []byte:
			_, _ = w.Write(v)
		default:
			_ = json.NewEncoder(w).Encode(v)
		}
	}))
	t.Cleanup(f.srv.Close)
	c := New()
	c.base = f.srv.URL
	return f, c
}

func TestLoginStoresToken(t *testing.T) {
	f, c := newFake(t, map[string]any{
		"/userinfo": map[string]any{"result": 0, "auth": "tok123"},
	})
	if err := c.Login(context.Background(), "a@b.c", "pw"); err != nil {
		t.Fatal(err)
	}
	if c.Auth() != "tok123" {
		t.Fatalf("auth = %q", c.Auth())
	}
	if f.form["username"][0] != "a@b.c" || f.form["getauth"][0] != "1" {
		t.Fatalf("form = %v", f.form)
	}
	if strings.Contains(f.last.URL.RawQuery, "pw") {
		t.Fatal("le mot de passe ne doit pas être dans l'URL")
	}
}

func TestAPIErrorAndAuthDetection(t *testing.T) {
	_, c := newFake(t, map[string]any{
		"/listfolder": map[string]any{"result": 2094, "error": "Invalid access token."},
	})
	_, err := c.ListFolder(context.Background(), 0)
	if err == nil || !IsAuthError(err) {
		t.Fatalf("err = %v, attendu une erreur d'authentification", err)
	}
	if IsAuthError(&APIError{Code: 2005}) {
		t.Fatal("2005 n'est pas une erreur d'authentification")
	}
}

func TestListFolderParsesItems(t *testing.T) {
	_, c := newFake(t, map[string]any{
		"/listfolder": map[string]any{"result": 0, "metadata": map[string]any{"contents": []map[string]any{
			{"name": "Photos", "isfolder": true, "folderid": 7},
			{"name": "a.jpg", "isfolder": false, "fileid": 9, "size": 1234, "category": 1, "thumb": true,
				"modified": "Sun, 10 Oct 2021 12:30:00 +0000"},
		}}},
	})
	items, err := c.ListFolder(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID() != 7 || items[1].ID() != 9 {
		t.Fatalf("items = %+v", items)
	}
	if !items[1].Thumb || items[1].Category != CategoryImage {
		t.Fatalf("catégorie/miniature mal lues : %+v", items[1])
	}
	want := time.Date(2021, 10, 10, 12, 30, 0, 0, time.UTC)
	if !items[1].ModTime().Equal(want) {
		t.Fatalf("ModTime = %v", items[1].ModTime())
	}
	if !items[0].ModTime().IsZero() {
		t.Fatal("date absente : attendu le temps zéro")
	}
}

func TestOAuthTokenUsesAccessTokenParam(t *testing.T) {
	f, c := newFake(t, map[string]any{
		"/userinfo": map[string]any{"result": 0},
	})
	if err := c.RestoreSession(context.Background(), "oauth:abc"); err != nil {
		t.Fatal(err)
	}
	if f.form["access_token"][0] != "abc" || len(f.form["auth"]) != 0 {
		t.Fatalf("form = %v", f.form)
	}
	if c.SessionValue() != "oauth:abc" {
		t.Fatalf("SessionValue = %q", c.SessionValue())
	}
}

func TestRestoreSessionFailureLogsOut(t *testing.T) {
	_, c := newFake(t, map[string]any{
		"/userinfo": map[string]any{"result": 1000, "error": "Log in required."},
	})
	if err := c.RestoreSession(context.Background(), "old"); err == nil {
		t.Fatal("erreur attendue")
	}
	if c.Auth() != "" {
		t.Fatal("le jeton invalide doit être oublié")
	}
}

func TestRenameMoveDeleteUseRightMethods(t *testing.T) {
	cases := []struct {
		name   string
		run    func(*Client) error
		path   string
		key    string
		extra  string
		extraV string
	}{
		{"rename file", func(c *Client) error { return c.Rename(context.Background(), Item{FileID: 5}, "n") }, "/renamefile", "fileid", "toname", "n"},
		{"rename folder", func(c *Client) error { return c.Rename(context.Background(), Item{IsFolder: true, FolderID: 5}, "n") }, "/renamefolder", "folderid", "toname", "n"},
		{"move file", func(c *Client) error { return c.Move(context.Background(), Item{FileID: 5}, 8) }, "/renamefile", "fileid", "tofolderid", "8"},
		{"delete file", func(c *Client) error { return c.Delete(context.Background(), Item{FileID: 5}) }, "/deletefile", "fileid", "", ""},
		{"delete folder", func(c *Client) error { return c.Delete(context.Background(), Item{IsFolder: true, FolderID: 5}) }, "/deletefolderrecursive", "folderid", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, c := newFake(t, map[string]any{tc.path: map[string]any{"result": 0}})
			if err := tc.run(c); err != nil {
				t.Fatal(err)
			}
			if f.last.URL.Path != tc.path || f.form[tc.key][0] != "5" {
				t.Fatalf("path=%s form=%v", f.last.URL.Path, f.form)
			}
			if tc.extra != "" && f.form[tc.extra][0] != tc.extraV {
				t.Fatalf("%s = %v", tc.extra, f.form[tc.extra])
			}
		})
	}
}

func TestDownloadReportsProgress(t *testing.T) {
	content := bytes.Repeat([]byte("x"), 100_000)
	var srvURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/getfilelink", func(w http.ResponseWriter, r *http.Request) {
		host := strings.TrimPrefix(srvURL, "http://")
		_ = json.NewEncoder(w).Encode(map[string]any{"result": 0, "hosts": []string{host}, "path": "/dl/file"})
	})
	mux.HandleFunc("/dl/file", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(content)))
		_, _ = w.Write(content)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL
	c := New()
	c.base = srv.URL

	var buf bytes.Buffer
	var lastDone, lastTotal int64
	err := c.Download(context.Background(), 1, &buf, func(done, total int64) {
		lastDone, lastTotal = done, total
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), content) {
		t.Fatal("contenu différent")
	}
	if lastDone != int64(len(content)) || lastTotal != int64(len(content)) {
		t.Fatalf("progression = %d/%d", lastDone, lastTotal)
	}
}

func TestUploadSendsMultipartWithLength(t *testing.T) {
	var gotLen int64
	var gotCT string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLen = r.ContentLength
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"result":0}`))
	}))
	defer srv.Close()
	c := New()
	c.base = srv.URL
	c.auth = "tok"

	data := []byte("hello pcloud")
	var done int64
	err := c.Upload(context.Background(), 42, "hello.txt", bytes.NewReader(data), int64(len(data)),
		time.Unix(1700000000, 0), func(d, _ int64) { done = d })
	if err != nil {
		t.Fatal(err)
	}
	if done != int64(len(data)) {
		t.Fatalf("progression = %d", done)
	}
	if gotLen != int64(len(gotBody)) {
		t.Fatalf("Content-Length %d != corps %d", gotLen, len(gotBody))
	}
	if !strings.HasPrefix(gotCT, "multipart/form-data") {
		t.Fatalf("Content-Type = %q", gotCT)
	}
	s := string(gotBody)
	for _, want := range []string{"hello pcloud", `filename="hello.txt"`, "42", "tok", "1700000000"} {
		if !strings.Contains(s, want) {
			t.Fatalf("le corps ne contient pas %q", want)
		}
	}
}

func TestUploadPropagatesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":2008,"error":"User is over quota."}`))
	}))
	defer srv.Close()
	c := New()
	c.base = srv.URL
	err := c.Upload(context.Background(), 0, "f", strings.NewReader("x"), 1, time.Time{}, nil)
	var apiErr *APIError
	if err == nil || !asAPIError(err, &apiErr) || apiErr.Code != 2008 {
		t.Fatalf("err = %v", err)
	}
}

func asAPIError(err error, target **APIError) bool {
	e, ok := err.(*APIError)
	if ok {
		*target = e
	}
	return ok
}

func TestVideoVariantsToleratesNumberOrStringFields(t *testing.T) {
	f, c := newFake(t, map[string]any{
		"/getvideolinks": map[string]any{"result": 0, "variants": []map[string]any{
			{"width": 1920, "height": 1080, "isoriginal": true, "videocodec": "h264", "audiocodec": "aac",
				"videobitrate": 8000, "audiobitrate": 192, "fps": "29.97", "duration": "125.5",
				"hosts": []string{"c1.pcloud.com"}, "path": "/orig.mp4"},
			{"width": 1280, "height": 720, "isoriginal": false, "fps": 25, "duration": 125.5,
				"hosts": []string{"c1.pcloud.com"}, "path": "/t720.mp4"},
		}},
	})
	vs, err := c.VideoVariants(context.Background(), 77)
	if err != nil {
		t.Fatal(err)
	}
	if f.form["fileid"][0] != "77" {
		t.Fatalf("form = %v", f.form)
	}
	if len(vs) != 2 || !vs[0].IsOriginal || vs[1].Height != 720 {
		t.Fatalf("variantes = %+v", vs)
	}
	if vs[0].DurationSeconds() != 125.5 || vs[1].DurationSeconds() != 125.5 {
		t.Fatalf("durées = %v / %v", vs[0].DurationSeconds(), vs[1].DurationSeconds())
	}
	if vs[1].FPS != "25" {
		t.Fatalf("fps = %q", vs[1].FPS)
	}
	u, err := c.VariantURL(vs[1])
	if err != nil || !strings.HasSuffix(u, "c1.pcloud.com/t720.mp4") {
		t.Fatalf("url = %q, err = %v", u, err)
	}
}

func TestVideoLinkSendsFixedBitrateAndOptions(t *testing.T) {
	f, c := newFake(t, map[string]any{
		"/getvideolink": map[string]any{"result": 0, "hosts": []string{"h.pcloud.com"}, "path": "/v"},
	})
	u, err := c.VideoLink(context.Background(), 5, VideoOptions{Resolution: "854x480", VideoBitrate: 1500, AudioBitrate: 128})
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"fileid": "5", "fixedbitrate": "1", "resolution": "854x480", "vbitrate": "1500", "abitrate": "128",
	} {
		if got := f.form[k]; len(got) != 1 || got[0] != want {
			t.Errorf("%s = %v, want %s", k, got, want)
		}
	}
	if !strings.HasSuffix(u, "h.pcloud.com/v") {
		t.Fatalf("url = %q", u)
	}
	// Valeurs par défaut : rien d'optionnel n'est envoyé.
	if _, err := c.VideoLink(context.Background(), 5, VideoOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"resolution", "vbitrate", "abitrate"} {
		if _, ok := f.form[k]; ok {
			t.Errorf("%s ne devrait pas être envoyé", k)
		}
	}
}
