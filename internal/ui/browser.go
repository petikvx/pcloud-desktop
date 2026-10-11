package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/zalando/go-keyring"

	"pcloud-desktop/internal/pcloud"
)

// pathEntry mémorise un dossier visité pour pouvoir remonter.
type pathEntry struct {
	id   uint64
	name string
}

// browser est l'écran principal. Toutes ses méthodes s'exécutent dans le
// thread d'interface ; les goroutines y reviennent par fyne.Do.
type browser struct {
	a fyne.App
	w fyne.Window
	c *pcloud.Client

	stack   []pathEntry
	history [][]pathEntry
	items   []pcloud.Item // contenu du dossier courant
	view    []pcloud.Item // items filtrés et triés, affichés
	cache   map[uint64][]pcloud.Item
	gen     int // identifie le dernier chargement, pour ignorer les réponses périmées

	sortKey  sortKey
	sortAsc  bool
	filter   string
	selected int

	thumbs         *thumbCache
	refreshPending bool

	list       *widget.List
	crumbs     *fyne.Container
	search     *widget.Entry
	status     *widget.Label
	quotaLabel *widget.Label
	quotaBar   *widget.ProgressBar
	emptyBox   *fyne.Container
	emptyLabel *widget.Label
	retryBtn   *widget.Button
	backBtn    *widget.Button
	upBtn      *widget.Button
	sortBtns   map[sortKey]*widget.Button
	findKey    *desktop.CustomShortcut
}

func showBrowser(a fyne.App, w fyne.Window, c *pcloud.Client) {
	b := &browser{
		a: a, w: w, c: c,
		stack:    []pathEntry{{0, "pCloud"}},
		cache:    map[uint64][]pcloud.Item{},
		sortAsc:  true,
		selected: -1,
		thumbs:   newThumbCache(),
	}
	b.build()
	b.load(true)
	b.loadQuota()
}

func (b *browser) current() pathEntry { return b.stack[len(b.stack)-1] }

// ---------------------------------------------------------------- interface

