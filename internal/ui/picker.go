package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

func showSetupPicker(w fyne.Window, picker *dialog.FileDialog, title string) {
	overlays := w.Canvas().Overlays()
	previous := overlays.Top()
	picker.SetTitleText(title)
	picker.Show()
	popup, ok := overlays.Top().(*widget.PopUp)
	if !ok || popup == previous {
		return // A native picker does not add a Fyne overlay.
	}

	// Fyne's modal PopUp always centers its content. Reuse the picker controls
	// and callbacks in a movable overlay that still blocks the rest of Setup.
	picker.SetTitleText(" ")
	bar := &pickerTitle{}
	bar.ExtendBaseWidget(bar)
	bar.Text = title
	bar.TextStyle.Bold = true
	header := container.NewStack(canvas.NewRectangle(hexColor(colorButton)), bar)
	background := canvas.NewRectangle(hexColor(colorCard))
	background.StrokeColor = hexColor(colorBorder)
	background.StrokeWidth = 1
	panel := container.NewStack(background, container.NewPadded(container.NewBorder(header, nil, nil, nil, popup.Content)))
	placement := &pickerPlacement{preferred: panel.MinSize().Max(popup.Content.Size().Add(fyne.NewSize(8, bar.MinSize().Height+12)))}
	layer := container.New(placement, canvas.NewRectangle(color.NRGBA{A: 128}), panel)
	bar.move = func(delta fyne.Delta) {
		placement.moved = true
		placement.position = placement.position.AddXY(delta.DX, delta.DY)
		placement.Layout(layer.Objects, layer.Size())
	}
	picker.SetOnClosed(func() { overlays.Remove(layer) })
	overlays.Remove(popup)
	layer.Resize(w.Canvas().Size())
	overlays.Add(layer)
}

type pickerTitle struct {
	widget.Label
	move func(fyne.Delta)
}

func (t *pickerTitle) Dragged(event *fyne.DragEvent) { t.move(event.Dragged) }
func (*pickerTitle) DragEnd()                       {}
func (*pickerTitle) Cursor() desktop.Cursor         { return desktop.PointerCursor }

type pickerPlacement struct {
	preferred fyne.Size
	position  fyne.Position
	moved     bool
}

func (*pickerPlacement) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.Size{} }

func (p *pickerPlacement) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	objects[0].Resize(size)
	inset := theme.Padding()
	panel := objects[1]
	bounds := p.preferred.Min(fyne.NewSize(max(0, size.Width-2*inset), max(0, size.Height-2*inset)))
	if !p.moved {
		p.position = fyne.NewPos((size.Width-bounds.Width)/2, (size.Height-bounds.Height)/2)
	}
	p.position.X = max(inset, min(p.position.X, size.Width-bounds.Width-inset))
	p.position.Y = max(inset, min(p.position.Y, size.Height-bounds.Height-inset))
	panel.Move(p.position)
	panel.Resize(bounds)
}
