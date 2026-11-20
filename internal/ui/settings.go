package ui

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/synthtrash/ebonhold-linux-patcher/internal/launcher"
)

func showSettings(w fyne.Window, tr translate, profileName, launcherLang, manager string, game launcher.GameSettings, applyCVars func(string) (bool, error), addToApplications func() error, save func(launcher.GameSettings, string)) {
	unchanged := tr("Leave unchanged")
	gameLocales := map[string]string{"English": "enUS", "Français": "frFR", "Español": "esES", "Deutsch": "deDE"}
	gameLanguage := widget.NewSelect([]string{unchanged, "English", "Français", "Español", "Deutsch"}, nil)
	gameLanguage.SetSelected(unchanged)
	for label, locale := range gameLocales {
		if locale == game.Language {
			gameLanguage.SetSelected(label)
			break
		}
	}
	hdCharacters := widget.NewCheck(tr("HD characters and world"), nil)
	hdCharacters.SetChecked(game.HDCharacters)
	hdCreatures := widget.NewCheck(tr("HD creatures"), nil)
	hdCreatures.SetChecked(game.HDCreatures)
	manageHD := widget.NewCheck(tr("Manage HD Patches"), func(checked bool) {
		setEnabled(hdCharacters, checked)
		setEnabled(hdCreatures, checked)
	})
	manageHD.SetChecked(game.ManageHD)
	setEnabled(hdCharacters, manageHD.Checked)
	setEnabled(hdCreatures, manageHD.Checked)
	uiLanguage := widget.NewSelect([]string{"English", "Français"}, nil)
	uiLanguage.SetSelected("English")
	if launcherLang == "fr" {
		uiLanguage.SetSelected("Français")
	}
	gameNotice := widget.NewLabel(tr("Game language and HD changes apply when you update. The realm's official language and support are English."))
	gameNotice.Wrapping = fyne.TextWrapWord
	updateBeforePlay := widget.NewCheck(tr("Check for updates before Play"), nil)
	updateBeforePlay.SetChecked(game.UpdateBeforePlay)
	editCVars := widget.NewButton(tr("Game CVars…"), func() { showGameCVars(w, tr, profileName, applyCVars) })
	launch := container.NewVBox(container.NewBorder(nil, nil, nil, editCVars, updateBeforePlay))
	flags := widget.NewMultiLineEntry()
	if strings.HasPrefix(manager, "bottles-") {
		flags.SetMinRowsVisible(2)
		flags.SetText(game.BottlesFlags)
		flags.SetPlaceHolder("-windowed")
		hint := widget.NewLabel(tr("Optional. Enter one argument per line, without quotes."))
		hint.Wrapping = fyne.TextWrapWord
		launch.Add(widget.NewLabel(tr("Bottles game arguments")))
		launch.Add(flags)
		launch.Add(hint)
	} else if manager == "steam" || manager == "lutris" {
		note := widget.NewLabel(tr("Set extra game arguments in %s.", map[string]string{"steam": "Steam", "lutris": "Lutris"}[manager]))
		note.Wrapping = fyne.TextWrapWord
		launch.Add(note)
	}
	addShortcut := widget.NewButton(tr("Add to Applications"), func() {
		if err := addToApplications(); err != nil {
			dialog.ShowInformation(tr("Could not add to Applications"), tr(err.Error()), w)
			return
		}
		dialog.ShowInformation(tr("Added to Applications"), tr("Find Ebonhold Linux Launcher in your app menu. Keep the launcher file in its current folder."), w)
	})
	launch.Add(widget.NewSeparator())
	launch.Add(container.NewHBox(addShortcut))
	gameOptions := container.NewVBox(
		container.New(layout.NewFormLayout(), widget.NewLabel(tr("Game language")), gameLanguage),
		manageHD,
		container.NewHBox(hdCharacters, hdCreatures),
		gameNotice,
		installationGuideLink(tr),
	)
	pages := [...]fyne.CanvasObject{launch, gameOptions}
	page := container.NewStack(pages[0])
	body := container.NewVScroll(page)
	tabs := container.NewHBox()
	var underlines [len(pages)]*canvas.Rectangle
	accent := hexColor(colorBlue)
	for i, title := range [...]string{tr("Launch"), tr("Game")} {
		tab := widget.NewButton(title, func() {
			page.Objects[0] = pages[i]
			for j, line := range underlines {
				line.FillColor = color.Transparent
				if i == j {
					line.FillColor = accent
				}
				line.Refresh()
			}
			page.Refresh()
			body.ScrollToTop()
		})
		tab.Importance = widget.LowImportance
		line := canvas.NewRectangle(color.Transparent)
		line.SetMinSize(fyne.NewSize(0, 3))
		underlines[i] = line
		tabs.Add(container.NewVBox(tab, line))
	}
	underlines[0].FillColor = accent
	languagePicker := container.NewHBox(widget.NewLabel(tr("Launcher language")), uiLanguage)
	header := container.NewVBox(container.NewBorder(nil, nil, tabs, container.NewCenter(languagePicker)), widget.NewSeparator())
	settings := container.NewBorder(header, nil, nil, nil, body)
	d := dialog.NewCustomConfirm(tr("Settings · %s", profileName), tr("Save"), tr("Cancel"), settings, func(ok bool) {
		if !ok {
			return
		}
		language := "en"
		if uiLanguage.Selected == "Français" {
			language = "fr"
		}
		game.Language = gameLocales[gameLanguage.Selected]
		game.ManageHD = manageHD.Checked
		game.HDCharacters = manageHD.Checked && hdCharacters.Checked
		game.HDCreatures = manageHD.Checked && hdCreatures.Checked
		game.UpdateBeforePlay = updateBeforePlay.Checked
		if strings.HasPrefix(manager, "bottles-") {
			game.BottlesFlags = flags.Text
		}
		save(game, language)
	}, w)
	size := w.Canvas().Size()
	d.Resize(fyne.NewSize(min(550, size.Width-32), min(424, size.Height-24)))
	d.Show()
}

