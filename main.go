package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"pcloud-desktop/internal/pcloud"
	"pcloud-desktop/internal/ui"
)

func main() {
	a := app.NewWithID("fr.krmr.pcloud-desktop")
	a.Settings().SetTheme(ui.NewTheme())

	w := a.NewWindow("pCloud Desktop")
	w.Resize(fyne.NewSize(900, 600))

	ui.Start(a, w, pcloud.New())

	w.ShowAndRun()
}
