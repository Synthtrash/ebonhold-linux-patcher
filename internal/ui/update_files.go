package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type fileState uint8

const (
	fileQueued fileState = iota
	fileUpdated
	fileRemoved
	fileMissing
	fileMismatch
	fileUnreadable
	fileDownloadFailed
	fileDuplicate
)

var fileStates = [...]struct {
	label, hint string
	color       fyne.ThemeColorName
}{
	fileQueued:         {"Queued", "Waiting for the launcher to download this file.", theme.ColorNameDisabled},
	fileUpdated:        {"Updated", "The launcher wrote this file.", theme.ColorNameSuccess},
	fileRemoved:        {"Removed", "The launcher removed this file from the active client.", theme.ColorNameError},
	fileMissing:        {"Missing", "Run an update to download the missing file.", theme.ColorNameWarning},
	fileMismatch:       {"Checksum mismatch", "Run an update to replace this file.", theme.ColorNameWarning},
	fileUnreadable:     {"Unreadable", "Check the file's permissions and verify again.", theme.ColorNameWarning},
	fileDownloadFailed: {"Download failed", "Run an update to retry the download.", theme.ColorNameWarning},
	fileDuplicate:      {"Duplicate filename", "Run an update to remove the case-only duplicate.", theme.ColorNameWarning},
}

type fileChange struct {
	path, detail string
	state        fileState
}

type updateFilePane struct {
	tr                     translate
	content, empty, active *fyne.Container
	list                   *widget.List
	summary, detailText    *widget.Label
	detailTitle            *canvas.Text
	inspector              *fyne.Container
	rows                   []fileChange
	indexes                map[string]int
	selected               int
}

func newUpdateFilePane(w fyne.Window, tr translate, card func(fyne.CanvasObject) fyne.CanvasObject) *updateFilePane {
	p := &updateFilePane{tr: tr, indexes: make(map[string]int), selected: -1}
	p.summary = widget.NewLabel(tr("Run Update to see files that change."))
	p.summary.Wrapping = fyne.TextWrapWord
	p.summary.Importance = widget.LowImportance
	title := canvas.NewText(tr("File changes"), hexColor(colorText))
	title.TextSize = 16
	title.TextStyle.Bold = true
	header := container.NewVBox(title, flushText(p.summary))
	// List scrollbars overlay their rows; keep the status text clear of the thumb.
	rowInset := layout.NewCustomPaddedLayout(0, 0, 0, 12)
	p.list = widget.NewList(
		func() int { return len(p.rows) },
		func() fyne.CanvasObject {
			path := widget.NewRichTextWithText(" ")
			path.Truncation = fyne.TextTruncateEllipsis
			state := canvas.NewText(" ", hexColor(colorMuted))
			state.TextSize = 14
			row := container.NewBorder(nil, nil, nil, container.NewCenter(state), path)
			return container.New(rowInset, row)
		},
		func(id widget.ListItemID, item fyne.CanvasObject) {
			if id >= len(p.rows) {
				return
			}
			change := p.rows[id]
			style := fileStates[change.state]
			row := item.(*fyne.Container).Objects[0].(*fyne.Container)
			span := row.Objects[0].(*widget.RichText).Segments[0].(*widget.TextSegment)
			span.Text, span.Style.ColorName = change.path, style.color
			state := row.Objects[1].(*fyne.Container).Objects[0].(*canvas.Text)
			state.Text, state.Color = tr(style.label), frostColors[style.color]
			row.Refresh()
		},
	)
	p.detailTitle = canvas.NewText("", hexColor(colorText))
	p.detailTitle.TextStyle.Bold = true
	p.detailText = widget.NewLabel("")
	p.detailText.Wrapping = fyne.TextWrapWord
	details := container.NewVScroll(container.New(rowInset, flushText(p.detailText)))
	details.SetMinSize(fyne.NewSize(0, 64))
	copyPath := widget.NewButton(tr("Copy path"), func() {
		if p.selected >= 0 {
			w.Clipboard().SetContent(p.rows[p.selected].path)
		}
	})
	p.inspector = container.NewVBox(widget.NewSeparator(),
		container.NewBorder(nil, nil, container.NewCenter(p.detailTitle), copyPath), details)
	p.inspector.Hide()
	p.list.OnSelected = func(id widget.ListItemID) {
		p.selected = id
		p.showSelection()
		details.ScrollToTop()
		p.inspector.Show()
		p.active.Refresh()
	}
	p.empty = container.NewVBox(card(header))
	p.active = container.NewStack(card(container.NewBorder(header, p.inspector, nil, nil, p.list)))
	p.content = container.NewStack(p.empty)
	return p
}

func (p *updateFilePane) showSelection() {
	row := p.rows[p.selected]
	style := fileStates[row.state]
	p.detailTitle.Text, p.detailTitle.Color = p.tr(style.label), frostColors[style.color]
	p.detailTitle.Refresh()
	text := p.tr(style.hint)
	if row.detail != "" {
		text = p.tr.diagnostic(row.detail) + "\n" + text
	}
	p.detailText.SetText(text + "\n" + row.path)
}

func (p *updateFilePane) showSummary(text string) {
	p.summary.SetText(text)
}

func (p *updateFilePane) record(path string, state fileState, detail string) {
	if path == "" {
		return
	}
	change := fileChange{path: path, state: state, detail: detail}
	if index, ok := p.indexes[path]; ok {
		p.rows[index] = change
		p.list.RefreshItem(index)
		if index == p.selected {
			p.showSelection()
			p.inspector.Refresh()
		}
		return
	}
	p.indexes[path] = len(p.rows)
	p.rows = append(p.rows, change)
	p.showSummary(p.tr("%d files affected", len(p.rows)))
	if len(p.rows) == 1 {
		p.content.Objects[0] = p.active
		p.content.Refresh()
	} else {
		p.list.Refresh()
	}
}

func (p *updateFilePane) reset(text string) {
	if len(p.rows) != 0 {
		p.list.UnselectAll()
		p.list.ScrollToTop()
	}
	clear(p.rows)
	p.rows = p.rows[:0]
	clear(p.indexes)
	p.selected = -1
	p.inspector.Hide()
	p.showSummary(text)
	p.content.Objects[0] = p.empty
	p.content.Refresh()
}
