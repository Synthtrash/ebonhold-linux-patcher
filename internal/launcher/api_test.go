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
			io.WriteString(w, `{"success":true,"data":{"games":[{"slug":"roguelike-prod","realmlist":"realm.test","files":[]}]}}`)
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
	if err != nil || len(manifest.Data.Games) != 1 || manifest.Data.Games[0].Slug != "roguelike-prod" {
		t.Fatalf("Games() = %+v, %v", manifest, err)
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
