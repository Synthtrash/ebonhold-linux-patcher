package ui

import (
	"context"
	"net/url"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/synthtrash/ebonhold-linux-patcher/internal/launcher"
)

func newNewsView(ctx context.Context, tr translate, fetch func(context.Context) ([]launcher.NewsArticle, error), showNews func()) (tab, preview fyne.CanvasObject, load func()) {
	site := url.URL{Scheme: "https", Host: "project-ebonhold.com", Path: "/news"}
	allNews := widget.NewHyperlink(tr("On the Ebonhold website"), &site)
	title := canvas.NewText(tr("Project Ebonhold news"), hexColor(colorText))
	title.TextSize = 26
	title.TextStyle.Bold = true
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	status.Hide()
	previewStatus := widget.NewLabel("")
	previewStatus.Importance = widget.LowImportance
	previewStatus.Wrapping = fyne.TextWrapWord
	previewStatusRow := flushText(previewStatus)
	previewStatusRow.Hide()
	previewEntries := container.NewVBox()
	previewTitle := canvas.NewText(tr("Latest news"), hexColor(colorText))
	previewTitle.TextSize = 16
	previewTitle.TextStyle.Bold = true
	viewAll := widget.NewButton(tr("All news"), showNews)
	viewAll.Importance = widget.LowImportance
	previewHeader := container.NewBorder(nil, nil, nil, viewAll, container.NewHBox(container.NewCenter(previewTitle)))
	previewPanel := container.NewVBox(previewHeader, previewStatusRow, previewEntries)
	setStatus := func(text string) {
		status.SetText(text)
		status.Show()
		if len(previewEntries.Objects) == 0 {
			previewStatus.SetText(text)
			previewStatusRow.Show()
			previewPanel.Refresh()
		}
	}
	entries := container.NewVBox()
	scroll := container.NewVScroll(entries)
	refresh := widget.NewButtonWithIcon(tr("Refresh"), theme.ViewRefreshIcon(), nil)
	refresh.OnTapped = func() {
		if refresh.Disabled() || ctx.Err() != nil {
			return
		}
		if fetch == nil {
			setStatus(tr("News could not be loaded."))
			return
		}
		refresh.Disable()
		setStatus(tr("Loading news…"))
		go func() {
			articles, err := fetch(ctx)
			if ctx.Err() != nil {
				return
			}
			fyne.Do(func() {
				if ctx.Err() != nil {
					return
				}
				refresh.Enable()
				if err != nil {
					setStatus(tr("News could not be loaded."))
					return
				}
				clear(entries.Objects)
				entries.Objects = entries.Objects[:0]
				clear(previewEntries.Objects)
				previewEntries.Objects = previewEntries.Objects[:0]
				for i, article := range articles[:min(len(articles), 8)] {
					link := site
					link.Path += "/" + strconv.FormatInt(article.ID, 10)
					headline := widget.NewHyperlinkWithStyle(article.Title, &link, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
					headline.Wrapping = fyne.TextWrapBreak
					var dateBuf [32]byte
					month := tr(article.Date.Month().String()[:3])
					dayMonth := append(article.Date.AppendFormat(dateBuf[:0], "2 "), month...)
					date := string(article.Date.AppendFormat(dayMonth, " 2006"))
					dateLabel := widget.NewLabel(date)
					dateLabel.Importance = widget.LowImportance
					row := container.NewVBox(container.NewBorder(nil, nil, nil, dateLabel, headline))
					if article.Description != "" {
						description := widget.NewLabel(article.Description)
						description.Wrapping = fyne.TextWrapBreak
						row.Add(description)
					}
					entries.Objects = append(entries.Objects, widget.NewSeparator(), row)
					if i < 2 {
						compact := widget.NewHyperlinkWithStyle(article.Title, &link, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
						compact.Truncation = fyne.TextTruncateEllipsis
						shortDate := canvas.NewText(date[:len(dayMonth)], hexColor(colorMuted))
						shortDate.TextSize = 12
						previewEntries.Objects = append(previewEntries.Objects,
							container.NewBorder(nil, nil, nil, container.NewCenter(shortDate), flushText(compact)))
					}
				}
				entries.Refresh()
				scroll.ScrollToTop()
				if len(articles) == 0 {
					setStatus(tr("No news has been published."))
				} else {
					status.Hide()
					previewStatusRow.Hide()
				}
				previewEntries.Refresh()
				previewPanel.Refresh()
			})
		}()
	}
	header := container.NewBorder(nil, nil, nil, container.NewHBox(refresh, container.NewCenter(allNews)), container.NewHBox(title))
	tab = container.NewBorder(container.NewVBox(header, status), nil, nil, nil, scroll)
	return tab, previewPanel, refresh.OnTapped
}
