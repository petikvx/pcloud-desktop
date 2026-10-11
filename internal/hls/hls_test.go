package hls

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const masterSample = `#EXTM3U
#EXT-X-VERSION:6
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="Français",LANGUAGE="fr",DEFAULT=YES,AUTOSELECT=YES,URI="audio/fr.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="English, original",LANGUAGE="en",DEFAULT=NO,URI="audio/en.m3u8"
#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="sub",NAME="Français",LANGUAGE="fr",URI="sub/fr.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=2500000,AVERAGE-BANDWIDTH=2000000,RESOLUTION=1280x720,FRAME-RATE=29.970,CODECS="avc1.4d401f,mp4a.40.2",AUDIO="aud",SUBTITLES="sub"
720p/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360,CODECS="avc1.42c01e,mp4a.40.2",AUDIO="aud"
https://cdn.example.com/360p/index.m3u8
`

func mustParse(t *testing.T, text, base string) (*Master, *Media) {
	t.Helper()
	b, _ := url.Parse(base)
	m, md, err := Parse(strings.NewReader(text), b)
	if err != nil {
		t.Fatal(err)
	}
	return m, md
}

func TestParseMaster(t *testing.T) {
	m, md := mustParse(t, masterSample, "https://host.example/vid/master.m3u8")
	if md != nil || m == nil {
		t.Fatal("playlist maître attendue")
	}
	if len(m.Variants) != 2 {
		t.Fatalf("variantes = %d", len(m.Variants))
	}
	v := m.Variants[0]
	if v.URI != "https://host.example/vid/720p/index.m3u8" {
		t.Errorf("URI relative mal résolue : %q", v.URI)
	}
	if v.Width != 1280 || v.Height != 720 || v.Bandwidth != 2500000 || v.AvgBandwidth != 2000000 || v.FrameRate != 29.97 {
		t.Errorf("variante = %+v", v)
	}
	if v.Codecs != "avc1.4d401f,mp4a.40.2" {
		t.Errorf("CODECS avec virgule mal lu : %q", v.Codecs)
	}
	if m.Variants[1].URI != "https://cdn.example.com/360p/index.m3u8" {
		t.Errorf("URI absolue modifiée : %q", m.Variants[1].URI)
	}

	audio := m.AudioFor(v)
	if len(audio) != 2 || audio[0].Language != "fr" || !audio[0].Default || audio[1].Name != "English, original" {
		t.Fatalf("audio = %+v", audio)
	}
	if audio[0].URI != "https://host.example/vid/audio/fr.m3u8" {
		t.Errorf("URI audio = %q", audio[0].URI)
	}
	if subs := m.SubtitlesFor(v); len(subs) != 1 || subs[0].Language != "fr" {
		t.Fatalf("sous-titres = %+v", subs)
	}
	if len(m.SubtitlesFor(m.Variants[1])) != 0 {
		t.Fatal("la 2e variante n'a pas de sous-titres")
	}
}

func TestParseMediaByteRangesKeyAndMap(t *testing.T) {
	text := `#EXTM3U
#EXT-X-TARGETDURATION:6
#EXT-X-MEDIA-SEQUENCE:7
#EXT-X-MAP:URI="init.mp4",BYTERANGE="100@0"
#EXT-X-KEY:METHOD=AES-128,URI="key.bin",IV=0x000102030405060708090a0b0c0d0e0f
#EXTINF:4.5,
#EXT-X-BYTERANGE:1000@100
media.mp4
#EXTINF:5.5,
#EXT-X-BYTERANGE:2000
media.mp4
#EXT-X-KEY:METHOD=NONE
#EXTINF:3,
#EXT-X-BYTERANGE:50
other.mp4
#EXT-X-ENDLIST
`
	_, m := mustParse(t, text, "https://h.example/a/p.m3u8")
	if m == nil || len(m.Segments) != 3 || !m.EndList {
		t.Fatalf("media = %+v", m)
	}
	if m.Init == nil || m.Init.URI != "https://h.example/a/init.mp4" || m.Init.Length != 100 {
		t.Errorf("init = %+v", m.Init)
	}
	s0, s1, s2 := m.Segments[0], m.Segments[1], m.Segments[2]
	if s0.Offset != 100 || s0.Length != 1000 {
		t.Errorf("s0 = %+v", s0)
	}
	if s1.Offset != 1100 || s1.Length != 2000 {
		t.Errorf("offset implicite : s1 = %+v", s1)
	}
	if s2.Offset != 0 || s2.Length != 50 {
		t.Errorf("autre fichier : s2 = %+v", s2)
	}
	if s0.Seq != 7 || s1.Seq != 8 || s2.Seq != 9 {
		t.Errorf("séquences = %d %d %d", s0.Seq, s1.Seq, s2.Seq)
	}
	if s0.Key == nil || s0.Key.URI != "https://h.example/a/key.bin" || len(s0.Key.IV) != 16 || s0.Key.IV[15] != 0x0f {
		t.Errorf("clé s0 = %+v", s0.Key)
	}
	if s2.Key != nil {
		t.Error("METHOD=NONE doit supprimer la clé")
	}
	if m.Duration != 13 {
		t.Errorf("durée = %v", m.Duration)
	}
}

func TestParseRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"pas une playlist": "<html>hello</html>",
		"sans segment":     "#EXTM3U\n#EXT-X-ENDLIST\n",
		"sample-aes":       "#EXTM3U\n#EXT-X-KEY:METHOD=SAMPLE-AES,URI=\"k\"\n#EXTINF:1,\na.ts\n",
	}
	for name, text := range cases {
		if _, _, err := Parse(strings.NewReader(text), nil); err == nil {
			t.Errorf("%s : erreur attendue", name)
		}
	}
}

func TestParseAttrs(t *testing.T) {
	a := parseAttrs(`A=1,B="x,y",C=z, D="q"`)
	if a["A"] != "1" || a["B"] != "x,y" || a["C"] != "z" || a["D"] != "q" {
		t.Fatalf("attrs = %v", a)
	}
}

// encrypt chiffre en AES-128-CBC avec bourrage PKCS#7.
func encrypt(t *testing.T, data, key, iv []byte) []byte {
	t.Helper()
	pad := aes.BlockSize - len(data)%aes.BlockSize
	padded := append(append([]byte{}, data...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
	return out
}

func TestDownloadOrderedEncryptedParallel(t *testing.T) {
	key := []byte("0123456789abcdef")
	const n = 12
	plain := make([][]byte, n)
	mux := http.NewServeMux()
	var playlist strings.Builder
	playlist.WriteString("#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:5\n#EXT-X-KEY:METHOD=AES-128,URI=\"key\"\n")
	for i := 0; i < n; i++ {
		plain[i] = []byte(fmt.Sprintf("segment-%02d-%s;", i, strings.Repeat("x", 100+i)))
		iv := make([]byte, 16) // IV dérivé de la séquence (5+i)
		binary.BigEndian.PutUint64(iv[8:], uint64(5+i))
		enc := encrypt(t, plain[i], key, iv)
		delay := time.Duration((n-i)%4) * 15 * time.Millisecond // finissent dans le désordre
		mux.HandleFunc(fmt.Sprintf("/s%d.ts", i), func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(delay)
			_, _ = w.Write(enc)
		})
		fmt.Fprintf(&playlist, "#EXTINF:2,\ns%d.ts\n", i)
	}
	playlist.WriteString("#EXT-X-ENDLIST\n")
	var keyHits int32
	mux.HandleFunc("/key", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&keyHits, 1)
		_, _ = w.Write(key)
	})
	mux.HandleFunc("/p.m3u8", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(playlist.String())) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := NewDownloader()
	_, m, err := d.FetchPlaylist(context.Background(), srv.URL+"/p.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	var lastDone, lastTotal int
	err = d.Download(context.Background(), m, &out, Options{}, func(done, total int) { lastDone, lastTotal = done, total })
	if err != nil {
		t.Fatal(err)
	}
	if want := bytes.Join(plain, nil); !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("contenu ou ordre incorrect (%d octets, %d attendus)", out.Len(), len(want))
	}
	if lastDone != n || lastTotal != n {
		t.Fatalf("progression = %d/%d", lastDone, lastTotal)
	}
	if hits := atomic.LoadInt32(&keyHits); hits != 1 {
		t.Fatalf("la clé doit être mise en cache : %d requêtes", hits)
	}
}

