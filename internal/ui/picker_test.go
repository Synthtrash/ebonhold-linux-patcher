package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestMovedFolderPickerStillOpensFolder(t *testing.T) {
	a := test.NewApp()
	w := a.NewWindow("Setup")
	defer w.Close()
	w.Resize(fyne.NewSize(960, 600))
	dir, err := storage.ListerForURI(storage.NewFileURI(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	var opened fyne.ListableURI
	picker := dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
		if err != nil {
			t.Error(err)
		}
		opened = uri
	}, w)
	picker.SetLocation(dir)
	showSetupPicker(w, picker, "Choose game folder")

	c := w.Canvas()
	title, ok := findControl(c.Overlays().Top(), func(t *pickerTitle) bool { return t.Text == "Choose game folder" })
	if !ok {
		t.Fatal("folder picker title not visible")
	}
	d := a.Driver()
	start := d.AbsolutePositionForObject(title).AddXY(12, 12)
	test.Drag(c, start, -c.Size().Width, -c.Size().Height)
	end := d.AbsolutePositionForObject(title).AddXY(12, 12)
	if end.X < 0 || end.X >= start.X || end.Y < 0 || end.Y >= start.Y {
		t.Fatalf("title did not move within the window: %v → %v", start, end)
	}
	open, ok := findControl(c.Overlays().Top(), func(b *widget.Button) bool { return b.Text == "Open" })
	if !ok {
		t.Fatal("Open not visible after drag")
	}
	test.TapCanvas(c, d.AbsolutePositionForObject(open).AddXY(open.Size().Width/2, open.Size().Height/2))
	if opened == nil || opened.Path() != dir.Path() || c.Overlays().Top() != nil {
		t.Fatalf("Open did not close the picker and return %s: got %v", dir.Path(), opened)
	}
}