func (b *browser) build() {
	b.search = widget.NewEntry()
	b.search.SetPlaceHolder("Rechercher dans ce dossier…  (Ctrl+F)")
	b.search.OnChanged = func(s string) {
		b.filter = s
		b.rebuildView()
	}

	b.list = widget.NewList(
		func() int { return len(b.view) },
		func() fyne.CanvasObject { return newFileRow(b) },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i < len(b.view) {
				o.(*fileRow).set(i, b.view[i])
			}
		},
	)
	b.list.OnSelected = func(i widget.ListItemID) {
		b.selected = i
		// La liste garde le focus clavier et mange les raccourcis : on le rend au canevas.
		b.w.Canvas().Unfocus()
	}
	b.list.OnUnselected = func(widget.ListItemID) { b.selected = -1 }

	// Barre d'outils.
	b.backBtn = widget.NewButtonWithIcon("", theme.NavigateBackIcon(), b.back)
	b.upBtn = widget.NewButtonWithIcon("", theme.MoveUpIcon(), b.up)
	refresh := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), b.refresh)
	newFolder := widget.NewButtonWithIcon("Nouveau dossier", theme.FolderNewIcon(), b.newFolder)
	var upload *widget.Button
	upload = widget.NewButtonWithIcon("Envoyer", theme.UploadIcon(), func() {
		menu := fyne.NewMenu("",
			fyne.NewMenuItem("Un fichier…", b.pickFileToUpload),
			fyne.NewMenuItem("Un dossier…", b.pickFolderToUpload),
		)
		pos := b.a.Driver().AbsolutePositionForObject(upload)
		widget.ShowPopUpMenuAtPosition(menu, b.w.Canvas(), pos.Add(fyne.NewPos(0, upload.Size().Height)))
	})
	hlsTool := widget.NewButtonWithIcon("Flux HLS", theme.MediaVideoIcon(), b.openHLSTool)
	logout := widget.NewButtonWithIcon("Déconnexion", theme.LogoutIcon(), b.logout)

	b.crumbs = container.NewHBox()
	toolbar := container.NewBorder(nil, nil,
		container.NewHBox(b.backBtn, b.upBtn, refresh),
		container.NewHBox(newFolder, upload, hlsTool, logout),
		container.NewHScroll(b.crumbs),
	)

	// En-tête de colonnes, aligné sur les lignes (même structure que fileRow).
	b.sortBtns = map[sortKey]*widget.Button{}
	mk := func(k sortKey) *widget.Button {
		btn := widget.NewButton("", func() { b.sortBy(k) })
		btn.Importance = widget.LowImportance
		btn.Alignment = widget.ButtonAlignLeading
		b.sortBtns[k] = btn
		return btn
	}
	header := container.NewBorder(nil, nil,
		container.NewGridWrap(fyne.NewSize(iconSize, 1)),
		container.NewHBox(
			container.NewGridWrap(fyne.NewSize(sizeColW, mk(sortSize).MinSize().Height), b.sortBtns[sortSize]),
			container.NewGridWrap(fyne.NewSize(dateColW, mk(sortDate).MinSize().Height), b.sortBtns[sortDate]),
		),
		mk(sortName),
	)

	// État vide / erreur de chargement.
	b.emptyLabel = widget.NewLabel("")
	b.emptyLabel.Alignment = fyne.TextAlignCenter
	b.emptyLabel.Wrapping = fyne.TextWrapWord
	b.retryBtn = widget.NewButtonWithIcon("Réessayer", theme.ViewRefreshIcon(), b.refresh)
	b.retryBtn.Hide()
	b.emptyBox = container.NewCenter(container.NewVBox(b.emptyLabel, b.retryBtn))
	b.emptyBox.Hide()

	// Barre d'état : contenu du dossier à gauche, quota à droite.
	b.status = widget.NewLabel("")
	b.quotaLabel = widget.NewLabel("")
	b.quotaBar = widget.NewProgressBar()
	b.quotaBar.TextFormatter = func() string { return "" }
	quota := container.NewHBox(b.quotaLabel, container.NewGridWrap(fyne.NewSize(140, b.quotaBar.MinSize().Height), b.quotaBar))
	bottom := container.NewBorder(nil, nil, b.status, quota)

	top := container.NewVBox(toolbar, b.search, header)
	b.w.SetContent(container.NewBorder(top, bottom, nil, nil, container.NewStack(b.list, b.emptyBox)))

	b.installShortcuts()
	b.w.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) {
		var paths []string
		for _, u := range uris {
			if u.Scheme() == "file" {
				paths = append(paths, u.Path())
			}
		}
		b.uploadPaths(paths)
	})
	b.updateSortButtons()
}

func (b *browser) installShortcuts() {
	canvas := b.w.Canvas()
	b.findKey = &desktop.CustomShortcut{KeyName: fyne.KeyF, Modifier: fyne.KeyModifierControl}
	canvas.AddShortcut(b.findKey, func(fyne.Shortcut) { canvas.Focus(b.search) })
	canvas.SetOnTypedKey(func(e *fyne.KeyEvent) {
		switch e.Name {
		case fyne.KeyBackspace:
			b.up()
		case fyne.KeyF5:
			b.refresh()
		case fyne.KeyReturn, fyne.KeyEnter:
			b.open(b.selected)
		case fyne.KeyDelete:
			if it, ok := b.selectedItem(); ok {
				b.confirmDelete(it)
			}
		case fyne.KeyF2:
			if it, ok := b.selectedItem(); ok {
				b.rename(it)
			}
		}
	})
}

// release retire les handlers installés sur la fenêtre avant de quitter l'écran.
func (b *browser) release() {
	b.gen++ // invalide les réponses en vol
	b.w.SetOnDropped(nil)
	b.w.Canvas().SetOnTypedKey(nil)
	b.w.Canvas().RemoveShortcut(b.findKey)
}

func (b *browser) logout() {
	_ = keyring.Delete(keyringService, keyringUser)
	b.release()
	showLogin(b.a, b.w, pcloud.New(), "")
}

// expire est appelé quand pCloud refuse le jeton.
func (b *browser) expire() {
	_ = keyring.Delete(keyringService, keyringUser)
	b.release()
	showLogin(b.a, b.w, pcloud.New(), "Session expirée : reconnectez-vous.")
}

// fail affiche une erreur, ou renvoie à la connexion si le jeton est refusé.
func (b *browser) fail(err error) {
	if pcloud.IsAuthError(err) {
		b.expire()
		return
	}
	dialog.ShowError(err, b.w)
}