func TestDownloadByteRangeAndInit(t *testing.T) {
	file := []byte("0123456789ABCDEFGHIJ")
	var mu sync.Mutex
	var gotRange []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/media.mp4":
			mu.Lock()
			gotRange = append(gotRange, r.Header.Get("Range"))
			mu.Unlock()
			http.ServeContent(w, r, "media.mp4", time.Time{}, bytes.NewReader(file))
		case "/init.mp4":
			_, _ = w.Write([]byte("INIT"))
		}
	}))
	defer srv.Close()
	text := `#EXTM3U
#EXT-X-MAP:URI="init.mp4"
#EXTINF:1,
#EXT-X-BYTERANGE:5@2
media.mp4
#EXTINF:1,
#EXT-X-BYTERANGE:3
media.mp4
#EXT-X-ENDLIST
`
	base, _ := url.Parse(srv.URL + "/p.m3u8")
	_, m, err := Parse(strings.NewReader(text), base)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := NewDownloader().Download(context.Background(), m, &out, Options{}, nil); err != nil {
		t.Fatal(err)
	}
	if out.String() != "INIT"+"23456"+"789" {
		t.Fatalf("sortie = %q", out.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(gotRange) != 2 || gotRange[0] == "" && gotRange[1] == "" {
		t.Fatalf("ranges = %v", gotRange)
	}
}

func TestDownloadRetriesTransientErrors(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			http.Error(w, "oups", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	m := &Media{EndList: true, Segments: []Segment{{URI: srv.URL + "/s.ts"}}}
	var out bytes.Buffer
	if err := NewDownloader().Download(context.Background(), m, &out, Options{}, nil); err != nil {
		t.Fatal(err)
	}
	if out.String() != "ok" || hits != 2 {
		t.Fatalf("out=%q hits=%d", out.String(), hits)
	}
}

func TestDownloadFailsAfterRetriesAndRejectsLive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	d := NewDownloader()
	d.Retries = 1
	err := d.Download(context.Background(), &Media{EndList: true, Segments: []Segment{{URI: srv.URL + "/x"}}}, &bytes.Buffer{}, Options{}, nil)
	if err == nil || !strings.Contains(err.Error(), "segment 1/1") {
		t.Fatalf("err = %v", err)
	}
	err = d.Download(context.Background(), &Media{Segments: []Segment{{URI: srv.URL}}}, &bytes.Buffer{}, Options{}, nil)
	if err == nil || !strings.Contains(err.Error(), "direct") {
		t.Fatalf("flux en direct : err = %v", err)
	}
}

func TestDownloadCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	m := &Media{EndList: true, Segments: []Segment{{URI: srv.URL + "/a"}, {URI: srv.URL + "/b"}}}
	start := time.Now()
	err := NewDownloader().Download(ctx, m, &bytes.Buffer{}, Options{}, nil)
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("annulation non respectée : err=%v après %v", err, time.Since(start))
	}
}

func TestLocalFilePlaylist(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.ts"), []byte("AAA"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.ts"), []byte("BBB"), 0o600); err != nil {
		t.Fatal(err)
	}
	pl := filepath.Join(dir, "p.m3u8")
	if err := os.WriteFile(pl, []byte("#EXTM3U\n#EXTINF:1,\na.ts\n#EXTINF:1,\nb.ts\n#EXT-X-ENDLIST\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := NewDownloader()
	_, m, err := d.FetchPlaylist(context.Background(), pl)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := d.Download(context.Background(), m, &out, Options{}, nil); err != nil {
		t.Fatal(err)
	}
	if out.String() != "AAABBB" {
		t.Fatalf("sortie = %q", out.String())
	}
}

func TestWebVTTHeadersStripped(t *testing.T) {
	seg1 := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nBonjour\n\n"
	seg2 := "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000\n\n00:00:05.000 --> 00:00:06.000\nSalut\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/1.vtt" {
			_, _ = w.Write([]byte(seg1))
		} else {
			_, _ = w.Write([]byte(seg2))
		}
	}))
	defer srv.Close()
	m := &Media{EndList: true, Segments: []Segment{{URI: srv.URL + "/1.vtt"}, {URI: srv.URL + "/2.vtt"}}}
	var out bytes.Buffer
	if err := NewDownloader().Download(context.Background(), m, &out, Options{WebVTT: true}, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "WEBVTT") != 1 || !strings.Contains(out.String(), "Salut") ||
		strings.Contains(out.String(), "X-TIMESTAMP-MAP") {
		t.Fatalf("sortie = %q", out.String())
	}
}

