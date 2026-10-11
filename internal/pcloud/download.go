package pcloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"pcloud-desktop/internal/i18n"
)

// partSuffix est ajouté au nom de destination pour le fichier en cours de
// téléchargement ; metaSuffix, pour les informations qui permettent de le
// reprendre.
const (
	partSuffix = ".part"
	metaSuffix = ".part.json"
)

// partMeta identifie le contenu d'un fichier partiel : il ne peut être repris
// que si le fichier distant est resté le même.
type partMeta struct {
	Tag  string `json:"tag"`            // identité fournie par l'appelant (id, taille, date…)
	ETag string `json:"etag,omitempty"` // validateur du serveur, envoyé en If-Range
}

// errRestart signale que le fichier partiel a été jeté et qu'il faut repartir
// de zéro.
var errRestart = errors.New("reprise impossible : nouveau départ")

// DownloadFileTo télécharge un fichier pCloud vers dest. Le contenu est écrit
// dans dest+".part", repris à l'octet près après une coupure (automatiquement,
// ou en relançant avec le même dest et le même tag), puis renommé en dest.
// tag identifie la version du fichier distant. progress reçoit l'octet de
// départ avant toute écriture, puis le cumul.
func (c *Client) DownloadFileTo(ctx context.Context, fileID uint64, dest, tag string, progress func(done, total int64)) error {
	return c.DownloadURLTo(ctx, func(ctx context.Context) (string, error) {
		return c.FileURL(ctx, fileID)
	}, dest, tag, progress)
}

// DownloadURLTo est comme DownloadFileTo pour une URL de contenu. link est
// rappelée à chaque tentative : les liens pCloud expirent.
func (c *Client) DownloadURLTo(ctx context.Context, link func(ctx context.Context) (string, error), dest, tag string, progress func(done, total int64)) error {
	part, metaPath := dest+partSuffix, dest+metaSuffix

	// Un fichier partiel d'une autre version, ou sans identité, est jeté.
	var meta partMeta
	if b, err := os.ReadFile(metaPath); err != nil || json.Unmarshal(b, &meta) != nil || meta.Tag != tag {
		meta = partMeta{Tag: tag}
		_ = os.Remove(part)
	}

	restarted := false
	err := c.retry(ctx, func() (bool, error) {
		before := fileSize(part)
		err := c.downloadOnce(ctx, link, part, metaPath, &meta, progress)
		if errors.Is(err, errRestart) {
			if restarted { // déjà repartis de zéro une fois : le serveur refuse
				return false, &statusError{code: http.StatusRequestedRangeNotSatisfiable,
					msg: i18n.T("le serveur refuse la reprise du téléchargement")}
			}
			restarted = true
			return true, err
		}
		return fileSize(part) > before, err
	})
	if err != nil {
		return err
	}
	if err := os.Rename(part, dest); err != nil {
		return err
	}
	_ = os.Remove(metaPath)
	return nil
}

func fileSize(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.Size()
	}
	return 0
}

// downloadOnce fait une tentative : demande la suite du fichier partiel
// (en-tête Range) et l'y ajoute.
func (c *Client) downloadOnce(ctx context.Context, link func(context.Context) (string, error),
	part, metaPath string, meta *partMeta, progress func(done, total int64)) error {

	u, err := link(ctx)
	if err != nil {
		return err
	}
	off := fileSize(part)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if off > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", off))
		if meta.ETag != "" {
			req.Header.Set("If-Range", meta.ETag)
		}
	}
	resp, err := c.transfer.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var total int64
	flags := os.O_WRONLY | os.O_CREATE
	switch resp.StatusCode {
	case http.StatusPartialContent:
		start, size, ok := parseContentRange(resp.Header.Get("Content-Range"))
		if !ok || start != off {
			_ = os.Remove(part)
			return errRestart
		}
		total = size
		flags |= os.O_APPEND
	case http.StatusOK:
		// Fichier entier : pas de reprise (Range ignoré, ou fichier modifié).
		off = 0
		total = resp.ContentLength
		flags |= os.O_TRUNC
		meta.ETag = ""
		if et := resp.Header.Get("ETag"); et != "" && !strings.HasPrefix(et, "W/") {
			meta.ETag = et
		}
	case http.StatusRequestedRangeNotSatisfiable:
		// Si le partiel a déjà la taille complète, il n'y a plus rien à lire.
		if _, size, ok := parseContentRange(resp.Header.Get("Content-Range")); ok && size == off && off > 0 {
			return nil
		}
		_ = os.Remove(part)
		return errRestart
	default:
		return &statusError{code: resp.StatusCode, msg: i18n.Tf("téléchargement refusé : %s", resp.Status)}
	}

	if b, err := json.Marshal(meta); err == nil {
		_ = os.WriteFile(metaPath, b, 0o644)
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	if progress != nil {
		progress(off, total)
	}
	pw := &progressWriter{w: f, done: off, total: total, progress: progress}
	_, err = io.Copy(pw, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && total > 0 && pw.done != total {
		err = io.ErrUnexpectedEOF
	}
	return err
}

// parseContentRange lit « bytes 100-199/1000 » (ou « bytes */1000 » : start
// vaut alors -1).
func parseContentRange(h string) (start, total int64, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(h), "bytes ")
	if !found {
		return 0, 0, false
	}
	rng, size, found := strings.Cut(rest, "/")
	if !found {
		return 0, 0, false
	}
	total, err := strconv.ParseInt(strings.TrimSpace(size), 10, 64)
	if err != nil {
		total = -1 // taille inconnue (« * »)
	}
	if strings.TrimSpace(rng) == "*" {
		return -1, total, total >= 0
	}
	from, _, found := strings.Cut(rng, "-")
	if !found {
		return 0, 0, false
	}
	start, err = strconv.ParseInt(strings.TrimSpace(from), 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return start, total, true
}
