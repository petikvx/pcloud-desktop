// Package ui contient les écrans Fyne : connexion puis navigateur de fichiers.
package ui

import (
	"context"
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/zalando/go-keyring"

	"pcloud-desktop/internal/pcloud"
)

const (
	keyringService = "pcloud-desktop"
	keyringUser    = "session"
)

// Start affiche l'écran de connexion, ou ouvre directement le navigateur si
// un jeton valide est stocké dans le trousseau système.
func Start(a fyne.App, w fyne.Window, c *pcloud.Client) {
	if host, token, ok := loadSession(); ok {
		w.SetContent(widget.NewLabel("Reconnexion…"))
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := c.Restore(ctx, host, token); err == nil {
				showBrowser(w, c)
				return
			}
			_ = keyring.Delete(keyringService, keyringUser)
			showLogin(w, c)
		}()
		return
	}
	showLogin(w, c)
}

func showLogin(w fyne.Window, c *pcloud.Client) {
	user := widget.NewEntry()
	user.SetPlaceHolder("Adresse e-mail")
	pass := widget.NewPasswordEntry()
	pass.SetPlaceHolder("Mot de passe")
	status := widget.NewLabel("")
	bar := widget.NewProgressBarInfinite()
	bar.Hide()

	var btn *widget.Button
	submit := func() {
		if user.Text == "" || pass.Text == "" {
			status.SetText("Renseignez l'e-mail et le mot de passe.")
			return
		}
		btn.Disable()
		bar.Show()
		status.SetText("")
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err := c.Login(ctx, user.Text, pass.Text)
			bar.Hide()
			btn.Enable()
			if err != nil {
				status.SetText("Échec de connexion : " + err.Error())
				return
			}
			saveSession(c.Host(), c.Auth())
			showBrowser(w, c)
		}()
	}
	btn = widget.NewButton("Se connecter", submit)
	pass.OnSubmitted = func(string) { submit() }

	form := container.NewVBox(
		widget.NewLabelWithStyle("pCloud", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		user, pass, btn, bar, status,
	)
	w.SetContent(container.NewCenter(container.NewGridWrap(fyne.NewSize(360, 260), form)))
}

// pathEntry mémorise un dossier visité pour pouvoir remonter.
type pathEntry struct {
	id   uint64
	name string
}

func showBrowser(w fyne.Window, c *pcloud.Client) {
	var (
		stack = []pathEntry{{0, "/"}}
		items []pcloud.Item
	)

	title := widget.NewLabel("/")
	status := widget.NewLabel("")

	list := widget.NewList(
		func() int { return len(items) + 1 }, // +1 pour la ligne « .. »
		func() fyne.CanvasObject {
			return container.NewBorder(nil, nil, widget.NewIcon(nil), widget.NewLabel(""), widget.NewLabel(""))
		},
		nil,
	)

	var load func()
	load = func() {
		cur := stack[len(stack)-1]
		title.SetText(pathString(stack))
		status.SetText("Chargement…")
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			res, err := c.ListFolder(ctx, cur.id)
			if err != nil {
				status.SetText("")
				dialog.ShowError(err, w)
				return
			}
			items = res
			status.SetText(fmt.Sprintf("%d élément(s)", len(res)))
			list.UnselectAll()
			list.Refresh()
		}()
	}

	list.UpdateItem = func(i widget.ListItemID, o fyne.CanvasObject) {
		row := o.(*fyne.Container)
		name := row.Objects[0].(*widget.Label)
		icon := row.Objects[1].(*widget.Icon)
		size := row.Objects[2].(*widget.Label)
		if i == 0 {
			name.SetText("..")
			icon.SetResource(iconFolder())
			size.SetText("")
			return
		}
		it := items[i-1]
		name.SetText(it.Name)
		if it.IsFolder {
			icon.SetResource(iconFolder())
			size.SetText("")
		} else {
			icon.SetResource(iconFile())
			size.SetText(humanSize(it.Size))
		}
	}

	list.OnSelected = func(i widget.ListItemID) {
		if i == 0 {
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
				load()
			}
			list.UnselectAll()
			return
		}
		it := items[i-1]
		if it.IsFolder {
			stack = append(stack, pathEntry{it.FolderID, it.Name})
			load()
			return
		}
		list.UnselectAll()
		// TODO : proposer le téléchargement (méthode getfilelink).
	}

	logout := widget.NewButton("Déconnexion", func() {
		_ = keyring.Delete(keyringService, keyringUser)
		showLogin(w, pcloud.New())
	})

	top := container.NewBorder(nil, nil, nil, logout, title)
	w.SetContent(container.NewBorder(top, status, nil, nil, list))
	load()
}
