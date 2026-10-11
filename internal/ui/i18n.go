package ui

import (
	"fyne.io/fyne/v2"

	"pcloud-desktop/internal/i18n"
)

// tr et trf traduisent un texte source (en français) dans la langue active.
var (
	tr  = i18n.T
	trf = i18n.Tf
)

// prefLang est la clé de préférence de la langue choisie.
const prefLang = "lang"

// InitLanguage applique la langue enregistrée dans les préférences, ou celle
// du système à défaut.
func InitLanguage(a fyne.App) {
	code := i18n.Normalize(a.Preferences().String(prefLang))
	if code == "" {
		code = i18n.Detect()
	}
	i18n.Set(code)
}

// languageMenu retourne les entrées d'un menu de choix de la langue ; onChange
// est appelé après le changement, pour reconstruire l'écran courant.
func languageMenu(a fyne.App, onChange func()) []*fyne.MenuItem {
	var items []*fyne.MenuItem
	for _, l := range i18n.Languages() {
		l := l
		item := fyne.NewMenuItem(l.Name, func() {
			if i18n.Current() == l.Code {
				return
			}
			i18n.Set(l.Code)
			a.Preferences().SetString(prefLang, l.Code)
			onChange()
		})
		item.Checked = i18n.Current() == l.Code
		items = append(items, item)
	}
	return items
}
