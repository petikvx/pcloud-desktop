package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"

	"pcloud-desktop/internal/i18n"
	"pcloud-desktop/internal/pcloud"
)

type sortKey int

const (
	sortName sortKey = iota
	sortSize
	sortDate
)

// sortItems trie en place : les dossiers d'abord, puis selon la clé. L'ordre
// des noms ignore la casse et sert de départage.
func sortItems(items []pcloud.Item, key sortKey, asc bool) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.IsFolder != b.IsFolder {
			return a.IsFolder
		}
		an, bn := strings.ToLower(a.Name), strings.ToLower(b.Name)
		var less, equal bool
		switch {
		case key == sortSize && !a.IsFolder:
			less, equal = a.Size < b.Size, a.Size == b.Size
		case key == sortDate:
			at, bt := a.ModTime(), b.ModTime()
			less, equal = at.Before(bt), at.Equal(bt)
		default:
			less, equal = an < bn, an == bn
		}
		if equal {
			return an < bn
		}
		if asc {
			return less
		}
		return !less
	})
}

// filterItems retourne une copie des éléments dont le nom contient q (sans
// tenir compte de la casse). q vide : tout est conservé.
func filterItems(items []pcloud.Item, q string) []pcloud.Item {
	q = strings.ToLower(strings.TrimSpace(q))
	out := make([]pcloud.Item, 0, len(items))
	for _, it := range items {
		if q == "" || strings.Contains(strings.ToLower(it.Name), q) {
			out = append(out, it)
		}
	}
	return out
}

// summary décrit un dossier : « 3 éléments · 1.2 Mo ».
func summary(items []pcloud.Item) string {
	var total int64
	for _, it := range items {
		if !it.IsFolder {
			total += it.Size
		}
	}
	s := i18n.N(len(items), "%d élément", "%d éléments")
	if total > 0 {
		s += " · " + humanSize(total)
	}
	return s
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf(tr("%d o"), n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf(tr("%.1f %co"), float64(n)/float64(div), "KMGTPE"[exp])
}

func formatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format(tr("02/01/2006 15:04"))
}

// iconFor choisit l'icône d'un élément d'après son type.
func iconFor(it pcloud.Item) fyne.Resource {
	if it.IsFolder {
		return theme.FolderIcon()
	}
	switch it.Category {
	case pcloud.CategoryImage:
		return theme.FileImageIcon()
	case pcloud.CategoryVideo:
		return theme.FileVideoIcon()
	case pcloud.CategoryAudio:
		return theme.FileAudioIcon()
	case pcloud.CategoryDocument:
		return theme.FileTextIcon()
	case pcloud.CategoryArchive:
		return theme.FileApplicationIcon()
	}
	return theme.FileIcon()
}

// validName refuse les noms vides ou contenant un séparateur.
func validName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\")
}