func (b *browser) selectedItem() (pcloud.Item, bool) {
	if b.selected < 0 || b.selected >= len(b.view) {
		return pcloud.Item{}, false
	}
	return b.view[b.selected], true
}

// ---------------------------------------------------------------- navigation

func (b *browser) navigate(stack []pathEntry) {
	b.history = append(b.history, b.stack)
	b.stack = stack
	b.search.SetText("")
	b.filter = ""
	b.load(true)
}

func (b *browser) back() {
	if n := len(b.history); n > 0 {
		b.stack = b.history[n-1]
		b.history = b.history[:n-1]
		b.search.SetText("")
		b.filter = ""
		b.load(true)
	}
}

func (b *browser) up() {
	if len(b.stack) > 1 {
		b.navigate(append([]pathEntry(nil), b.stack[:len(b.stack)-1]...))
	}
}

// open ouvre un dossier, ou propose de télécharger un fichier.
func (b *browser) open(idx int) {
	if idx < 0 || idx >= len(b.view) {
		return
	}
	it := b.view[idx]
	if it.IsFolder {
		b.navigate(append(append([]pathEntry(nil), b.stack...), pathEntry{it.FolderID, it.Name}))
		return
	}
	b.download(it)
}

func (b *browser) refresh() { b.load(false) }

func (b *browser) renderCrumbs() {
	b.crumbs.RemoveAll()
	for i, e := range b.stack {
		if i > 0 {
			b.crumbs.Add(widget.NewIcon(theme.NavigateNextIcon()))
		}
		btn := widget.NewButton(e.name, func() {
			if i < len(b.stack)-1 {
				b.navigate(append([]pathEntry(nil), b.stack[:i+1]...))
			}
		})
		btn.Importance = widget.LowImportance
		b.crumbs.Add(btn)
	}
	b.crumbs.Refresh()
	if len(b.history) > 0 {
		b.backBtn.Enable()
	} else {
		b.backBtn.Disable()
	}
	if len(b.stack) > 1 {
		b.upBtn.Enable()
	} else {
		b.upBtn.Disable()
	}
}

// load affiche le dossier courant : d'abord le cache s'il existe (useCache),
// puis la réponse du réseau.
func (b *browser) load(useCache bool) {
	b.gen++
	gen := b.gen
	cur := b.current()
	b.renderCrumbs()
	b.retryBtn.Hide()

	if cached, ok := b.cache[cur.id]; ok && useCache {
		b.setItems(cached)
		b.status.SetText(summary(b.view) + " · actualisation…")
	} else {
		b.setItems(nil)
		b.emptyBox.Hide()
		b.status.SetText("Chargement…")
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := b.c.ListFolder(ctx, cur.id)
		fyne.Do(func() {
			if gen != b.gen {
				return
			}
			if err != nil {
				if pcloud.IsAuthError(err) {
					b.expire()
					return
				}
				b.showLoadError(err)
				return
			}
			if res == nil {
				res = []pcloud.Item{} // dossier vide : distinct de « pas encore chargé »
			}
			b.cache[cur.id] = res
			b.setItems(res)
		})
	}()
}

func (b *browser) showLoadError(err error) {
	b.setItems(nil)
	b.status.SetText("")
	b.emptyLabel.SetText("Impossible de charger ce dossier.\n" + err.Error())
	b.retryBtn.Show()
	b.emptyBox.Show()
}

func (b *browser) setItems(items []pcloud.Item) {
	b.items = items
	b.rebuildView()
}

// rebuildView applique filtre et tri, puis rafraîchit liste, état vide et statut.
func (b *browser) rebuildView() {
	v := filterItems(b.items, b.filter)
	sortItems(v, b.sortKey, b.sortAsc)
	b.view = v
	b.list.UnselectAll()
	b.list.Refresh()
	b.updateSortButtons()

	switch {
	case len(v) > 0:
		b.emptyBox.Hide()
	case strings.TrimSpace(b.filter) != "":
		b.emptyLabel.SetText("Aucun résultat pour « " + strings.TrimSpace(b.filter) + " ».")
		b.retryBtn.Hide()
		b.emptyBox.Show()
	case b.items != nil:
		b.emptyLabel.SetText("Dossier vide.\nGlissez des fichiers ici pour les envoyer.")
		b.retryBtn.Hide()
		b.emptyBox.Show()
	}
	if b.items != nil {
		b.status.SetText(summary(v))
	}
}

