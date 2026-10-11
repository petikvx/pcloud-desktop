package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// pcloudBlue est la couleur d'accent de l'application.
var pcloudBlue = color.NRGBA{R: 0x17, G: 0xBE, B: 0xD0, A: 0xFF}

// appTheme reprend le thème par défaut (clair ou sombre selon le système) en
// remplaçant la couleur d'accent.
type appTheme struct{ fyne.Theme }

// NewTheme retourne le thème de l'application.
func NewTheme() fyne.Theme { return appTheme{theme.DefaultTheme()} }

func (t appTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNamePrimary, theme.ColorNameHyperlink:
		return pcloudBlue
	case theme.ColorNameSelection:
		return color.NRGBA{R: pcloudBlue.R, G: pcloudBlue.G, B: pcloudBlue.B, A: 0x40}
	}
	return t.Theme.Color(name, variant)
}
