package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"pcloud-desktop/internal/pcloud"
)

const (
	iconSize  = 32
	sizeColW  = 90
	dateColW  = 140
	thumbSize = "64x64"
)

// fileRow est une ligne de la liste : icône ou miniature, nom, taille, date.
// Double-clic : ouvrir ; clic droit : menu contextuel.
type fileRow struct {
	widget.BaseWidget
	b     *browser
	idx   int
	icon  *widget.Icon
	thumb *canvas.Image
	name  *widget.Label
	size  *widget.Label
	date  *widget.Label
}

func newFileRow(b *browser) *fileRow {
	r := &fileRow{
		b:     b,
		icon:  widget.NewIcon(nil),
		thumb: canvas.NewImageFromResource(nil),
		name:  widget.NewLabel(""),
		size:  widget.NewLabel(""),
		date:  widget.NewLabel(""),
	}
	r.thumb.FillMode = canvas.ImageFillContain
	r.thumb.Hide()
	r.name.Truncation = fyne.TextTruncateEllipsis
	r.size.Alignment = fyne.TextAlignTrailing
	r.ExtendBaseWidget(r)
	return r
}

func (r *fileRow) CreateRenderer() fyne.WidgetRenderer {
	visual := container.NewGridWrap(fyne.NewSize(iconSize, iconSize), container.NewStack(r.icon, r.thumb))
	right := container.NewHBox(
		container.NewGridWrap(fyne.NewSize(sizeColW, r.size.MinSize().Height), r.size),
		container.NewGridWrap(fyne.NewSize(dateColW, r.date.MinSize().Height), r.date),
	)
	return widget.NewSimpleRenderer(container.NewBorder(nil, nil, visual, right, r.name))
}

func (r *fileRow) set(idx int, it pcloud.Item) {
	r.idx = idx
	r.name.SetText(it.Name)
	r.date.SetText(formatDate(it.ModTime()))
	if it.IsFolder {
		r.size.SetText("")
	} else {
		r.size.SetText(humanSize(it.Size))
	}

	r.icon.SetResource(iconFor(it))
	r.icon.Show()
	r.thumb.Hide()
	if it.IsFolder || !it.Thumb {
		return
	}
	res, known := r.b.thumbs.get(it.FileID)
	switch {
	case res != nil:
		r.thumb.Resource = res
		r.thumb.Refresh()
		r.thumb.Show()
		r.icon.Hide()
	case !known:
		r.b.requestThumb(it)
	}
}

func (r *fileRow) DoubleTapped(*fyne.PointEvent) { r.b.open(r.idx) }

func (r *fileRow) TappedSecondary(e *fyne.PointEvent) { r.b.showMenu(r.idx, e.AbsolutePosition) }