func (b *browser) sortBy(k sortKey) {
	if b.sortKey == k {
		b.sortAsc = !b.sortAsc
	} else {
		b.sortKey = k
		b.sortAsc = k == sortName
	}
	b.rebuildView()
}

func (b *browser) updateSortButtons() {
	labels := map[sortKey]string{sortName: "Nom", sortSize: "Taille", sortDate: "Modifié"}
	for k, btn := range b.sortBtns {
		text := labels[k]
		if k == b.sortKey {
			if b.sortAsc {
				text += " ▲"
			} else {
				text += " ▼"
			}
		}
		btn.SetText(text)
	}
}

func (b *browser) loadQuota() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		info, err := b.c.UserInfo(ctx)
		if err != nil || info.Quota <= 0 {
			return
		}
		fyne.Do(func() {
			b.quotaLabel.SetText(humanSize(info.UsedQuota) + " / " + humanSize(info.Quota))
			b.quotaBar.SetValue(float64(info.UsedQuota) / float64(info.Quota))
		})
	}()
}

// ---------------------------------------------------------------- miniatures

func (b *browser) requestThumb(it pcloud.Item) {
	b.thumbs.request(it.FileID, it.Name, func() ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return b.c.Thumbnail(ctx, it.FileID, thumbSize)
	}, func() {
		fyne.Do(b.scheduleRefresh)
	})
}

// scheduleRefresh regroupe les rafraîchissements dus à l'arrivée des miniatures.
func (b *browser) scheduleRefresh() {
	if b.refreshPending {
		return
	}
	b.refreshPending = true
	time.AfterFunc(150*time.Millisecond, func() {
		fyne.Do(func() {
			b.refreshPending = false
			b.list.Refresh()
		})
	})
}

// ---------------------------------------------------------------- menu et actions

func (b *browser) showMenu(idx int, pos fyne.Position) {
	if idx < 0 || idx >= len(b.view) {
		return
	}
	b.list.Select(idx)
	it := b.view[idx]

	var items []*fyne.MenuItem
	if it.IsFolder {
		items = append(items, fyne.NewMenuItem("Ouvrir", func() { b.open(idx) }))
	} else {
		items = append(items, fyne.NewMenuItem("Télécharger…", func() { b.download(it) }))
		if it.Category == pcloud.CategoryVideo {
			items = append(items, fyne.NewMenuItem("Télécharger une version streaming…", func() { b.downloadStream(it) }))
		}
	}
	items = append(items,
		fyne.NewMenuItem("Renommer…", func() { b.rename(it) }),
		fyne.NewMenuItem("Déplacer…", func() { b.move(it) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Créer et copier un lien public", func() { b.publicLink(it) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Supprimer…", func() { b.confirmDelete(it) }),
	)
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", items...), b.w.Canvas(), pos)
}

// mutate exécute une modification côté pCloud, puis recharge le dossier.
func (b *browser) mutate(op func(ctx context.Context) error) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		err := op(ctx)
		fyne.Do(func() {
			if err != nil {
				b.fail(err)
				return
			}
			b.cache = map[uint64][]pcloud.Item{}
			b.load(false)
			b.loadQuota()
		})
	}()
}

// askName ouvre un petit formulaire à un champ.
func (b *browser) askName(title, confirm, initial string, onOK func(name string)) {
	e := widget.NewEntry()
	e.SetText(initial)
	d := dialog.NewForm(title, confirm, "Annuler",
		[]*widget.FormItem{widget.NewFormItem("Nom", e)},
		func(ok bool) {
			if !ok {
				return
			}
			name := strings.TrimSpace(e.Text)
			if !validName(name) {
				dialog.ShowError(errors.New("nom invalide : il ne doit pas être vide ni contenir « / » ou « \\ »"), b.w)
				return
			}
			onOK(name)
		}, b.w)
	d.Resize(fyne.NewSize(420, d.MinSize().Height))
	d.Show()
	b.w.Canvas().Focus(e)
}

func (b *browser) newFolder() {
	parent := b.current().id
	b.askName("Nouveau dossier", "Créer", "", func(name string) {
		b.mutate(func(ctx context.Context) error { return b.c.CreateFolder(ctx, parent, name) })
	})
}

