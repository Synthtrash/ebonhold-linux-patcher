package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
)

// NewsArticle contains the public metadata shown in the launcher's news list.
type NewsArticle struct {
	ID          int64
	Title       string
	Description string
	Date        time.Time
}

// News returns the latest public Project Ebonhold articles without authentication.
func (a *HTTPAPI) News(ctx context.Context) ([]NewsArticle, error) {
	var response struct {
		Success bool              `json:"success"`
		Data    []json.RawMessage `json:"data"`
	}
	if err := a.jsonRequest(ctx, http.MethodGet, "/api/news?page=1&limit=8", "", nil, 15*time.Second, false, &response); err != nil {
		return nil, err
	}
	if !response.Success {
		return nil, errors.New("news API reported an unsuccessful response")
	}
	if response.Data == nil {
		return nil, errors.New("news API returned an invalid article list")
	}

	articles := make([]NewsArticle, 0, len(response.Data))
	for _, raw := range response.Data {
		var row struct {
			ID          int64  `json:"id"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Published   *bool  `json:"published"`
			PublishedAt string `json:"publishedAt"`
			CreatedAt   string `json:"createdAt"`
		}
		if json.Unmarshal(raw, &row) != nil || row.ID <= 0 || row.Published != nil && !*row.Published {
			continue
		}
		title := strings.TrimSpace(row.Title)
		if title == "" || len(title) > 512 {
			continue
		}
		date, err := time.Parse(time.RFC3339, row.PublishedAt)
		if err != nil {
			date, err = time.Parse(time.RFC3339, row.CreatedAt)
		}
		if err != nil {
			continue
		}
		if len(row.Description) > 2048 {
			row.Description = ""
		}
		articles = append(articles, NewsArticle{ID: row.ID, Title: title, Description: row.Description, Date: date})
	}
	sort.SliceStable(articles, func(i, j int) bool { return articles[i].Date.After(articles[j].Date) })
	return articles, nil
}