func TestDecryptWrongKeyIsDetectedOrGarbage(t *testing.T) {
	key := []byte("0123456789abcdef")
	iv := make([]byte, 16)
	enc := encrypt(t, []byte("hello world"), key, iv)
	got, err := decryptAES128(enc, key, iv)
	if err != nil || string(got) != "hello world" {
		t.Fatalf("got %q, err %v", got, err)
	}
	if _, err := decryptAES128(enc[:5], key, iv); err == nil {
		t.Fatal("longueur invalide : erreur attendue")
	}
}

func TestDownloadReportsBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("z"), 5000))
	}))
	defer srv.Close()
	m := &Media{EndList: true, Segments: []Segment{{URI: srv.URL + "/a"}, {URI: srv.URL + "/b"}, {URI: srv.URL + "/c"}}}
	var got int64
	d := NewDownloader()
	d.Parallel = 3
	err := d.Download(context.Background(), m, &bytes.Buffer{}, Options{OnBytes: func(n int) { atomic.AddInt64(&got, int64(n)) }}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != 15000 {
		t.Fatalf("octets comptés = %d, attendus 15000", got)
	}
}

func TestDefaultParallel(t *testing.T) {
	if NewDownloader().Parallel != 6 {
		t.Fatalf("parallélisme par défaut = %d, attendu 6", NewDownloader().Parallel)
	}
}

const multiAudioMaster = `#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="v1a",NAME="English",LANGUAGE="en",URI="a/en1.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="v1a",NAME="French, low",LANGUAGE="fr",DEFAULT=YES,URI="a/fr1.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="v3a",NAME="English",LANGUAGE="en",URI="a/en3.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="v3a",NAME="French, high",LANGUAGE="fr",DEFAULT=YES,URI="a/fr3.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="v9a",NAME="Dup",LANGUAGE="fr",URI="a/fr3.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=2628000,RESOLUTION=960x540,AUDIO="v1a"
540/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=5192000,RESOLUTION=1920x1080,AUDIO="v3a"
1080/index.m3u8
`

func TestAllRenditionsSpansGroupsWithoutDuplicates(t *testing.T) {
	m, _ := mustParse(t, multiAudioMaster, "https://h.example/m/master.m3u8")
	all := m.AllRenditions("AUDIO")
	if len(all) != 4 {
		t.Fatalf("pistes = %d, attendu 4 (doublon d'URI retiré) : %+v", len(all), all)
	}
	// La variante 540p ne voit que son groupe, mais toutes les versions restent choisissables.
	if got := len(m.AudioFor(m.Variants[0])); got != 2 {
		t.Fatalf("AudioFor(540p) = %d pistes", got)
	}
	groups := map[string]bool{}
	for _, r := range all {
		groups[r.GroupID] = true
	}
	if !groups["v1a"] || !groups["v3a"] || groups["v9a"] {
		t.Fatalf("groupes = %v", groups)
	}
	if len(m.AllRenditions("SUBTITLES")) != 0 {
		t.Fatal("aucun sous-titre attendu")
	}
}

const prefsMaster = `#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="v1a",NAME="English",LANGUAGE="en",URI="a/en1.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="v1a",NAME="French, low",LANGUAGE="fr",URI="a/fr1.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="v2a",NAME="English",LANGUAGE="en",URI="a/en2.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="v2a",NAME="French",LANGUAGE="fr",URI="a/fr2.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="v3a",NAME="English",LANGUAGE="en",URI="a/en3.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="v3a",NAME="French, high",LANGUAGE="fr",DEFAULT=YES,URI="a/fr3.m3u8"
#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="s",NAME="Français",LANGUAGE="fra",URI="s/fr.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=2628000,RESOLUTION=960x540,AUDIO="v1a",SUBTITLES="s"
540/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=3660000,RESOLUTION=1280x720,AUDIO="v2a",SUBTITLES="s"
720/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=5192000,RESOLUTION=1920x1080,AUDIO="v3a",SUBTITLES="s"
1080/index.m3u8
`

