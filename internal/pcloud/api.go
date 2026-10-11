package pcloud

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Catégories de fichiers renvoyées par pCloud.
const (
	CategoryImage    = 1
	CategoryVideo    = 2
	CategoryAudio    = 3
	CategoryDocument = 4
	CategoryArchive  = 5
)

// Item est un fichier ou un dossier.
type Item struct {
	Name        string `json:"name"`
	IsFolder    bool   `json:"isfolder"`
	FolderID    uint64 `json:"folderid"`
	FileID      uint64 `json:"fileid"`
	Size        int64  `json:"size"`
	Modified    string `json:"modified"`
	Category    int    `json:"category"`
	Thumb       bool   `json:"thumb"`
	ContentType string `json:"contenttype"`
}

// ID retourne l'identifiant du dossier ou du fichier.
func (it Item) ID() uint64 {
	if it.IsFolder {
		return it.FolderID
	}
	return it.FileID
}

// ModTime analyse la date de modification (RFC 1123) ; zéro si illisible.
func (it Item) ModTime() time.Time {
	t, err := time.Parse(time.RFC1123Z, it.Modified)
	if err != nil {
		return time.Time{}
	}
	return t
}

// UserInfo résume le compte.
type UserInfo struct {
	Email     string `json:"email"`
	Quota     int64  `json:"quota"`
	UsedQuota int64  `json:"usedquota"`
}

// UserInfo retourne l'e-mail et le quota du compte.
func (c *Client) UserInfo(ctx context.Context) (UserInfo, error) {
	var out UserInfo
	err := c.call(ctx, "userinfo", nil, &out)
	return out, err
}

// ListFolder liste le contenu d'un dossier (0 = racine).
func (c *Client) ListFolder(ctx context.Context, folderID uint64) ([]Item, error) {
	var out struct {
		Metadata struct {
			Contents []Item `json:"contents"`
		} `json:"metadata"`
	}
	err := c.call(ctx, "listfolder", url.Values{
		"folderid": {fmt.Sprint(folderID)},
	}, &out)
	if err != nil {
		return nil, err
	}
	return out.Metadata.Contents, nil
}

// CreateFolder crée un dossier.
func (c *Client) CreateFolder(ctx context.Context, parent uint64, name string) error {
	return c.call(ctx, "createfolder", url.Values{
		"folderid": {fmt.Sprint(parent)},
		"name":     {name},
	}, nil)
}

// EnsureFolder crée un dossier s'il n'existe pas et retourne son identifiant.
func (c *Client) EnsureFolder(ctx context.Context, parent uint64, name string) (uint64, error) {
	var out struct {
		Metadata struct {
			FolderID uint64 `json:"folderid"`
		} `json:"metadata"`
	}
	err := c.call(ctx, "createfolderifnotexists", url.Values{
		"folderid": {fmt.Sprint(parent)},
		"name":     {name},
	}, &out)
	return out.Metadata.FolderID, err
}

// Rename renomme un fichier ou un dossier.
func (c *Client) Rename(ctx context.Context, it Item, newName string) error {
	method, key := "renamefile", "fileid"
	if it.IsFolder {
		method, key = "renamefolder", "folderid"
	}
	return c.call(ctx, method, url.Values{
		key:      {fmt.Sprint(it.ID())},
		"toname": {newName},
	}, nil)
}

// Move déplace un fichier ou un dossier dans un autre dossier.
func (c *Client) Move(ctx context.Context, it Item, toFolder uint64) error {
	method, key := "renamefile", "fileid"
	if it.IsFolder {
		method, key = "renamefolder", "folderid"
	}
	return c.call(ctx, method, url.Values{
		key:          {fmt.Sprint(it.ID())},
		"tofolderid": {fmt.Sprint(toFolder)},
	}, nil)
}

// Delete supprime un fichier, ou un dossier et tout son contenu.
func (c *Client) Delete(ctx context.Context, it Item) error {
	if it.IsFolder {
		return c.call(ctx, "deletefolderrecursive", url.Values{
			"folderid": {fmt.Sprint(it.FolderID)},
		}, nil)
	}
	return c.call(ctx, "deletefile", url.Values{
		"fileid": {fmt.Sprint(it.FileID)},
	}, nil)
}

// PublicLink crée (ou retrouve) un lien public vers un fichier ou un dossier.
func (c *Client) PublicLink(ctx context.Context, it Item) (string, error) {
	method, key := "getfilepublink", "fileid"
	if it.IsFolder {
		method, key = "getfolderpublink", "folderid"
	}
	var out struct {
		Link string `json:"link"`
	}
	err := c.call(ctx, method, url.Values{key: {fmt.Sprint(it.ID())}}, &out)
	if err == nil && out.Link == "" {
		err = fmt.Errorf("lien absent de la réponse pCloud")
	}
	return out.Link, err
}