func showGameCVars(w fyne.Window, tr translate, profileName string, applyCVars func(string) (bool, error)) {
	help := widget.NewLabel(tr("Close WoW first. Enter one name=value per line, without quotes. Only this profile changes."))
	help.Wrapping = fyne.TextWrapWord
	entry := widget.NewMultiLineEntry()
	entry.TextStyle.Monospace = true
	entry.SetMinRowsVisible(4)
	entry.SetPlaceHolder("gxResolution=1920x1080")
	windowed := widget.NewButton(tr("Load windowed preset"), func() {
		entry.SetText("gxWindow=1\ngxMaximize=1")
	})
	guide := widget.NewButton(tr("CVar guide…"), func() { showCVarGuide(w, tr) })
	result := widget.NewLabel(tr("The preset keeps your resolution. To change it, load the preset before adding gxResolution."))
	result.Wrapping = fyne.TextWrapWord
	result.Importance = widget.LowImportance
	d := dialog.NewCustomWithoutButtons(tr("Game CVars · %s", profileName),
		container.NewVScroll(container.NewVBox(help, container.NewGridWithColumns(2, windowed, guide), entry, result)), w)
	apply := widget.NewButton(tr("Apply"), func() {
		changed, err := applyCVars(entry.Text)
		switch {
		case err != nil:
			result.Importance = widget.DangerImportance
			result.SetText(tr("Could not apply CVars: %s", tr.diagnostic(err.Error())))
		case changed:
			result.Importance = widget.SuccessImportance
			result.SetText(tr("CVars saved. Start WoW to use them. If Config.wtf existed, its previous version is in WTF/Config.wtf.before-launcher-cvars."))
		default:
			result.Importance = widget.LowImportance
			result.SetText(tr("These CVars are already set."))
		}
	})
	apply.Importance = widget.HighImportance
	d.SetButtons([]fyne.CanvasObject{widget.NewButton(tr("Close"), d.Hide), apply})
	size := w.Canvas().Size()
	d.Resize(fyne.NewSize(min(530, size.Width-32), min(400, size.Height-24)))
	d.Show()
}

func showCVarGuide(w fyne.Window, tr translate) {
	intro := widget.NewLabel(tr("Black bars or offset clicks? Try the windowed preset. It replaces your draft. Click Apply to save."))
	intro.Wrapping = fyne.TextWrapWord
	rows := container.NewVBox(flushText(intro))
	for _, item := range [...]struct{ example, description string }{
		{"gxWindow=1", "Starts WoW in windowed mode instead of exclusive fullscreen."},
		{"gxMaximize=1", "Fills the screen while WoW is in windowed mode."},
		{"gxResolution=1920x1080", "If needed, use a resolution your display supports."},
	} {
		code := widget.NewLabelWithStyle(item.example, fyne.TextAlignLeading, fyne.TextStyle{Monospace: true, Bold: true})
		code.Selectable = true
		description := widget.NewLabel(tr(item.description))
		description.Wrapping = fyne.TextWrapWord
		rows.Add(container.NewVBox(flushText(code), flushText(description)))
	}
	note := widget.NewLabel(tr("These settings won't fix a Wine or X11 startup error."))
	note.Importance = widget.LowImportance
	note.Wrapping = fyne.TextWrapWord
	rows.Add(flushText(note))
	d := dialog.NewCustom(tr("CVar guide"), tr("Back to CVars"), container.NewVScroll(rows), w)
	size := w.Canvas().Size()
	d.Resize(fyne.NewSize(min(530, size.Width-32), min(400, size.Height-24)))
	d.Show()
}
