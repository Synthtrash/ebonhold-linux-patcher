package launcher

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type authFake struct {
	loginToken string
	loginErr   error
	gamesErr   map[string]error
	loginCalls int
	gamesCalls int
}

func (f *authFake) Login(context.Context, string, string) (string, error) {
	f.loginCalls++
	if f.loginErr != nil && f.loginCalls == 1 {
		return f.loginToken, f.loginErr
	}
	return f.loginToken, nil
}
func (f *authFake) Games(_ context.Context, t string) (Manifest, error) {
	f.gamesCalls++
	return Manifest{Success: true}, f.gamesErr[t]
}
func (*authFake) Status(context.Context, string) (ServerStatus, error)        { return ServerStatus{}, nil }
func (*authFake) Addons(context.Context, string) ([]Addon, error)             { return nil, nil }
func (*authFake) DownloadURL(context.Context, string, string) (string, error) { return "", nil }
func (*authFake) AddonDownloads(context.Context, string, []int64) ([]AddonDownload, error) {
	return nil, nil
}
func (*authFake) Download(context.Context, string, io.Writer) error { return nil }

type authPrompt struct {
	called        bool
	remember      bool
	allowRemember bool
}

func (p *authPrompt) PromptLogin(_ context.Context, _ string, allowRemember bool) (string, string, bool, error) {
	p.called = true
	p.allowRemember = allowRemember
	return "manual", "password", p.remember, nil
}

