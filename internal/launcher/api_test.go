package launcher

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPAPIAuthAndManifestRequests(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/login":
			if r.Method != http.MethodPost || r.Header.Get("X-Client-Id") != clientID || r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("unexpected login request: %s %s, headers=%v", r.Method, r.URL.Path, r.Header)
			}
			var body struct {
				Username   string `json:"username"`
				Password   string `json:"password"`
				RememberMe bool   `json:"rememberMe"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode login body: %v", err)
			}
			if body.Username != "player" || body.Password != "password" || !body.RememberMe {
				t.Errorf("login body = %+v", body)
			}
			io.WriteString(w, `{"success":true,"token":"session-token"}`)
		case "/api/launcher/games":
			if r.Header.Get("Authorization") != "Bearer session-token" || r.Header.Get("Origin") != origin || r.Header.Get("Referer") != referer {
				t.Errorf("missing manifest request headers: %v", r.Header)
			}
			io.WriteString(w, `{"success":true,"data":{"common":{"files":[{"id":42,"file_size_bytes":"123"},{"id":"43","file_size_bytes":456}]},"games":[{"slug":"roguelike-prod","realmlist":"realm.test","files":[]}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	api, err := NewHTTPAPI(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	token, err := api.Login(context.Background(), "player", "password")
	if err != nil || token != "session-token" {
		t.Fatalf("Login() = %q, %v", token, err)
	}
	manifest, err := api.Games(context.Background(), token)
	if err != nil || len(manifest.Data.Common.Files) != 2 || manifest.Data.Common.Files[0].ID != "42" || manifest.Data.Common.Files[1].ID != "43" || len(manifest.Data.Games) != 1 || manifest.Data.Games[0].Slug != "roguelike-prod" {
		t.Fatalf("Games() = %+v, %v", manifest, err)
	}
}
func TestHTTPAPIInvalidJSONReportsSafeResponseMetadata(t *testing.T) {
	const body = "<html>private server detail</html>"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()

	api, err := NewHTTPAPI(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = api.Games(context.Background(), "test-token")
	if err == nil {
		t.Fatal("Games() accepted a non-JSON manifest response")
	}
	for _, want := range []string{"HTTP 200", `Content-Type "text/html; charset=utf-8"`, "invalid character '<'"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Games() error = %v, want it to contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "private server detail") {
		t.Fatalf("Games() exposed response body: %v", err)
	}
}

func TestHTTPAPIRejectsPlainHTTPAndRedactsBearerToken(t *testing.T) {
	if _, err := NewHTTPAPI("http://example.test", nil); err == nil {
		t.Fatal("NewHTTPAPI accepted an insecure base URL")
	}
	const token = "secret-bearer-token"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"message":"expired `+token+`"}`)
	}))
	defer server.Close()
	api, err := NewHTTPAPI(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = api.Games(context.Background(), token)
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("Games() error leaked token: %v", err)
	}
}

func TestHTTPAPIDoesNotFollowRedirectToHTTP(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetCalls.Add(1)
	}))
	defer target.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer source.Close()
	api, err := NewHTTPAPI(source.URL, source.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := api.Download(context.Background(), source.URL, io.Discard); err == nil {
		t.Fatal("download followed an insecure redirect")
	}
	if targetCalls.Load() != 0 {
		t.Fatal("insecure redirect destination received a request")
	}
}

func TestHTTPAPIReportsTruncatedDownload(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "64")
		_, _ = io.WriteString(w, "short body")
	}))
	defer server.Close()
	api, err := NewHTTPAPI(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	err = api.Download(context.Background(), server.URL+"?signature=private-fixture", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "before all bytes arrived") || strings.Contains(err.Error(), "private-fixture") {
		t.Fatalf("expected a redacted truncated-download error, got %v", err)
	}
}

func TestHTTPAPIRejectsOversizedJSONResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Repeat(" ", int(maxAPIResponseBytes)+1))
	}))
	defer server.Close()
	api, err := NewHTTPAPI(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = api.Games(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("oversized response error = %v", err)
	}
}
func TestHTTPAPINewsUsesPublicFeedAndValidatesArticles(t *testing.T) {
	body := `{"success":true,"data":[
		{"id":1,"title":" Older ","description":"old","published":true,"createdAt":"2026-01-01T00:00:00Z"},
		{"id":2,"title":"Latest","description":"new","published":true,"publishedAt":"2026-02-01T12:00:00Z","createdAt":"2026-01-01T00:00:00Z"},
		{"id":3,"title":"Hidden","published":false,"createdAt":"2026-03-01T00:00:00Z"},
		{"id":0,"title":"Invalid ID","createdAt":"2026-03-01T00:00:00Z"},
		{"id":4,"title":"   ","createdAt":"2026-03-01T00:00:00Z"},
		{"id":"5","title":"String ID","createdAt":"2026-03-01T00:00:00Z"},
		{"id":6,"title":"Bad date","createdAt":"not-a-date"}
	]}`
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.RequestURI() != "/api/news?page=1&limit=8" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected public news request: %s %s, authorization=%q", r.Method, r.URL.RequestURI(), r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	defer server.Close()
	api, err := NewHTTPAPI(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	articles, err := api.News(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(articles) != 2 || articles[0].ID != 2 || articles[0].Title != "Latest" || articles[0].Description != "new" || !articles[0].Date.Equal(time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("News() latest article = %+v", articles)
	}
	if articles[1].ID != 1 || articles[1].Title != "Older" || !articles[1].Date.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("News() fallback article = %+v", articles)
	}

	for _, response := range []string{`{"success":false,"data":[]}`, `{"success":true,"data":null}`} {
		body = response
		if _, err := api.News(context.Background()); err == nil {
			t.Fatalf("News() accepted invalid feed: %s", response)
		}
	}
	body = `{"success":true,"data":[]}`
	articles, err = api.News(context.Background())
	if err != nil || len(articles) != 0 {
		t.Fatalf("News() empty feed = %v, %v", articles, err)
	}
}
