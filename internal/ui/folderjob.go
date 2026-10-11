package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"pcloud-desktop/internal/hls"
	"pcloud-desktop/internal/pcloud"
	"pcloud-desktop/internal/queue"
)

// entry est un fichier trouvé dans un dossier pCloud, avec son sous-dossier
// relatif à la racine explorée (séparateur « / », vide pour la racine).
type entry struct {
	Item   pcloud.Item
	RelDir string
}

// lister est la part de pcloud.Client utilisée pour explorer un dossier.
type lister interface {
	ListFolder(ctx context.Context, folderID uint64) ([]pcloud.Item, error)
}

const maxScanDepth = 20

// localName rend un nom pCloud utilisable comme nom de fichier local.
func localName(name string) string {
	n := strings.NewReplacer("/", "_", "\\", "_", "\x00", "_").Replace(name)
	n = strings.TrimSpace(n)
	if n == "" || n == "." || n == ".." {
		return "_"
	}
	return n
}

// scanFolder liste récursivement les fichiers d'un dossier, dans un ordre
// stable (sous-dossiers et fichiers triés par nom).
func scanFolder(ctx context.Context, l lister, folderID uint64) ([]entry, error) {
	var out []entry
	var walk func(id uint64, rel string, depth int) error
	walk = func(id uint64, rel string, depth int) error {
		if depth > maxScanDepth {
			return fmt.Errorf(tr("arborescence trop profonde (plus de %d niveaux)"), maxScanDepth)
		}
		items, err := l.ListFolder(ctx, id)
		if err != nil {
			return err
		}
		sortItems(items, sortName, true)
		for _, it := range items {
			if it.IsFolder {
				if err := walk(it.FolderID, path.Join(rel, localName(it.Name)), depth+1); err != nil {
					return err
				}
				continue
			}
			out = append(out, entry{Item: it, RelDir: rel})
		}
		return nil
	}
	if err := walk(folderID, "", 0); err != nil {
		return nil, err
	}
	return out, nil
}

func isVideo(it pcloud.Item) bool { return it.Category == pcloud.CategoryVideo }

// buildTasks retient les vidéos, et les autres fichiers si others est vrai.
func buildTasks(entries []entry, others bool) (tasks []queue.Task, kept []entry) {
	for _, e := range entries {
		if !isVideo(e.Item) && !others {
			continue
		}
		kept = append(kept, e)
		tasks = append(tasks, queue.Task{Name: path.Join(e.RelDir, e.Item.Name)})
	}
	return tasks, kept
}

// folderJob télécharge les fichiers d'un dossier vers un dossier local, en
// appliquant les mêmes préférences de qualité et de langue à chaque vidéo.
type folderJob struct {
	c      *pcloud.Client
	dl     *hls.Downloader
	ffmpeg string // non vide : un MP4 unique par vidéo
	prefs  hls.Preferences
	root   string // dossier local de destination
}

// Run traite un fichier. Un fichier déjà présent donne queue.ErrSkipped, ce
// qui permet de relancer un lot interrompu sans tout retélécharger.
func (j *folderJob) Run(ctx context.Context, e entry, report func(string, float64)) error {
	dir := filepath.Join(j.root, filepath.FromSlash(e.RelDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if isVideo(e.Item) {
		return j.video(ctx, dir, e.Item, report)
	}
	return j.plain(ctx, dir, e.Item, report)
}

// finalize remplace dest par part (écriture atomique : pas de fichier partiel).
func finalize(part, dest string) error {
	if err := os.Rename(part, dest); err != nil {
		_ = os.Remove(part)
		return err
	}
	return nil
}

func (j *folderJob) plain(ctx context.Context, dir string, it pcloud.Item, report func(string, float64)) error {
	dest := filepath.Join(dir, localName(it.Name))
	if fi, err := os.Stat(dest); err == nil && fi.Size() == it.Size {
		return queue.ErrSkipped
	}
	// Le fichier partiel (dest.part) est conservé après un échec ou une
	// annulation : relancer le lot reprend à l'octet où il s'est arrêté.
	meter := newSpeedMeter()
	return j.c.DownloadFileTo(ctx, it.FileID, dest, resumeTag(it), func(done, total int64) {
		meter.Set(done)
		report(progressText(it.Name, done, total, meter.Rate()), fraction(done, total))
	})
}

func (j *folderJob) video(ctx context.Context, dir string, it pcloud.Item, report func(string, float64)) error {
	base := localName(outputBaseName("", it.Name))
	for _, ext := range []string{".mp4", ".ts"} {
		if fi, err := os.Stat(filepath.Join(dir, base+ext)); err == nil && fi.Size() > 0 {
			return queue.ErrSkipped
		}
	}

	report(it.Name+tr(" · recherche du flux…"), 0)
	link, isHLS, err := j.c.MediaTranscodeLink(ctx, it.FileID)
	if err != nil {
		return fmt.Errorf(tr("lien de streaming : %w"), err)
	}
	if !isHLS {
		// Pas de playlist : on prend le fichier tel quel.
		return j.plain(ctx, dir, it, report)
	}
	master, media, err := j.dl.FetchPlaylist(ctx, link)
	if err != nil {
		return fmt.Errorf(tr("playlist : %w"), err)
	}

	var video *hls.Media
	var audio, subs []hls.Rendition
	if master != nil {
		sel, err := master.Select(j.prefs)
		if err != nil {
			return err
		}
		nested, md, err := j.dl.FetchPlaylist(ctx, sel.Variant.URI)
		if err != nil {
			return fmt.Errorf(tr("playlist vidéo : %w"), err)
		}
		if nested != nil || md == nil {
			return errors.New(tr("playlist vidéo inattendue (master au lieu d'une playlist média)"))
		}
		video, audio, subs = md, sel.Audio, sel.Subs
	} else {
		video = media
	}

	noop := func([]string) {}
	if j.ffmpeg != "" {
		dest := filepath.Join(dir, base+".mp4")
		part := filepath.Join(dir, "."+base+".partial.mp4")
		if err := runHLSMerged(ctx, j.dl, j.ffmpeg, part, video, audio, subs, report, noop); err != nil {
			_ = os.Remove(part)
			return err
		}
		return finalize(part, dest)
	}

	dest := filepath.Join(dir, base+hlsExt(video, false))
	part := dest + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	err = runHLS(ctx, j.dl, f, dest, video, audio, subs, report, noop)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(part)
		return err
	}
	return finalize(part, dest)
}
