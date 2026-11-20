package launcher

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
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
	called bool
}

func (p *authPrompt) PromptLogin(context.Context) (string, string, error) {
	p.called = true
	return "manual", "password", nil
}

func writeLegacyPassword(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, legacyPasswordDir)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, legacyPasswordFile)
	if err := os.WriteFile(path, []byte(`{"username":"fixture","password":"fixture"}`), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func seedToken(t *testing.T, root, token string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, tokenFile), []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestAuthenticateKeepsCachedTokenAndRemovesOldPassword(t *testing.T) {
	s := &CredentialStore{Root: t.TempDir()}
	seedToken(t, s.Root, "good")
	path := writeLegacyPassword(t, s.Root)
	other := filepath.Join(filepath.Dir(path), "other")
	if err := os.WriteFile(other, nil, 0600); err != nil {
		t.Fatal(err)
	}
	api := &authFake{gamesErr: map[string]error{}}
	p := &authPrompt{}
	token, _, err := (&Authenticator{API: api, Store: s, Prompt: p}).Authenticate(context.Background(), false)
	if err != nil || token != "good" || p.called || api.loginCalls != 0 {
		t.Fatalf("token=%q err=%v prompt=%v logins=%d", token, err, p.called, api.loginCalls)
	}
	if cached, err := s.Token(); err != nil || cached != "good" {
		t.Fatalf("upgrade did not preserve cached token: %q, %v", cached, err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old password file remains: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("upgrade removed an unrelated file: %v", err)
	}
}

func TestAuthenticateReadOnlyLeavesSavedFilesAlone(t *testing.T) {
	s := &CredentialStore{Root: t.TempDir()}
	path := writeLegacyPassword(t, s.Root)
	seedToken(t, s.Root, "expired")
	prompt := &authPrompt{}
	api := &authFake{loginToken: "new", gamesErr: map[string]error{"expired": &HTTPError{StatusCode: http.StatusUnauthorized}}}
	token, _, err := (&Authenticator{API: api, Store: s, Prompt: prompt, ReadOnly: true}).Authenticate(context.Background(), false)
	if err != nil || token != "new" || !prompt.called {
		t.Fatalf("read-only login: %q, %v, prompted=%t", token, err, prompt.called)
	}
	if cached, err := s.Token(); err != nil || cached != "expired" {
		t.Fatalf("read-only auth replaced the cached token: %q, %v", cached, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("read-only auth removed the old password: %v", err)
	}
}

func TestAuthenticatePromptsForExpiredSessionAndSavesOnlyToken(t *testing.T) {
	s := &CredentialStore{Root: t.TempDir()}
	path := writeLegacyPassword(t, s.Root)
	seedToken(t, s.Root, "expired")
	prompt := &authPrompt{}
	api := &authFake{loginToken: "renewed", gamesErr: map[string]error{"expired": &HTTPError{StatusCode: http.StatusUnauthorized}}}
	token, _, err := (&Authenticator{API: api, Store: s, Prompt: prompt}).Authenticate(context.Background(), false)
	if err != nil || token != "renewed" || !prompt.called || api.loginCalls != 1 {
		t.Fatalf("login: %q, %v, prompted=%t, logins=%d", token, err, prompt.called, api.loginCalls)
	}
	if cached, err := s.Token(); err != nil || cached != "renewed" {
		t.Fatalf("cached token = %q, %v", cached, err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("login left a password file: %v", err)
	}
}

func TestSignInWithoutManifestDoesNotSaveSession(t *testing.T) {
	store := &CredentialStore{Root: t.TempDir()}
	api := &authFake{loginToken: "fresh-token", gamesErr: map[string]error{"fresh-token": &HTTPError{StatusCode: http.StatusServiceUnavailable}}}

	_, _, err := (&Authenticator{API: api, Store: store, Prompt: &authPrompt{}}).Authenticate(context.Background(), false)
	if err == nil {
		t.Fatal("reported a successful sign-in without the manifest")
	}
	if cached, err := store.Token(); err != nil || cached != "" {
		t.Fatalf("failed sign-in saved a token: %q, %v", cached, err)
	}
}

func TestForgetRemovesUnsafeTokenPath(t *testing.T) {
	s := &CredentialStore{Root: t.TempDir()}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(s.Root, tokenFile)); err != nil {
		t.Fatal(err)
	}
	if err := (&Authenticator{Store: s}).Forget(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(s.Root, tokenFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("token path remains: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "untouched" {
		t.Fatalf("symlink target changed: %q, %v", data, err)
	}
}

func TestForgetIsLocalAndRemovesState(t *testing.T) {
	s := &CredentialStore{Root: t.TempDir()}
	seedToken(t, s.Root, "token")
	path := writeLegacyPassword(t, s.Root)
	api := &authFake{gamesErr: map[string]error{"token": errors.New("must not call")}}
	if err := (&Authenticator{API: api, Store: s}).Forget(); err != nil {
		t.Fatal(err)
	}
	if api.gamesCalls != 0 || api.loginCalls != 0 {
		t.Fatal("forget made network requests")
	}
	for _, path := range []string{filepath.Join(s.Root, tokenFile), path} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("state remains at %s", path)
		}
	}
}

func TestLegacyPasswordCleanupDoesNotFollowLinkedFolder(t *testing.T) {
	store := &CredentialStore{Root: t.TempDir()}
	outside := writeLegacyPassword(t, t.TempDir())
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(store.Root, legacyPasswordDir)); err != nil {
		t.Fatal(err)
	}
	auth := &Authenticator{
		API:    &authFake{loginToken: "new-token"},
		Store:  store,
		Prompt: &authPrompt{},
	}
	token, _, err := auth.Authenticate(context.Background(), false)
	if err != nil || token != "new-token" || !strings.Contains(auth.Warning, "Could not remove the old saved password") {
		t.Fatalf("expected a cleanup warning with a working session, got %q, %v, %q", token, err, auth.Warning)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("cleanup removed a file outside the installation: %v", err)
	}
}

func TestForgetReportsCredentialClearFailure(t *testing.T) {
	store := &CredentialStore{Root: t.TempDir()}
	seedToken(t, store.Root, "token")
	if err := os.MkdirAll(filepath.Join(store.Root, legacyPasswordDir, legacyPasswordFile), 0700); err != nil {
		t.Fatal(err)
	}
	err := (&Authenticator{Store: store}).Forget()
	if err == nil || !strings.Contains(err.Error(), "could not remove the old saved password") {
		t.Fatalf("Forget error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(store.Root, tokenFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("token remains after forget: %v", err)
	}
}

func TestForgetDoesNotRemoveTokenDirectory(t *testing.T) {
	store := &CredentialStore{Root: t.TempDir()}
	path := filepath.Join(store.Root, tokenFile)
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := (&Authenticator{Store: store}).Forget(); err == nil {
		t.Fatal("forget accepted a directory as the cached token")
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("forget changed the directory: %v", err)
	}
}

func TestDesktopCleanupReportsUnverifiedGameFolder(t *testing.T) {
	store := &CredentialStore{Root: t.TempDir()}
	path := writeLegacyPassword(t, store.Root)
	if err := store.RemoveLegacyPassword(); err == nil || !strings.Contains(err.Error(), "Data/ and a regular Wow.exe") {
		t.Fatalf("cleanup did not report the missing game files: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("cleanup removed a password from an unverified folder: %v", err)
	}
}

func TestDesktopCleanupDoesNotWarnAboutAbsentPassword(t *testing.T) {
	store := &CredentialStore{Root: t.TempDir()}
	dir := filepath.Join(store.Root, legacyPasswordDir)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0777); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(store.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := unix.Flock(int(held.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- store.RemoveLegacyPassword() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("warned about an old password that was not present: %v", err)
		}
	case <-time.After(5 * time.Second):
		held.Close()
		<-done
		t.Fatal("cleanup waited for the authentication lock with no password to remove")
	}
}

func TestAuthenticationLockIsSharedWithoutCreatingFiles(t *testing.T) {
	path := t.TempDir()
	root, err := openInstallation(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	locked, err := lockAuthentication(root)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Close()
	other, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := unix.Flock(int(other.Fd()), unix.LOCK_EX|unix.LOCK_NB); !errors.Is(err, unix.EWOULDBLOCK) {
		t.Fatalf("another process can take the installation lock while it is held: %v", err)
	}
	if err := locked.Close(); err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(other.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatalf("installation lock did not release: %v", err)
	}
	if entries, err := os.ReadDir(path); err != nil || len(entries) != 0 {
		t.Fatalf("authentication lock wrote to the game folder: %v, %v", entries, err)
	}
}
