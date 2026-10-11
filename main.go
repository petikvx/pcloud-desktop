package main

import (
	_ "embed"
	"fmt"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"pcloud-desktop/internal/pcloud"
	"pcloud-desktop/internal/ui"
)

// version est fixée à la compilation : -ldflags "-X main.version=0.1.0".
var version = "dev"

//go:embed packaging/icon.png
var iconPNG []byte

func main() {
	for _, arg := range os.Args[1:] {
		if arg == "--version" || arg == "-version" {
			fmt.Println("pcloud-desktop", version)
			return
		}
	}

	a := app.NewWithID("fr.krmr.pcloud-desktop")
	a.SetIcon(fyne.NewStaticResource("icon.png", iconPNG))
	a.Settings().SetTheme(ui.NewTheme())

	w := a.NewWindow("pCloud Desktop")
	w.Resize(fyne.NewSize(900, 600))

	ui.Start(a, w, pcloud.New())

	w.ShowAndRun()
}
