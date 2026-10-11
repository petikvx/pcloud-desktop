package pcloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"pcloud-desktop/internal/i18n"
)

// defaultChunkSize : taille d'un morceau d'un envoi reprenable. Un fichier qui
// tient dans un morceau part en une seule requête.
const defaultChunkSize = 8 << 20

// ResumeStore mémorise, pour un fichier local, l'identifiant d'envoi ouvert
// côté pCloud (upload_create) afin de reprendre un envoi interrompu, même
// après un redémarrage de l'application. Sa valeur zéro (0) signifie « aucun ».
type ResumeStore interface {
	Get() uint64
	Put(id uint64)
	Clear()
}

// UploadFile envoie f (size octets) dans un dossier. Un petit fichier part en
// une requête (Upload), réessayée après une coupure. Un gros fichier est
// envoyé par morceaux dans un envoi pCloud (upload_create / upload_write /
// upload_save) : après une coupure, l'envoi reprend à l'octet que le serveur a
// reçu, et store (facultatif) permet de le reprendre aussi après un arrêt.
func (c *Client) UploadFile(ctx context.Context, folderID uint64, name string, f io.ReadSeeker, size int64,
	mtime time.Time, store ResumeStore, progress func(done, total int64)) error {

	if size <= c.chunkSize {
		return c.retry(ctx, func() (bool, error) {
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return false, err
			}
			return false, c.Upload(ctx, folderID, name, f, size, mtime, progress)
		})
	}
	return c.uploadChunked(ctx, folderID, name, f, size, mtime, store, progress)
}

// errStaleUpload : l'envoi mémorisé n'existe plus côté serveur.
var errStaleUpload = errors.New("envoi inconnu du serveur")

func (c *Client) uploadChunked(ctx context.Context, folderID uint64, name string, f io.ReadSeeker, size int64,
	mtime time.Time, store ResumeStore, progress func(done, total int64)) error {

	var id uint64
	if store != nil {
		id = store.Get()
	}
	clear := func() {
		if store != nil {
			store.Clear()
		}
	}

	// Une fois : si l'envoi mémorisé a disparu du serveur, on repart de zéro.
	for fresh := false; ; fresh = true {
		var off int64
		if id != 0 {
			var err error
			if off, err = c.uploadOffset(ctx, id); err != nil {
				if !errors.Is(err, errStaleUpload) {
					return err
				}
				clear()
				id, off = 0, 0
			} else if off > size {
				clear()
				id, off = 0, 0
			}
		}
		if id == 0 {
			var out struct {
				UploadID uint64 `json:"uploadid"`
			}
			err := c.retry(ctx, func() (bool, error) {
				return false, c.call(ctx, "upload_create", nil, &out)
			})
			if err != nil {
				return err
			}
			id = out.UploadID
			if store != nil {
				store.Put(id)
			}
		}

		err := c.writeChunks(ctx, id, f, off, size, progress)
		if errors.Is(err, errStaleUpload) && !fresh {
			clear()
			id = 0
			continue
		}
		if errors.Is(err, errStaleUpload) {
			err = errors.New(i18n.T("envoi inconnu du serveur"))
		}
		if err != nil {
			return err // l'envoi reste mémorisé : il sera repris
		}
		break
	}

	err := c.retry(ctx, func() (bool, error) {
		params := url.Values{
			"uploadid": {fmt.Sprint(id)},
			"folderid": {fmt.Sprint(folderID)},
			"name":     {name},
		}
		if !mtime.IsZero() {
			params.Set("mtime", fmt.Sprint(mtime.Unix()))
		}
		return false, c.call(ctx, "upload_save", params, nil)
	})
	if err != nil {
		return err
	}
	clear()
	return nil
}

// uploadOffset retourne le nombre d'octets déjà reçus pour un envoi. Un envoi
// inconnu (expiré) donne errStaleUpload.
func (c *Client) uploadOffset(ctx context.Context, id uint64) (int64, error) {
	var out struct {
		Size int64 `json:"size"`
	}
	err := c.retry(ctx, func() (bool, error) {
		return false, c.call(ctx, "upload_info", url.Values{"uploadid": {fmt.Sprint(id)}}, &out)
	})
	var apiErr *APIError
	if errors.As(err, &apiErr) && !IsAuthError(err) {
		return 0, errStaleUpload
	}
	return out.Size, err
}

// writeChunks envoie f de off à size, morceau par morceau. Après un échec
// transitoire, le décalage est relu auprès du serveur avant de continuer.
func (c *Client) writeChunks(ctx context.Context, id uint64, f io.ReadSeeker, off, size int64,
	progress func(done, total int64)) error {

	if progress != nil {
		progress(off, size)
	}
	fails := 0
	for off < size {
		n := min(c.chunkSize, size-off)
		start := off
		err := c.writeChunk(ctx, id, f, start, n, func(done int64) {
			if progress != nil {
				progress(start+done, size)
			}
		})
		if err == nil {
			off += n
			fails = 0
			continue
		}
		if ctx.Err() != nil || !isTransient(err) || fails >= c.retries {
			return err
		}
		if berr := c.backoff(ctx, fails); berr != nil {
			return err
		}
		fails++
		// Une partie du morceau a pu être reçue : on se recale sur le serveur.
		got, ierr := c.uploadOffset(ctx, id)
		if ierr != nil {
			return ierr
		}
		if got > size {
			return errStaleUpload
		}
		off = got
	}
	return nil
}

// writeChunk envoie n octets de f, lus à partir de off, avec upload_write. Les
// paramètres sont dans l'URL (le corps est le contenu brut du morceau).
func (c *Client) writeChunk(ctx context.Context, id uint64, f io.ReadSeeker, off, n int64, onBytes func(done int64)) error {
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return err
	}
	q := c.withAuth(url.Values{
		"uploadid":     {fmt.Sprint(id)},
		"uploadoffset": {fmt.Sprint(off)},
	})
	body := &readerProgress{r: io.LimitReader(f, n), total: n, progress: func(done, _ int64) { onBytes(done) }}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/upload_write?"+q.Encode(), body)
	if err != nil {
		return err
	}
	req.ContentLength = n
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.transfer.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeAPI(resp.Body, nil)
}