func (b *browser) rename(it pcloud.Item) {
	b.askName("Renommer", "Renommer", it.Name, func(name string) {
		if name == it.Name {
			return
		}
		b.mutate(func(ctx context.Context) error { return b.c.Rename(ctx, it, name) })
	})
}

func (b *browser) confirmDelete(it pcloud.Item) {
	msg := fmt.Sprintf("Supprimer « %s » ?", it.Name)
	if it.IsFolder {
		msg = fmt.Sprintf("Supprimer le dossier « %s » et tout son contenu ?", it.Name)
	}
	dialog.ShowConfirm("Supprimer", msg, func(ok bool) {
		if ok {
			b.mutate(func(ctx context.Context) error { return b.c.Delete(ctx, it) })
		}
	}, b.w)
}

func (b *browser) move(it pcloud.Item) {
	b.pickFolder("Déplacer « "+it.Name+" » vers…", it, func(dest uint64) {
		b.mutate(func(ctx context.Context) error { return b.c.Move(ctx, it, dest) })
	})
}

func (b *browser) publicLink(it pcloud.Item) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		link, err := b.c.PublicLink(ctx, it)
		fyne.Do(func() {
			if err != nil {
				b.fail(err)
				return
			}
			b.a.Clipboard().SetContent(link)
			dialog.ShowInformation("Lien copié", link, b.w)
		})
	}()
}

// pickFolder affiche un sélecteur de dossier pCloud. skip (fichier ou dossier
// à déplacer) n'est pas proposé comme destination.
func (b *browser) pickFolder(title string, skip pcloud.Item, onPick func(folderID uint64)) {
	stack := []pathEntry{{0, "pCloud"}}
	var folders []pcloud.Item
	path := widget.NewLabel("")
	path.Truncation = fyne.TextTruncateEllipsis
	var list *widget.List

	load := func() {}
	load = func() {
		cur := stack[len(stack)-1]
		names := make([]string, len(stack))
		for i, e := range stack {
			names[i] = e.name
		}
		path.SetText(strings.Join(names, " / "))
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			res, err := b.c.ListFolder(ctx, cur.id)
			fyne.Do(func() {
				if err != nil {
					b.fail(err)
					return
				}
				folders = folders[:0]
				for _, it := range res {
					if it.IsFolder && !(skip.IsFolder && it.FolderID == skip.FolderID) {
						folders = append(folders, it)
					}
				}
				sortItems(folders, sortName, true)
				list.UnselectAll()
				list.Refresh()
			})
		}()
	}

	list = widget.NewList(
		func() int { return len(folders) + 1 }, // ligne 0 : dossier parent
		func() fyne.CanvasObject {
			return container.NewHBox(widget.NewIcon(theme.FolderIcon()), widget.NewLabel(""))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			l := o.(*fyne.Container).Objects[1].(*widget.Label)
			switch {
			case i == 0 && len(stack) > 1:
				l.SetText(".. (dossier parent)")
			case i == 0:
				l.SetText("")
			case i-1 < len(folders):
				l.SetText(folders[i-1].Name)
			}
		},
	)
	list.OnSelected = func(i widget.ListItemID) {
		switch {
		case i == 0 && len(stack) > 1:
			stack = stack[:len(stack)-1]
			load()
		case i > 0 && i-1 < len(folders):
			stack = append(stack, pathEntry{folders[i-1].FolderID, folders[i-1].Name})
			load()
		default:
			list.UnselectAll()
		}
	}

	min := container.NewGridWrap(fyne.NewSize(460, 320), list)
	content := container.NewBorder(path, nil, nil, nil, min)
	d := dialog.NewCustomConfirm(title, "Choisir ce dossier", "Annuler", content, func(ok bool) {
		if ok {
			onPick(stack[len(stack)-1].id)
		}
	}, b.w)
	d.Show()
	load()
}

// ---------------------------------------------------------------- transferts

