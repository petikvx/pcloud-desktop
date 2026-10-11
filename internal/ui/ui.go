// Package ui contient les écrans Fyne : connexion puis navigateur de fichiers.
package ui

import (
	"context"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/zalando/go-keyring"

	"pcloud-desktop/internal/i18n"
	"pcloud-desktop/internal/pcloud"
)

// Start affiche l'écran de connexion, ou ouvre directement le navigateur si
// un jeton valide est stocké dans le trousseau système.
func Start(a fyne.App, w fyne.Window, c *pcloud.Client) {
	if value, ok := loadSession(); ok {
		w.SetContent(container.NewCenter(widget.NewLabel(tr("Reconnexion…"))))
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			err := c.RestoreSession(ctx, value)
			if err != nil {
				_ = keyring.Delete(keyringService, keyringUser)
			}
			fyne.Do(func() {
				if err == nil {
					showBrowser(a, w, c)
				} else {
					showLogin(a, w, c, "")
				}
			})
		}()
		return
	}
	showLogin(a, w, c, "")
}

// showLogin affiche l'écran de connexion ; notice est un message d'information
// (par exemple « session expirée »).
func showLogin(a fyne.App, w fyne.Window, c *pcloud.Client, notice string) {
	status := widget.NewLabel(notice)
	status.Wrapping = fyne.TextWrapWord
	status.Alignment = fyne.TextAlignCenter
	setStatus := func(msg string, isError bool) {
		status.SetText(msg)
		if isError {
			status.Importance = widget.DangerImportance
		} else {
			status.Importance = widget.MediumImportance
		}
		status.Refresh()
	}
	bar := widget.NewProgressBarInfinite()
	bar.Hide()

	user := widget.NewEntry()
	user.SetPlaceHolder(tr("Adresse e-mail"))
	pass := widget.NewPasswordEntry()
	pass.SetPlaceHolder(tr("Mot de passe"))
	key := widget.NewPasswordEntry()
	key.SetPlaceHolder(tr("Clé API (jeton auth)"))

	var buttons []*widget.Button
	// run exécute une connexion hors du thread d'interface.
	run := func(timeout time.Duration, missing string, empty bool, do func(context.Context) error) {
		if empty {
			setStatus(missing, true)
			return
		}
		for _, b := range buttons {
			b.Disable()
		}
		bar.Show()
		setStatus("", false)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			err := do(ctx)
			if err == nil {
				saveSession(c.SessionValue())
			}
			fyne.Do(func() {
				bar.Hide()
				for _, b := range buttons {
					b.Enable()
				}
				if err != nil {
					setStatus(trf("Échec de connexion : %s", err), true)
					return
				}
				showBrowser(a, w, c)
			})
		}()
	}

	loginPass := func() {
		run(30*time.Second, tr("Renseignez l'e-mail et le mot de passe."), user.Text == "" || pass.Text == "",
			func(ctx context.Context) error { return c.Login(ctx, user.Text, pass.Text) })
	}
	loginKey := func() {
		token := strings.TrimSpace(key.Text)
		run(30*time.Second, tr("Renseignez la clé API."), token == "",
			func(ctx context.Context) error { return c.UseToken(ctx, token) })
	}
	btnPass := widget.NewButton(tr("Se connecter"), loginPass)
	btnKey := widget.NewButton(tr("Se connecter avec la clé"), loginKey)
	buttons = []*widget.Button{btnPass, btnKey}
	pass.OnSubmitted = func(string) { loginPass() }
	user.OnSubmitted = func(string) { loginPass() }
	key.OnSubmitted = func(string) { loginKey() }

	tabs := container.NewAppTabs(
		container.NewTabItem(tr("E-mail"), container.NewVBox(user, pass, btnPass)),
		container.NewTabItem(tr("Clé API"), container.NewVBox(key, btnKey)),
	)

	title := canvas.NewText("pCloud", pcloudBlue)
	title.TextSize = 36
	title.TextStyle = fyne.TextStyle{Bold: true}
	title.Alignment = fyne.TextAlignCenter
	subtitle := widget.NewLabelWithStyle(trf("Client de bureau · eapi.pcloud.com · %s", VersionLabel()), fyne.TextAlignCenter, fyne.TextStyle{})

	// Choix de la langue : le nom de chaque langue est écrit dans cette langue.
	var langBtn *widget.Button
	langBtn = widget.NewButtonWithIcon(strings.ToUpper(i18n.Current()), theme.ComputerIcon(), func() {
		menu := fyne.NewMenu("", languageMenu(a, func() { showLogin(a, w, c, "") })...)
		pos := a.Driver().AbsolutePositionForObject(langBtn)
		widget.ShowPopUpMenuAtPosition(menu, w.Canvas(), pos.Add(fyne.NewPos(0, langBtn.Size().Height)))
	})
	langBtn.Importance = widget.LowImportance

	form := container.NewVBox(title, subtitle, tabs, bar, status, container.NewCenter(langBtn))
	w.SetContent(container.NewCenter(container.NewGridWrap(fyne.NewSize(400, 340), form)))
}
