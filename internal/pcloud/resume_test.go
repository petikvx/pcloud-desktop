package pcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func testData(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + i/251)
	}
	return b
}

func fastClient(base string) *Client {
	c := New()
	c.base = base
	c.retryDelay = time.Millisecond
	return c
}

// rangeServer sert content avec prise en charge de Range. cutAfter > 0 : la
// première réponse est coupée après cutAfter octets.
type rangeServer struct {
	content   []byte
	etag      string
	ignore    bool // ignore Range : répond toujours 200
	cutAfter  int
	mu        sync.Mutex
	ranges    []string
	ifRanges  []string
	requests  int
	cutsLeft  int
	forceCode int // si non nul, répond ce code
}

func (s *rangeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests++
	s.ranges = append(s.ranges, r.Header.Get("Range"))
	s.ifRanges = append(s.ifRanges, r.Header.Get("If-Range"))
	cut := s.cutsLeft > 0
	if cut {
		s.cutsLeft--
	}
	s.mu.Unlock()

	if s.forceCode != 0 {
		http.Error(w, "boom", s.forceCode)
		return
	}
	if s.etag != "" {
		w.Header().Set("ETag", s.etag)
	}
	body := s.content
	status := http.StatusOK
	if rg := r.Header.Get("Range"); rg != "" && !s.ignore {
		if ir := r.Header.Get("If-Range"); ir == "" || ir == s.etag {
			var from int
			fmt.Sscanf(rg, "bytes=%d-", &from)
			if from >= len(s.content) {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(s.content)))
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			body = s.content[from:]
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, len(s.content)-1, len(s.content)))
			status = http.StatusPartialContent
		}
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if cut {
		_, _ = w.Write(body[:s.cutAfter])
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}
	_, _ = w.Write(body)
}

// snap retourne requêtes et en-têtes Range / If-Range reçus.
func (s *rangeServer) snap() (n int, ranges, ifRanges []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests, append([]string(nil), s.ranges...), append([]string(nil), s.ifRanges...)
}

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fixedLink(u string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return u, nil }
}

func TestDownloadResumesAfterConnectionCut(t *testing.T) {
	s := &rangeServer{content: testData(100_000), cutAfter: 30_000, cutsLeft: 1, etag: `"v1"`}
	srv := httptest.NewServer(s)
	defer srv.Close()
	c := fastClient(srv.URL)

	dest := filepath.Join(t.TempDir(), "out.bin")
	var starts []int64
	err := c.DownloadURLTo(context.Background(), fixedLink(srv.URL), dest, "tag1", func(done, total int64) {
		if len(starts) == 0 || total > 0 && done == 0 {
			starts = append(starts, done)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readFile(t, dest), s.content) {
		t.Fatal("contenu différent après reprise")
	}
	n, ranges, ifRanges := s.snap()
	if n != 2 {
		t.Fatalf("requêtes = %d, attendu 2", n)
	}
	if ranges[0] != "" || !strings.HasPrefix(ranges[1], "bytes=") || ranges[1] == "bytes=0-" {
		t.Fatalf("Range = %q", ranges)
	}
	if ifRanges[1] != `"v1"` {
		t.Fatalf("If-Range = %q", ifRanges[1])
	}
	for _, p := range []string{dest + partSuffix, dest + metaSuffix} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s devrait avoir disparu", p)
		}
	}
}

