package ui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/synthtrash/ebonhold-linux-patcher/internal/launcher"
)

func wrappedLabel(text string) *widget.Label {
	label := widget.NewLabel(text)
	label.Wrapping = fyne.TextWrapWord
	return label
}

func installationGuideLink(tr translate) *widget.Hyperlink {
	return widget.NewHyperlink(tr("Ebonhold Installation Guide"), &url.URL{Scheme: "https", Host: "project-ebonhold.com", Path: "/support/installation-guide"})
}

func bottlesInstallLink(tr translate) *widget.Hyperlink {
	return widget.NewHyperlink(tr("Get Bottles on Flathub"), &url.URL{Scheme: "https", Host: "flathub.org", Path: "/en/apps/com.usebottles.bottles"})
}

func showInstallHelp(w fyne.Window, tr translate) {
	content := container.NewVBox(
		wrappedLabel(tr("Download and extract the game using the installation guide. Then return here and choose the game's Wow.exe.")),
		container.NewHBox(installationGuideLink(tr)),
		widget.NewSeparator(),
		wrappedLabel(tr("Keep using Steam, Lutris, or Bottles if you already have one set up. Otherwise, install Bottles, create a Gaming bottle, and use Run Executable to open Wow.exe.")),
		container.NewHBox(bottlesInstallLink(tr)),
	)
	d := dialog.NewCustom(tr("Install Ebonhold on Linux"), tr("Close"), content, w)
	d.Resize(fyne.NewSize(min(560, w.Canvas().Size().Width-32), 290))
	d.Show()
}

func launchTargetTitle(tr translate, manager string) string {
	if strings.HasPrefix(manager, "bottles-") {
		return tr("Choose a bottle…")
	}
	return tr("Choose a game shortcut…")
}

func showLaunchTarget(ctx context.Context, w fyne.Window, tr translate, target launcher.LaunchTarget, root string, save func(string)) {
	title := launchTargetTitle(tr, target.Manager)
	validate := func(id string) error {
		if err := (launcher.LaunchTarget{Manager: target.Manager, ID: id}).Validate(); err != nil {
			return errors.New(tr(err.Error()))
		}
		return nil
	}
	size := fyne.NewSize(min(580, w.Canvas().Size().Width-32), 256)
	if strings.HasPrefix(target.Manager, "bottles-") {
		picker := widget.NewSelectEntry(nil)
		picker.SetText(target.ID)
		picker.SetPlaceHolder(tr("Finding bottles…"))
		picker.Validator = validate
		dropdown := picker.ActionItem.(fyne.Disableable)
		dropdown.Disable()
		note := wrappedLabel(tr("Pick the bottle you use for Ebonhold, or type its name as shown in Bottles."))
		closed := false
		form := dialog.NewForm(title, tr("Save"), tr("Cancel"), []*widget.FormItem{
			widget.NewFormItem(tr("Bottle name"), picker), widget.NewFormItem("", note),
		}, func(ok bool) {
			closed = true
			if ok {
				save(picker.Text)
			}
		}, w)
		form.Resize(size)
		form.Show()
		go func() {
			names, err := launcher.ListBottles(ctx, target.Manager)
			fyne.Do(func() {
				if closed || ctx.Err() != nil {
					return
				}
				picker.SetPlaceHolder(tr("Select or type a bottle name"))
				if err != nil || len(names) == 0 {
					note.SetText(tr("No bottles were listed. In Bottles, find the setup you use for Ebonhold and type its name here."))
					form.Refresh()
					return
				}
				picker.SetOptions(names)
				dropdown.Enable()
			})
		}()
		return
	}

	var d *dialog.CustomDialog
	ids := map[string]string{}
	picker := widget.NewSelect(nil, nil)
	picker.PlaceHolder = tr("Choose a shortcut")
	picker.Disable()
	confirm := widget.NewButton(tr("Save"), func() {
		if id := ids[picker.Selected]; id != "" {
			d.Hide()
			save(id)
		}
	})
	confirm.Importance = widget.HighImportance
	confirm.Disable()
	picker.OnChanged = func(label string) { setEnabled(confirm, ids[label] != "") }
	advanced := widget.NewButton(tr("Advanced: enter shortcut ID"), func() {
		d.Hide()
		id := widget.NewEntry()
		id.SetText(target.ID)
		id.Validator = validate
		form := dialog.NewForm(tr("Enter a shortcut ID"), tr("Save"), tr("Cancel"), []*widget.FormItem{
			widget.NewFormItem(tr("Shortcut ID"), id),
			widget.NewFormItem("", wrappedLabel(tr("Copy the number after rungameid/ from your game app's desktop shortcut."))),
		}, func(ok bool) {
			if ok {
				save(strings.TrimSpace(id.Text))
			}
		}, w)
		form.Resize(size)
		form.Show()
	})
	advanced.Importance = widget.LowImportance
	note := wrappedLabel(tr("Finding shortcuts…"))
	d = dialog.NewCustomWithoutButtons(title, container.NewVBox(note, picker, container.NewHBox(advanced)), w)
	d.SetButtons([]fyne.CanvasObject{widget.NewButton(tr("Cancel"), d.Hide), confirm})
	closed := false
	d.SetOnClosed(func() { closed = true })
	d.Resize(size)
	d.Show()
	go func() {
		shortcuts := launcher.ListGameShortcuts(ctx, target.Manager, root)
		fyne.Do(func() {
			if closed || ctx.Err() != nil {
				return
			}
			if len(shortcuts) == 0 {
				if target.Manager == "steam" {
					note.SetText(tr("No matching shortcut. In Steam, choose Games > Add a Non-Steam Game and browse to this folder's Wow.exe."))
				} else {
					note.SetText(tr("No matching shortcut. In Lutris, add a locally installed game, choose the Wine runner, and select this folder's Wow.exe."))
				}
				d.Refresh()
				return
			}
			options := make([]string, 0, len(shortcuts))
			selected := ""
			for _, shortcut := range shortcuts {
				label := shortcut.Name
				for ids[label] != "" {
					label += " · " + shortcut.ID
				}
				ids[label] = shortcut.ID
				options = append(options, label)
				if shortcut.ID == target.ID {
					selected = label
				}
			}
			picker.SetOptions(options)
			picker.Enable()
			if len(options) == 1 {
				note.SetText(tr("Found one matching shortcut. Choose Save to use it."))
				selected = options[0]
			} else {
				note.SetText(tr("Choose a shortcut for this game folder."))
			}
			if selected != "" {
				picker.SetSelected(selected)
			}
			d.Refresh()
		})
	}()
}

func setupFolderMissing(path string) bool {
	if strings.TrimSpace(path) == "" {
		return true
	}
	_, err := os.Lstat(filepath.Clean(path))
	return errors.Is(err, os.ErrNotExist)
}

func copySize(bytes uint64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(bytes)/(1<<20))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