func TestAuthenticateReusesTokenWithoutReadingCredentials(t *testing.T) {
	s := &CredentialStore{Root: t.TempDir()}
	if err := os.WriteFile(s.tokenPath(), []byte("good"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(s.credentialDir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/missing", s.credentialPath()); err != nil {
		t.Fatal(err)
	}
	api := &authFake{gamesErr: map[string]error{}}
	p := &authPrompt{}
	token, _, err := (&Authenticator{API: api, Store: s, Prompt: p}).Authenticate(context.Background(), false)
	if err != nil || token != "good" || p.called || api.loginCalls != 0 {
		t.Fatalf("token=%q err=%v prompt=%v logins=%d", token, err, p.called, api.loginCalls)
	}
}

func TestReadonlyPromptDisallowsRememberConsent(t *testing.T) {
	s := &CredentialStore{Root: t.TempDir()}
	p := &authPrompt{remember: true}
	api := &authFake{loginToken: "new", gamesErr: map[string]error{}}
	_, _, err := (&Authenticator{API: api, Store: s, Prompt: p, ReadOnly: true}).Authenticate(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if p.allowRemember {
		t.Fatal("read-only mode allowed remember consent")
	}
}

func TestCredentialPermissionsAndSymlinkRejection(t *testing.T) {
	s := &CredentialStore{Root: t.TempDir()}
	if err := s.saveCredentials(Credentials{"user", "pass"}); err != nil {
		t.Fatal(err)
	}
	di, _ := os.Stat(s.credentialDir())
	fi, _ := os.Stat(s.credentialPath())
	if di.Mode().Perm() != 0700 || fi.Mode().Perm() != 0600 {
		t.Fatalf("permissions dir=%o file=%o", di.Mode().Perm(), fi.Mode().Perm())
	}
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte(`{"username":"user","password":"pass"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s.credentialPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, s.credentialPath()); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Credentials(); ok {
		t.Fatal("accepted symlink credentials")
	}
	if err := s.saveCredentials(Credentials{"replacement", "secret"}); err == nil {
		t.Fatal("replaced symlink destination")
	}
	b, _ := os.ReadFile(victim)
	if string(b) != `{"username":"user","password":"pass"}` {
		t.Fatal("symlink victim modified")
	}
}
func TestRefreshUsesSavedCredentialsWithoutPrompt(t *testing.T) {
	s := &CredentialStore{Root: t.TempDir()}
	if err := s.saveToken("old"); err != nil {
		t.Fatal(err)
	}
	if err := s.saveCredentials(Credentials{"saved", "secret"}); err != nil {
		t.Fatal(err)
	}
	api := &authFake{loginToken: "renewed", gamesErr: map[string]error{}}
	p := &authPrompt{}
	token, _, err := (&Authenticator{API: api, Store: s, Prompt: p}).Refresh(context.Background())
	if err != nil || token != "renewed" || api.loginCalls != 1 || p.called {
		t.Fatalf("token=%q err=%v logins=%d prompt=%v", token, err, api.loginCalls, p.called)
	}
}

func TestSavedCredentialRejectionVersusTransientFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		loginErr error
		prompt   bool
	}{{"rejected", &HTTPError{StatusCode: http.StatusUnauthorized}, true}, {"transient", errors.New("network detail"), false}} {
		t.Run(tc.name, func(t *testing.T) {
			s := &CredentialStore{Root: t.TempDir()}
			if err := s.saveCredentials(Credentials{"saved", "secret"}); err != nil {
				t.Fatal(err)
			}
			api := &authFake{loginToken: "manual-token", loginErr: tc.loginErr}
			p := &authPrompt{}
			_, _, err := (&Authenticator{API: api, Store: s, Prompt: p}).Authenticate(context.Background(), false)
			if tc.prompt != p.called {
				t.Fatalf("prompt called=%v err=%v", p.called, err)
			}
			if tc.prompt && err != nil {
				t.Fatal(err)
			}
			if !tc.prompt && err == nil {
				t.Fatal("expected transient error")
			}
		})
	}
}
func TestForgetRemovesUnsafeTokenPath(t *testing.T) {
	s := &CredentialStore{Root: t.TempDir()}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, s.tokenPath()); err != nil {
		t.Fatal(err)
	}
	if err := (&Authenticator{Store: s}).Forget(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(s.tokenPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("token path remains: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "untouched" {
		t.Fatalf("symlink target changed: %q, %v", data, err)
	}
}

func TestForgetIsLocalAndRemovesState(t *testing.T) {
	s := &CredentialStore{Root: t.TempDir()}
	_ = s.saveToken("token")
	_ = s.saveCredentials(Credentials{"user", "pass"})
	api := &authFake{gamesErr: map[string]error{"token": errors.New("must not call")}}
	if err := (&Authenticator{API: api, Store: s}).Forget(); err != nil {
		t.Fatal(err)
	}
	if api.gamesCalls != 0 || api.loginCalls != 0 {
		t.Fatal("forget made network requests")
	}
	for _, path := range []string{s.tokenPath(), s.credentialPath()} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("state remains at %s", path)
		}
	}
}
func TestRememberSaveFailureWarnsWithoutDiscardingAuthentication(t *testing.T) {
	store := &CredentialStore{Root: t.TempDir()}
	outside := t.TempDir()
	if err := os.Symlink(outside, store.credentialDir()); err != nil {
		t.Fatal(err)
	}
	prompt := &authPrompt{remember: true}
	auth := &Authenticator{
		API:    &authFake{loginToken: "new-token", gamesErr: map[string]error{}},
		Store:  store,
		Prompt: prompt,
	}
	token, _, err := auth.Authenticate(context.Background(), false)
	if err != nil || token != "new-token" {
		t.Fatalf("authentication = %q, %v", token, err)
	}
	if !strings.Contains(auth.Warning, "Remember me was not saved") {
		t.Fatalf("missing storage warning: %q", auth.Warning)
	}
	if got, err := store.Token(); err != nil || got != "new-token" {
		t.Fatalf("token = %q, %v", got, err)
	}
}

func TestForgetReportsCredentialClearFailure(t *testing.T) {
	store := &CredentialStore{Root: t.TempDir()}
	if err := store.saveToken("token"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(store.credentialDir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(store.credentialPath(), 0700); err != nil {
		t.Fatal(err)
	}
	err := (&Authenticator{Store: store}).Forget()
	if err == nil || !strings.Contains(err.Error(), "Could not clear saved local credentials") {
		t.Fatalf("Forget error = %v", err)
	}
	if _, err := os.Lstat(store.tokenPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("token remains after forget: %v", err)
	}
}
func TestRememberWithUnsafeClientFolderWarnsAndKeepsSession(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	root := t.TempDir()
	if err := os.Chmod(root, 0777); err != nil {
		t.Fatal(err)
	}
	store := &CredentialStore{Root: root}
	prompt := &authPrompt{remember: true}
	auth := &Authenticator{
		API:    &authFake{loginToken: "session", gamesErr: map[string]error{}},
		Store:  store,
		Prompt: prompt,
	}
	token, _, err := auth.Authenticate(context.Background(), false)
	if err != nil || token != "session" {
		t.Fatalf("authentication = %q, %v", token, err)
	}
	if !strings.Contains(auth.Warning, "Remember me was not saved") {
		t.Fatalf("missing unsafe-folder warning: %q", auth.Warning)
	}
	if _, err := os.Lstat(store.tokenPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe client folder received a token: %v", err)
	}
}
func TestAuthLockPathConcurrentInitialization(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	root := t.TempDir()
	const callers = 32
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := authLockPath(root)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
