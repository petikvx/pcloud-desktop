package ui

import (
	"context"
	"fmt"
	"os"

	"fyne.io/fyne/v2"

	"pcloud-desktop/internal/pcloud"
)

// resumeTag identifie une version d'un fichier pCloud : un téléchargement
// partiel n'est repris que si le fichier distant n'a pas changé.
func resumeTag(it pcloud.Item) string {
	return fmt.Sprintf("%d:%d:%s", it.FileID, it.Size, it.Modified)
}

// saveInto télécharge l'URL fournie par link vers la destination choisie dans
// une boîte « Enregistrer sous ».
//
// Pour un fichier local, le contenu arrive dans « destination.part » et le
// téléchargement est repris à l'octet près : automatiquement après une coupure,
// ou en relançant vers la même destination après un échec ou une annulation.
// Pour toute autre destination (pas de chemin local), l'écriture se fait en un
// seul passage, sans reprise.
func (b *browser) saveInto(ctx context.Context, wc fyne.URIWriteCloser, link func(context.Context) (string, error),
	tag, label string, report func(string, float64)) error {

	meter := newSpeedMeter()
	progress := func(done, total int64) {
		meter.Set(done)
		report(progressText(label, done, total, meter.Rate()), fraction(done, total))
	}

	u := wc.URI()
	if u == nil || u.Scheme() != "file" {
		defer wc.Close()
		url, err := link(ctx)
		if err != nil {
			return err
		}
		return b.c.DownloadURL(ctx, url, wc, progress)
	}

	// La boîte de dialogue a déjà créé (et vidé) le fichier : on ne s'en sert
	// que pour son chemin, le contenu est écrit puis renommé par le client.
	dest := u.Path()
	_ = wc.Close()
	err := b.c.DownloadURLTo(ctx, link, dest, tag, progress)
	if err != nil {
		// Ne pas laisser un fichier vide à la place du téléchargement échoué ;
		// le partiel (dest.part) reste pour la reprise.
		if fi, serr := os.Stat(dest); serr == nil && fi.Size() == 0 {
			_ = os.Remove(dest)
		}
		return fmt.Errorf("%w\n\n%s", err, tr("Le téléchargement partiel est conservé : relancez-le vers le même fichier pour le reprendre."))
	}
	return nil
}