func TestDownloadResumesFromExistingPart(t *testing.T) {
	content := testData(50_000)
	s := &rangeServer{content: content, etag: `"v1"`}
	srv := httptest.NewServer(s)
	defer srv.Close()
	c := fastClient(srv.URL)

	dest := filepath.Join(t.TempDir(), "out.bin")
	_ = os.WriteFile(dest+partSuffix, content[:20_000], 0o644)
	meta, _ := json.Marshal(partMeta{Tag: "tag1", ETag: `"v1"`})
	_ = os.WriteFile(dest+metaSuffix, meta, 0o644)

	var first int64 = -1
	err := c.DownloadURLTo(context.Background(), fixedLink(srv.URL), dest, "tag1", func(done, total int64) {
		if first < 0 {
			first = done
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if first != 20_000 {
		t.Fatalf("progression initiale = %d, attendu 20000", first)
	}
	if _, ranges, _ := s.snap(); ranges[0] != "bytes=20000-" {
		t.Fatalf("Range = %q", ranges[0])
	}
	if !bytes.Equal(readFile(t, dest), content) {
		t.Fatal("contenu différent")
	}
}

func TestDownloadDiscardsPartOfOtherVersion(t *testing.T) {
	content := testData(10_000)
	s := &rangeServer{content: content}
	srv := httptest.NewServer(s)
	defer srv.Close()
	c := fastClient(srv.URL)

	dest := filepath.Join(t.TempDir(), "out.bin")
	_ = os.WriteFile(dest+partSuffix, bytes.Repeat([]byte{9}, 4_000), 0o644)
	meta, _ := json.Marshal(partMeta{Tag: "old"})
	_ = os.WriteFile(dest+metaSuffix, meta, 0o644)

	if err := c.DownloadURLTo(context.Background(), fixedLink(srv.URL), dest, "new", nil); err != nil {
		t.Fatal(err)
	}
	if _, ranges, _ := s.snap(); ranges[0] != "" {
		t.Fatalf("Range = %q : l'ancien partiel aurait dû être jeté", ranges[0])
	}
	if !bytes.Equal(readFile(t, dest), content) {
		t.Fatal("contenu différent")
	}
}

func TestDownloadRangeIgnoredRestartsCleanly(t *testing.T) {
	content := testData(30_000)
	s := &rangeServer{content: content, ignore: true}
	srv := httptest.NewServer(s)
	defer srv.Close()
	c := fastClient(srv.URL)

	dest := filepath.Join(t.TempDir(), "out.bin")
	_ = os.WriteFile(dest+partSuffix, content[:10_000], 0o644)
	meta, _ := json.Marshal(partMeta{Tag: "t"})
	_ = os.WriteFile(dest+metaSuffix, meta, 0o644)

	if err := c.DownloadURLTo(context.Background(), fixedLink(srv.URL), dest, "t", nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readFile(t, dest), content) {
		t.Fatal("un 200 après Range ne doit pas être ajouté au partiel")
	}
}

func TestDownloadCompletePartIsFinalized(t *testing.T) {
	content := testData(8_000)
	s := &rangeServer{content: content}
	srv := httptest.NewServer(s)
	defer srv.Close()
	c := fastClient(srv.URL)

	dest := filepath.Join(t.TempDir(), "out.bin")
	_ = os.WriteFile(dest+partSuffix, content, 0o644)
	meta, _ := json.Marshal(partMeta{Tag: "t"})
	_ = os.WriteFile(dest+metaSuffix, meta, 0o644)

	if err := c.DownloadURLTo(context.Background(), fixedLink(srv.URL), dest, "t", nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readFile(t, dest), content) {
		t.Fatal("contenu différent")
	}
}

func TestDownloadGivesUpOnClientError(t *testing.T) {
	s := &rangeServer{content: testData(10), forceCode: http.StatusForbidden}
	srv := httptest.NewServer(s)
	defer srv.Close()
	c := fastClient(srv.URL)

	dest := filepath.Join(t.TempDir(), "out.bin")
	err := c.DownloadURLTo(context.Background(), fixedLink(srv.URL), dest, "t", nil)
	if err == nil {
		t.Fatal("erreur attendue")
	}
	if n, _, _ := s.snap(); n != 1 {
		t.Fatalf("requêtes = %d : un 403 ne doit pas être réessayé", n)
	}
}

func TestDownloadRetriesServerErrorThenFails(t *testing.T) {
	s := &rangeServer{content: testData(10), forceCode: http.StatusBadGateway}
	srv := httptest.NewServer(s)
	defer srv.Close()
	c := fastClient(srv.URL)
	c.retries = 2

	err := c.DownloadURLTo(context.Background(), fixedLink(srv.URL), filepath.Join(t.TempDir(), "o"), "t", nil)
	if err == nil {
		t.Fatal("erreur attendue")
	}
	if n, _, _ := s.snap(); n != 3 {
		t.Fatalf("requêtes = %d, attendu 3 (1 + 2 reprises)", n)
	}
}

func TestDownloadCancelKeepsPart(t *testing.T) {
	content := testData(200_000)
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		_, _ = w.Write(content[:50_000])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := fastClient(srv.URL)

	dest := filepath.Join(t.TempDir(), "out.bin")
	err := c.DownloadURLTo(ctx, fixedLink(srv.URL), dest, "t", func(done, total int64) {
		if done >= 50_000 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if fi, err := os.Stat(dest + partSuffix); err != nil || fi.Size() < 50_000 {
		t.Fatalf("le partiel doit être conservé : %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("la destination ne doit pas exister")
	}
}

func TestParseContentRange(t *testing.T) {
	cases := []struct {
		in           string
		start, total int64
		ok           bool
	}{
		{"bytes 100-199/1000", 100, 1000, true},
		{"bytes */1000", -1, 1000, true},
		{"bytes 0-9/*", 0, -1, true},
		{"", 0, 0, false},
		{"items 1-2/3", 0, 0, false},
	}
	for _, c := range cases {
		s, tot, ok := parseContentRange(c.in)
		if ok != c.ok || (ok && (s != c.start || tot != c.total)) {
			t.Errorf("%q : %d %d %v", c.in, s, tot, ok)
		}
	}
}

// ------------------------------------------------------------- envoi

// uploadServer simule upload_create / upload_info / upload_write / upload_save.
type uploadServer struct {
	mu       sync.Mutex
	nextID   uint64
	uploads  map[uint64][]byte
	saved    map[string][]byte
	savedMT  string
	writes   []int64 // décalages reçus
	failAt   int     // n-ième upload_write (1-based) : coupé en plein corps
	failKeep int     // octets conservés avant la coupure
	nWrites  int
	infoGone bool // upload_info : envoi inconnu
	creates  int
}

// state retourne une copie de l'état, pour lecture depuis le test.
func (s *uploadServer) state() (writes []int64, creates int, savedMT string, saved map[string][]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	saved = map[string][]byte{}
	for k, v := range s.saved {
		saved[k] = v
	}
	return append([]int64(nil), s.writes...), s.creates, s.savedMT, saved
}

func newUploadServer() *uploadServer {
	return &uploadServer{uploads: map[uint64][]byte{}, saved: map[string][]byte{}, nextID: 41}
}

func (s *uploadServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	_ = r.ParseForm()
	id, _ := strconv.ParseUint(r.Form.Get("uploadid"), 10, 64)
	switch r.URL.Path {
	case "/upload_create":
		s.creates++
		s.nextID++
		s.uploads[s.nextID] = nil
		reply(map[string]any{"result": 0, "uploadid": s.nextID})
	case "/upload_info":
		data, ok := s.uploads[id]
		if !ok || s.infoGone {
			reply(map[string]any{"result": 2064, "error": "Upload not found."})
			return
		}
		reply(map[string]any{"result": 0, "uploadid": id, "size": len(data)})
	case "/upload_write":
		s.nWrites++
		off, _ := strconv.ParseInt(r.URL.Query().Get("uploadoffset"), 10, 64)
		if r.URL.Query().Get("auth") != "tok" {
			reply(map[string]any{"result": 1000, "error": "no auth"})
			return
		}
		s.writes = append(s.writes, off)
		body, _ := io.ReadAll(r.Body)
		cur := s.uploads[id]
		if int64(len(cur)) != off {
			reply(map[string]any{"result": 2000, "error": "bad offset"})
			return
		}
		if s.failAt == s.nWrites {
			keep := min(s.failKeep, len(body))
			s.uploads[id] = append(cur, body[:keep]...)
			panic(http.ErrAbortHandler)
		}
		s.uploads[id] = append(cur, body...)
		reply(map[string]any{"result": 0})
	case "/upload_save":
		s.saved[r.Form.Get("folderid")+"/"+r.Form.Get("name")] = s.uploads[id]
		s.savedMT = r.Form.Get("mtime")
		delete(s.uploads, id)
		reply(map[string]any{"result": 0})
	default:
		http.NotFound(w, r)
	}
}

type memStore struct{ id uint64 }

func (m *memStore) Get() uint64   { return m.id }
func (m *memStore) Put(id uint64) { m.id = id }
func (m *memStore) Clear()        { m.id = 0 }

func uploadClient(srv *httptest.Server) *Client {
	c := fastClient(srv.URL)
	c.auth = "tok"
	c.chunkSize = 1000
	return c
}

func TestUploadChunkedResumesAfterCut(t *testing.T) {
	s := newUploadServer()
	s.failAt, s.failKeep = 3, 400 // 3e morceau : 400 octets reçus puis coupure
	srv := httptest.NewServer(s)
	defer srv.Close()
	c := uploadClient(srv)

	content := testData(4_500)
	st := &memStore{}
	var last int64
	err := c.UploadFile(context.Background(), 7, "big.bin", bytes.NewReader(content), int64(len(content)),
		time.Unix(1700000000, 0), st, func(done, total int64) { last = done })
	if err != nil {
		t.Fatal(err)
	}
	writes, creates, savedMT, saved := s.state()
	_ = creates
	if got := saved["7/big.bin"]; !bytes.Equal(got, content) {
		t.Fatalf("contenu enregistré : %d octets, attendu %d", len(got), len(content))
	}
	if last != int64(len(content)) {
		t.Fatalf("dernière progression = %d", last)
	}
	// Le 3e morceau (à 2000) est coupé après 400 octets : la reprise repart de
	// 2400 et non de 2000.
	want := []int64{0, 1000, 2000, 2400, 3400, 4400}
	if fmt.Sprint(writes) != fmt.Sprint(want) {
		t.Fatalf("décalages = %v, attendu %v", writes, want)
	}
	if savedMT != "1700000000" {
		t.Fatalf("mtime = %q", savedMT)
	}
	if st.id != 0 {
		t.Fatal("l'identifiant mémorisé doit être effacé après l'envoi")
	}
}

func TestUploadChunkedResumesFromStoredID(t *testing.T) {
	s := newUploadServer()
	content := testData(3_500)
	s.uploads[99] = append([]byte(nil), content[:2_000]...)
	srv := httptest.NewServer(s)
	defer srv.Close()
	c := uploadClient(srv)

	st := &memStore{id: 99}
	err := c.UploadFile(context.Background(), 1, "f", bytes.NewReader(content), int64(len(content)), time.Time{}, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	writes, creates, _, saved := s.state()
	if creates != 0 {
		t.Fatal("upload_create ne doit pas être appelé")
	}
	if fmt.Sprint(writes) != "[2000 3000]" {
		t.Fatalf("décalages = %v", writes)
	}
	if !bytes.Equal(saved["1/f"], content) {
		t.Fatal("contenu différent")
	}
}

func TestUploadChunkedStaleIDStartsOver(t *testing.T) {
	s := newUploadServer()
	srv := httptest.NewServer(s)
	defer srv.Close()
	c := uploadClient(srv)

	content := testData(2_500)
	st := &memStore{id: 12345} // inconnu du serveur
	if err := c.UploadFile(context.Background(), 1, "f", bytes.NewReader(content), int64(len(content)), time.Time{}, st, nil); err != nil {
		t.Fatal(err)
	}
	if _, creates, _, saved := s.state(); creates != 1 || !bytes.Equal(saved["1/f"], content) {
		t.Fatalf("creates = %d", creates)
	}
}

func TestUploadChunkedKeepsIDOnFailure(t *testing.T) {
	s := newUploadServer()
	s.failAt, s.failKeep = 2, 0
	srv := httptest.NewServer(s)
	defer srv.Close()
	c := uploadClient(srv)
	c.retries = 0 // pas de reprise automatique

	content := testData(3_000)
	st := &memStore{}
	err := c.UploadFile(context.Background(), 1, "f", bytes.NewReader(content), int64(len(content)), time.Time{}, st, nil)
	if err == nil {
		t.Fatal("erreur attendue")
	}
	if st.id == 0 {
		t.Fatal("l'identifiant doit rester mémorisé pour reprendre plus tard")
	}
	// Seconde exécution : reprend à 1000.
	s.mu.Lock()
	s.failAt = 0
	s.mu.Unlock()
	if err := c.UploadFile(context.Background(), 1, "f", bytes.NewReader(content), int64(len(content)), time.Time{}, st, nil); err != nil {
		t.Fatal(err)
	}
	_, creates, _, saved := s.state()
	if creates != 1 {
		t.Fatalf("creates = %d, attendu 1", creates)
	}
	if !bytes.Equal(saved["1/f"], content) {
		t.Fatal("contenu différent")
	}
}

func TestUploadSmallFileSingleRequestWithRetry(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		n := hits
		mu.Unlock()
		_, _ = io.Copy(io.Discard, r.Body)
		if n == 1 {
			panic(http.ErrAbortHandler)
		}
		_, _ = w.Write([]byte(`{"result":0}`))
	}))
	defer srv.Close()
	c := fastClient(srv.URL)

	content := testData(500)
	if err := c.UploadFile(context.Background(), 1, "small", bytes.NewReader(content), int64(len(content)), time.Time{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 2 {
		t.Fatalf("requêtes = %d, attendu 2", hits)
	}
}
