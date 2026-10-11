package hls

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// DefaultParallel est le nombre de segments téléchargés simultanément par défaut.
const DefaultParallel = 6

// Downloader récupère des playlists et des segments.
type Downloader struct {
	Client   *http.Client
	Parallel int // segments téléchargés simultanément (défaut DefaultParallel)
	Retries  int // essais supplémentaires par segment (défaut 2)

	mu   sync.Mutex
	keys map[string][]byte
}

// NewDownloader retourne un Downloader aux réglages par défaut.
func NewDownloader() *Downloader {
	return &Downloader{Client: &http.Client{}, Parallel: DefaultParallel, Retries: 2}
}

// get lit une ressource http(s) ou un fichier local (chemin ou file://).
// rng non nul : demande la plage [off, off+length).
// onBytes (facultatif) est appelé au fil de la lecture, parfois depuis
// plusieurs goroutines : il doit être sûr en concurrence.
func (d *Downloader) get(ctx context.Context, raw string, off, length int64, onBytes func(n int)) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		path := raw
		if u.Scheme == "file" {
			path = u.Path
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if onBytes != nil {
			onBytes(len(data))
		}
		return slice(data, off, length)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	if length > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+length-1))
	}
	resp, err := d.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("%s : %s", raw, resp.Status)
	}
	var body io.Reader = resp.Body
	if onBytes != nil {
		body = &countingReader{r: resp.Body, on: onBytes}
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	if length > 0 && resp.StatusCode == http.StatusOK {
		// Le serveur a ignoré Range : on découpe nous-mêmes.
		return slice(data, off, length)
	}
	return data, nil
}

// countingReader signale les octets lus.
type countingReader struct {
	r  io.Reader
	on func(n int)
}

func (c *countingReader) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	if n > 0 {
		c.on(n)
	}
	return n, err
}

func slice(data []byte, off, length int64) ([]byte, error) {
	if length <= 0 {
		return data, nil
	}
	if off < 0 || off+length > int64(len(data)) {
		return nil, errors.New("byte range hors du fichier")
	}
	return data[off : off+length], nil
}

// FetchPlaylist télécharge (ou lit) et analyse une playlist. raw est une URL
// http(s) ou un chemin de fichier local.
func (d *Downloader) FetchPlaylist(ctx context.Context, raw string) (*Master, *Media, error) {
	data, err := d.get(ctx, raw, 0, 0, nil)
	if err != nil {
		return nil, nil, err
	}
	base, err := baseURL(raw)
	if err != nil {
		return nil, nil, err
	}
	return Parse(bytes.NewReader(data), base)
}

// baseURL retourne l'URL servant à résoudre les chemins relatifs.
func baseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" {
		// Chemin local : on le transforme en file:// pour résoudre les relatifs.
		u = &url.URL{Scheme: "file", Path: raw}
	}
	return u, nil
}

// Options règle un téléchargement.
type Options struct {
	// OnBytes (facultatif) reçoit les octets reçus du réseau au fil de l'eau,
	// depuis plusieurs goroutines : il doit être sûr en concurrence. Les
	// reprises après erreur comptent à nouveau les octets relus.
	OnBytes func(n int)

	// WebVTT retire les en-têtes « WEBVTT » répétés quand on concatène des
	// segments de sous-titres.
	WebVTT bool
}

// Download écrit dans w, dans l'ordre, tous les segments de m (précédés du
// segment d'initialisation s'il existe). progress reçoit (segments faits, total).
func (d *Downloader) Download(ctx context.Context, m *Media, w io.Writer, opt Options, progress func(done, total int)) error {
	if !m.EndList {
		return errors.New("flux en direct (sans #EXT-X-ENDLIST) : non géré")
	}
	if m.Init != nil {
		data, err := d.get(ctx, m.Init.URI, m.Init.Offset, m.Init.Length, opt.OnBytes)
		if err != nil {
			return fmt.Errorf("segment d'initialisation : %w", err)
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
	}

	par := d.Parallel
	if par < 1 {
		par = 4
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		data []byte
		err  error
	}
	n := len(m.Segments)
	results := make([]chan result, n)
	for i := range results {
		results[i] = make(chan result, 1)
	}
	// sem borne le nombre de segments « en vol ou non encore écrits » (mémoire).
	sem := make(chan struct{}, par)
	go func() {
		for i := range m.Segments {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			go func(i int) {
				data, err := d.segment(ctx, m.Segments[i], opt.OnBytes)
				results[i] <- result{data, err}
			}(i)
		}
	}()

	for i := 0; i < n; i++ {
		var r result
		select {
		case r = <-results[i]:
		case <-ctx.Done():
			return ctx.Err()
		}
		<-sem
		if r.err != nil {
			return fmt.Errorf("segment %d/%d : %w", i+1, n, r.err)
		}
		data := r.data
		if opt.WebVTT && i > 0 {
			data = stripWebVTTHeader(data)
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
		if progress != nil {
			progress(i+1, n)
		}
	}
	return nil
}

// segment télécharge un segment avec quelques essais, et le déchiffre.
func (d *Downloader) segment(ctx context.Context, s Segment, onBytes func(n int)) ([]byte, error) {
	var data []byte
	var err error
	for attempt := 0; attempt <= d.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		data, err = d.get(ctx, s.URI, s.Offset, s.Length, onBytes)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	if s.Key != nil {
		return d.decrypt(ctx, data, s)
	}
	return data, nil
}

func (d *Downloader) decrypt(ctx context.Context, data []byte, s Segment) ([]byte, error) {
	key, err := d.key(ctx, s.Key.URI)
	if err != nil {
		return nil, fmt.Errorf("clé de déchiffrement : %w", err)
	}
	iv := s.Key.IV
	if iv == nil {
		iv = make([]byte, 16)
		binary.BigEndian.PutUint64(iv[8:], uint64(s.Seq))
	}
	return decryptAES128(data, key, iv)
}

// key retourne la clé de déchiffrement. La récupération se fait sous verrou :
// quand plusieurs segments démarrent ensemble, une seule requête part.
func (d *Downloader) key(ctx context.Context, uri string) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if k, ok := d.keys[uri]; ok {
		return k, nil
	}
	k, err := d.get(ctx, uri, 0, 0, nil)
	if err != nil {
		return nil, err
	}
	if len(k) != 16 {
		return nil, fmt.Errorf("clé de %d octets (16 attendus)", len(k))
	}
	if d.keys == nil {
		d.keys = map[string][]byte{}
	}
	d.keys[uri] = k
	return k, nil
}

func decryptAES128(data, key, iv []byte) ([]byte, error) {
	if len(data)%aes.BlockSize != 0 {
		return nil, errors.New("segment chiffré de longueur invalide")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
	if len(out) == 0 {
		return out, nil
	}
	pad := int(out[len(out)-1])
	if pad < 1 || pad > aes.BlockSize || pad > len(out) {
		return nil, errors.New("bourrage PKCS#7 invalide (mauvaise clé ?)")
	}
	return out[:len(out)-pad], nil
}

// stripWebVTTHeader retire le bloc d'en-tête (jusqu'à la première ligne vide)
// d'un segment WebVTT.
func stripWebVTTHeader(data []byte) []byte {
	if !bytes.HasPrefix(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), []byte("WEBVTT")) {
		return data
	}
	s := string(data)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if i := strings.Index(s, "\n\n"); i >= 0 {
		return []byte(s[i+2:])
	}
	return nil
}