// contentURL construit l'URL d'un contenu à partir d'une réponse hosts/path.
func (c *Client) contentURL(hosts []string, path string) (string, error) {
	if len(hosts) == 0 || path == "" {
		return "", fmt.Errorf("lien de contenu absent de la réponse pCloud")
	}
	scheme := "https"
	if strings.HasPrefix(c.base, "http://") {
		scheme = "http"
	}
	return scheme + "://" + hosts[0] + path, nil
}

// FileURL retourne une URL de téléchargement temporaire.
func (c *Client) FileURL(ctx context.Context, fileID uint64) (string, error) {
	var out struct {
		Hosts []string `json:"hosts"`
		Path  string   `json:"path"`
	}
	if err := c.call(ctx, "getfilelink", url.Values{"fileid": {fmt.Sprint(fileID)}}, &out); err != nil {
		return "", err
	}
	return c.contentURL(out.Hosts, out.Path)
}

// progressWriter appelle progress à chaque écriture.
type progressWriter struct {
	w        io.Writer
	done     int64
	total    int64
	progress func(done, total int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.done += int64(n)
	if p.progress != nil {
		p.progress(p.done, p.total)
	}
	return n, err
}

// Download écrit le contenu d'un fichier dans w.
func (c *Client) Download(ctx context.Context, fileID uint64, w io.Writer, progress func(done, total int64)) error {
	u, err := c.FileURL(ctx, fileID)
	if err != nil {
		return err
	}
	return c.DownloadURL(ctx, u, w, progress)
}

// DownloadURL écrit dans w le contenu servi par une URL de contenu pCloud
// (fichier, variante vidéo…). progress reçoit total <= 0 si la taille est inconnue.
func (c *Client) DownloadURL(ctx context.Context, u string, w io.Writer, progress func(done, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.transfer.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("téléchargement refusé : %s", resp.Status)
	}
	_, err = io.Copy(&progressWriter{w: w, total: resp.ContentLength, progress: progress}, resp.Body)
	return err
}

// Thumbnail retourne la miniature (JPEG/PNG) d'un fichier image ou vidéo.
func (c *Client) Thumbnail(ctx context.Context, fileID uint64, size string) ([]byte, error) {
	var out struct {
		Hosts []string `json:"hosts"`
		Path  string   `json:"path"`
	}
	err := c.call(ctx, "getthumblink", url.Values{
		"fileid": {fmt.Sprint(fileID)},
		"size":   {size},
		"crop":   {"1"},
	}, &out)
	if err != nil {
		return nil, err
	}
	u, err := c.contentURL(out.Hosts, out.Path)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.transfer.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("miniature refusée : %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// readerProgress compte les octets lus.
type readerProgress struct {
	r        io.Reader
	done     int64
	total    int64
	progress func(done, total int64)
}

func (p *readerProgress) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if n > 0 && p.progress != nil {
		p.progress(p.done, p.total)
	}
	return n, err
}

// Upload envoie un fichier (size octets lus dans r) dans un dossier. mtime, si
// non nul, devient la date de modification du fichier sur pCloud.
func (c *Client) Upload(ctx context.Context, folderID uint64, name string, r io.Reader, size int64, mtime time.Time, progress func(done, total int64)) error {
	params := c.withAuth(url.Values{
		"folderid":  {fmt.Sprint(folderID)},
		"nopartial": {"1"},
	})
	if !mtime.IsZero() {
		params.Set("mtime", fmt.Sprint(mtime.Unix()))
	}

	// Préambule et pied du multipart construits à la main pour connaître la
	// longueur du corps (pas d'envoi "chunked") tout en streamant le fichier.
	var pre bytes.Buffer
	mw := multipart.NewWriter(&pre)
	for k, v := range params {
		if err := mw.WriteField(k, v[0]); err != nil {
			return err
		}
	}
	if _, err := mw.CreateFormFile("file", name); err != nil {
		return err
	}
	footer := "\r\n--" + mw.Boundary() + "--\r\n"

	body := io.MultiReader(&pre, &readerProgress{r: r, total: size, progress: progress}, strings.NewReader(footer))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/uploadfile", body)
	if err != nil {
		return err
	}
	req.ContentLength = int64(pre.Len()) + size + int64(len(footer))
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.transfer.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeAPI(resp.Body, nil)
}
