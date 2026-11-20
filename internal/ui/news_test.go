package ui

import (
	"context"
	"errors"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/synthtrash/ebonhold-linux-patcher/internal/launcher"
)

func TestHomeNewsShowsLatestTwoAfterRetry(t *testing.T) {
	attempts := 0
	date := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	articles := []launcher.NewsArticle{
		{ID: 24, Title: "Major Update Rollout Plan", Description: "Downtime planned for the rollout", Date: date},
		{ID: 23, Title: "Major Update: Release Date and Fresh Realm Timeline", Date: date.AddDate(0, 0, -7)},
		{ID: 22, Title: "Development Roadmap", Date: date.AddDate(0, -4, 0)},
	}
	fetch := func(context.Context) ([]launcher.NewsArticle, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("temporarily offline")
		}
		return articles, nil
	}
	api := &windowTestAPI{statusTestAPI: statusTestAPI{token: "not-installed"}}
	w, driver, _ := openTestWindowWithToken(t, api, nil, "", fetch)
	tap := func(text string) {
		t.Helper()
		button, ok := findControl(w.Content(), func(button *widget.Button) bool { return button.Text == text })
		if !ok || button.Disabled() {
			t.Fatalf("%s is not available", text)
		}
		test.Tap(button)
	}
	waitForError := func() {
		t.Helper()
		driver.until(t, "inline news load error", func() bool {
			_, visible := findControl(w.Content(), func(label *widget.Label) bool { return label.Text == "News could not be loaded." })
			return visible
		})
	}
	waitForError()
	if w.Canvas().Overlays().Top() != nil {
		t.Fatal("news requires an account or interrupts the launcher with a dialog")
	}
	tap("All news")
	waitForError()
	tap("Refresh")
	headline := func(title string) (*widget.Hyperlink, bool) {
		return findControl(w.Content(), func(link *widget.Hyperlink) bool { return link.Text == title })
	}
	var article *widget.Hyperlink
	driver.until(t, "news article and summary", func() bool {
		var latest, oldest, summary bool
		article, latest = headline(articles[0].Title)
		_, oldest = headline(articles[2].Title)
		_, summary = findControl(w.Content(), func(label *widget.Label) bool { return label.Text == articles[0].Description })
		return latest && oldest && summary
	})
	if got := article.URL.String(); got != "https://project-ebonhold.com/news/24" {
		t.Fatalf("opened article URL = %s", got)
	}
	tap("Home")
	for _, post := range articles[:2] {
		if _, ok := headline(post.Title); !ok {
			t.Errorf("Home is missing %q after refreshing news", post.Title)
		}
	}
	if _, ok := headline(articles[2].Title); ok {
		t.Errorf("Home shows an older post beyond the latest two: %q", articles[2].Title)
	}
}