func TestSelectVideoByHeight(t *testing.T) {
	m, _ := mustParse(t, prefsMaster, "https://h.example/m/master.m3u8")
	cases := []struct {
		height int
		want   int
	}{{0, 1080}, {1080, 1080}, {900, 720}, {720, 720}, {600, 540}, {400, 540}}
	for _, c := range cases {
		sel, err := m.Select(Preferences{Height: c.height})
		if err != nil {
			t.Fatal(err)
		}
		if sel.Variant.Height != c.want {
			t.Errorf("hauteur %d : variante %d, attendu %d", c.height, sel.Variant.Height, c.want)
		}
	}
	sel, _ := m.Select(Preferences{Height: 400})
	if len(sel.Notes) == 0 {
		t.Error("une note de repli est attendue quand tout dépasse la hauteur visée")
	}
}

func TestSelectAudioVersionsByQuality(t *testing.T) {
	m, _ := mustParse(t, prefsMaster, "https://h.example/m/master.m3u8")
	cases := []struct {
		q    Quality
		want string
	}{{QualityBest, "French, high"}, {QualityMiddle, "French"}, {QualityLowest, "French, low"}}
	for _, c := range cases {
		sel, err := m.Select(Preferences{AudioLangs: []string{"fr"}, AudioQuality: c.q})
		if err != nil {
			t.Fatal(err)
		}
		if len(sel.Audio) != 1 || sel.Audio[0].Name != c.want {
			t.Errorf("qualité %d : %+v, attendu %q", c.q, sel.Audio, c.want)
		}
	}
	// Deux langues, dans l'ordre demandé ; « fra » est reconnu comme « fr ».
	sel, _ := m.Select(Preferences{AudioLangs: []string{"en", "FRA"}, AudioQuality: QualityBest})
	if len(sel.Audio) != 2 || sel.Audio[0].Language != "en" || sel.Audio[1].Name != "French, high" {
		t.Fatalf("audio = %+v", sel.Audio)
	}
}

func TestSelectFallbackAndSubtitles(t *testing.T) {
	m, _ := mustParse(t, prefsMaster, "https://h.example/m/master.m3u8")
	// Langue absente : repli sur la piste par défaut du groupe de la variante (1080p → v3a).
	sel, _ := m.Select(Preferences{AudioLangs: []string{"de"}})
	if len(sel.Audio) != 1 || sel.Audio[0].Name != "French, high" {
		t.Fatalf("repli = %+v", sel.Audio)
	}
	if len(sel.Notes) < 2 {
		t.Errorf("notes = %v", sel.Notes)
	}
	// Sans préférence audio : piste par défaut, jamais de vidéo muette.
	sel, _ = m.Select(Preferences{})
	if len(sel.Audio) != 1 {
		t.Fatalf("audio par défaut = %+v", sel.Audio)
	}
	sel, _ = m.Select(Preferences{SubLangs: []string{"fr", "es"}})
	if len(sel.Subs) != 1 || sel.Subs[0].Name != "Français" {
		t.Fatalf("sous-titres = %+v", sel.Subs)
	}
	if _, err := (*Master)(nil).Select(Preferences{}); err == nil {
		t.Fatal("master nil : erreur attendue")
	}
}

func TestLanguagesSummary(t *testing.T) {
	m, _ := mustParse(t, prefsMaster, "https://h.example/m/master.m3u8")
	langs := Languages(m.AllRenditions("AUDIO"))
	if len(langs) != 2 {
		t.Fatalf("langues = %+v", langs)
	}
	if langs[0].Key != "en" || langs[0].Versions != 3 || langs[1].Key != "fr" || langs[1].Versions != 3 || !langs[1].Default {
		t.Fatalf("langues = %+v", langs)
	}
	if langs[1].Label != "French (fr)" {
		t.Fatalf("libellé = %q", langs[1].Label)
	}
	if LangKey("fr-CA") != "fr" || LangKey("fre") != "fr" || LangKey("") != "" {
		t.Fatal("LangKey")
	}
}