// transfer affiche une fenêtre de progression annulable pendant work, puis
// recharge le dossier.
func (b *browser) transfer(title string, work func(ctx context.Context, report func(text string, frac float64)) error) {
	ctx, cancel := context.WithCancel(context.Background())
	label := widget.NewLabel("")
	label.Truncation = fyne.TextTruncateEllipsis
	bar := widget.NewProgressBar()
	d := dialog.NewCustom(title, "Annuler",
		container.NewGridWrap(fyne.NewSize(440, label.MinSize().Height+bar.MinSize().Height+theme.Padding()),
			container.NewVBox(label, bar)), b.w)
	d.SetOnClosed(cancel)
	d.Show()

	var mu sync.Mutex
	var last time.Time
	report := func(text string, frac float64) {
		mu.Lock()
		now := time.Now()
		skip := frac < 1 && now.Sub(last) < 100*time.Millisecond
		if !skip {
			last = now
		}
		mu.Unlock()
		if skip {
			return
		}
		fyne.Do(func() {
			label.SetText(text)
			bar.SetValue(frac)
		})
	}

	go func() {
		err := work(ctx, report)
		fyne.Do(func() {
			d.Hide()
			cancel()
			if err != nil && !errors.Is(err, context.Canceled) {
				b.fail(err)
			}
			b.cache = map[uint64][]pcloud.Item{}
			b.load(false)
			b.loadQuota()
		})
	}()
}

func fraction(done, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(done) / float64(total)
}

func (b *browser) download(it pcloud.Item) {
	if it.IsFolder {
		return
	}
	d := dialog.NewFileSave(func(wc fyne.URIWriteCloser, err error) {
		if err != nil {
			dialog.ShowError(err, b.w)
			return
		}
		if wc == nil {
			return
		}
		b.transfer("Téléchargement", func(ctx context.Context, report func(string, float64)) error {
			defer wc.Close()
			meter := newSpeedMeter()
			return b.c.Download(ctx, it.FileID, wc, func(done, total int64) {
				meter.Set(done)
				report(progressText(it.Name, done, total, meter.Rate()), fraction(done, total))
			})
		})
	}, b.w)
	d.SetFileName(it.Name)
	d.Show()
}

func (b *browser) pickFileToUpload() {
	d := dialog.NewFileOpen(func(rc fyne.URIReadCloser, err error) {
		if err != nil {
			dialog.ShowError(err, b.w)
			return
		}
		if rc == nil {
			return
		}
		path := rc.URI().Path()
		_ = rc.Close()
		b.uploadPaths([]string{path})
	}, b.w)
	d.Show()
}

func (b *browser) pickFolderToUpload() {
	d := dialog.NewFolderOpen(func(lu fyne.ListableURI, err error) {
		if err != nil {
			dialog.ShowError(err, b.w)
			return
		}
		if lu != nil {
			b.uploadPaths([]string{lu.Path()})
		}
	}, b.w)
	d.Show()
}

// uploadPaths envoie des fichiers et des dossiers (récursivement) dans le
// dossier courant. Les liens symboliques sont ignorés.
func (b *browser) uploadPaths(paths []string) {
	if len(paths) == 0 {
		return
	}
	dest := b.current().id
	b.transfer("Envoi", func(ctx context.Context, report func(string, float64)) error {
		total := 0
		for _, p := range paths {
			total += countFiles(p)
		}
		n := 0
		meter := newSpeedMeter()
		var sent int64 // octets des fichiers déjà envoyés
		var up func(parent uint64, p string) error
		up = func(parent uint64, p string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			fi, err := os.Lstat(p)
			if err != nil {
				return err
			}
			switch {
			case fi.Mode()&os.ModeSymlink != 0:
				return nil
			case fi.IsDir():
				id, err := b.c.EnsureFolder(ctx, parent, filepath.Base(p))
				if err != nil {
					return err
				}
				entries, err := os.ReadDir(p)
				if err != nil {
					return err
				}
				for _, e := range entries {
					if err := up(id, filepath.Join(p, e.Name())); err != nil {
						return err
					}
				}
				return nil
			case !fi.Mode().IsRegular():
				return nil
			}
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			defer f.Close()
			n++
			prefix := fmt.Sprintf("%d/%d · %s", n, total, fi.Name())
			err = b.c.Upload(ctx, parent, fi.Name(), f, fi.Size(), fi.ModTime(), func(done, size int64) {
				meter.Set(sent + done)
				report(progressText(prefix, done, size, meter.Rate()), fraction(done, size))
			})
			sent += fi.Size()
			return err
		}
		for _, p := range paths {
			if err := up(dest, p); err != nil {
				return err
			}
		}
		return nil
	})
}

// countFiles compte les fichiers réguliers sous p (p lui-même s'il en est un).
func countFiles(p string) int {
	n := 0
	_ = filepath.WalkDir(p, func(_ string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			n++
		}
		return nil
	})
	return n
}
