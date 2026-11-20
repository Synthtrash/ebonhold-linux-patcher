package launcher

import (
	"bytes"
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
	lastLogin  LoginCredentials
}

func (f *authFake) Login(_ context.Context, username, password string) (string, error) {
	f.loginCalls++
	f.lastLogin = LoginCredentials{Username: username, Password: password}
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
	called      bool
	remember    bool
	offeredSave bool
}

func (p *authPrompt) PromptLogin(_ context.Context, allowRemember bool) (LoginCredentials, error) {
	p.called = true
	p.offeredSave = allowRemember
	return LoginCredentials{Username: "manual", Password: "test-manual-secret", Remember: p.remember}, nil
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
	prompt := &authPrompt{remember: true}
	api := &authFake{loginToken: "new", gamesErr: map[string]error{"expired": &HTTPError{StatusCode: http.StatusUnauthorized}}}
	token, _, err := (&Authenticator{API: api, Store: s, Prompt: prompt, ReadOnly: true}).Authenticate(context.Background(), false)
	if err != nil || token != "new" || !prompt.called || prompt.offeredSave {
		t.Fatalf("read-only login: %q, %v, prompted=%t, offered save=%t", token, err, prompt.called, prompt.offeredSave)
	}
	if cached, err := s.Token(); err != nil || cached != "expired" {
		t.Fatalf("read-only auth replaced the cached token: %q, %v", cached, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("read-only auth removed the old password: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, savedLoginFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only sign-in wrote a saved password: %v", err)
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
	if _, err := os.Stat(filepath.Join(s.Root, savedLoginFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sign-in without consent wrote a saved password: %v", err)
	}
}

func TestSignInWithoutManifestDoesNotSaveSession(t *testing.T) {
	store := &CredentialStore{Root: t.TempDir()}
	api := &authFake{loginToken: "fresh-token", gamesErr: map[string]error{"fresh-token": &HTTPError{StatusCode: http.StatusServiceUnavailable}}}

	_, _, err := (&Authenticator{API: api, Store: store, Prompt: &authPrompt{remember: true}}).Authenticate(context.Background(), false)
	if err == nil {
		t.Fatal("reported a successful sign-in without the manifest")
	}
	if cached, err := store.Token(); err != nil || cached != "" {
		t.Fatalf("failed sign-in saved a token: %q, %v", cached, err)
	}
	if _, err := os.Stat(filepath.Join(store.Root, savedLoginFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed sign-in wrote a saved password: %v", err)
	}
}

func TestRememberedLoginRenewsSessionsAndForgets(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	state := filepath.Join(t.TempDir(), "state")
	t.Setenv("XDG_STATE_HOME", state)
	store := &CredentialStore{Root: root}
	api := &authFake{
		loginToken: "first-session",
		gamesErr:   make(map[string]error),
	}
	const password = "synthetic $ p@ssword "
	var output strings.Builder
	console := NewConsole(strings.NewReader("example\n"+password+"\nyes\n"), io.Discard, &output, true, false)
	if err := RunCLI(ctx, api, root, []string{"--status"}, console); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, savedLoginFile)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 || !ownedByCurrentUser(info) {
		t.Fatalf("saved credential not owner-only: %v, %v", info, err)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte(password)) || bytes.Contains(stored, []byte("example")) {
		t.Fatal("saved login contains plaintext credentials")
	}
	keyDir := filepath.Join(state, savedLoginStateDir)
	keyDirInfo, err := os.Stat(keyDir)
	if err != nil || keyDirInfo.Mode().Perm() != 0700 || !ownedByCurrentUser(keyDirInfo) {
		t.Fatalf("key directory not owner-only: %v, %v", keyDirInfo, err)
	}
	keyInfo, err := os.Stat(filepath.Join(keyDir, savedLoginKeyFile))
	if err != nil || keyInfo.Mode().Perm() != 0600 || !ownedByCurrentUser(keyInfo) || keyInfo.Size() != 32 {
		t.Fatalf("key is not a private 32-byte file: %v, %v", keyInfo, err)
	}
	if strings.Contains(output.String(), password) {
		t.Fatal("the CLI printed the password")
	}

	api.gamesErr["first-session"] = &HTTPError{StatusCode: http.StatusUnauthorized}
	api.loginToken = "renewed-session"
	if _, _, err := (&Authenticator{API: api, Store: store, ReadOnly: true}).Authenticate(ctx, false); err == nil || api.loginCalls != 1 {
		t.Fatalf("background check used more than the cached token: login calls=%d, err=%v", api.loginCalls, err)
	}
	prompt := &authPrompt{}
	token, _, err := (&Authenticator{API: api, Store: store, Prompt: prompt, ReadOnly: true}).Authenticate(ctx, false)
	if err != nil || token != "renewed-session" || prompt.called {
		t.Fatalf("read-only renewal token=%q, prompted=%t, err=%v", token, prompt.called, err)
	}
	if cached, err := store.Token(); err != nil || cached != "first-session" {
		t.Fatalf("read-only renewal rewrote the cache: %q, %v", cached, err)
	}

	headless := NewConsole(strings.NewReader(""), io.Discard, io.Discard, false, false)
	if err := RunCLI(ctx, api, root, []string{"--status"}, headless); err != nil {
		t.Fatal(err)
	}
	if api.lastLogin.Username != "example" || api.lastLogin.Password != password || api.loginCalls != 3 {
		t.Fatal("renewal did not reuse the correct saved credentials exactly once per attempt")
	}
	if cached, err := store.Token(); err != nil || cached != "renewed-session" {
		t.Fatalf("renewed token was not cached: %q, %v", cached, err)
	}
	if err := RunCLI(ctx, api, root, []string{"--forget-login"}, headless); err != nil {
		t.Fatal(err)
	}
	if api.loginCalls != 3 {
		t.Fatal("forgetting the password contacted the login service")
	}
	for _, name := range []string{savedLoginFile, tokenFile} {
		if _, err := os.Lstat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s remains after forgetting: %v", name, err)
		}
	}
}

func TestRememberedLoginFallsBackWhenItCannotDecrypt(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	store := &CredentialStore{Root: t.TempDir()}
	login := &Authenticator{API: &authFake{loginToken: "saved-session"}, Store: store, Prompt: &authPrompt{remember: true}}
	if _, _, err := login.Authenticate(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	sealed, err := os.ReadFile(filepath.Join(store.Root, savedLoginFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"game folder copied without key", "modified ciphertext", "plaintext", "group-readable key"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			data := sealed
			switch kind {
			case "game folder copied without key":
				t.Setenv("XDG_STATE_HOME", t.TempDir())
			case "modified ciphertext":
				data = bytes.Clone(sealed)
				data[len(data)-1] ^= 1
			case "plaintext":
				data = []byte(`{"username":"old","password":"synthetic-plaintext"}`)
			case "group-readable key":
				path := filepath.Join(state, savedLoginStateDir, savedLoginKeyFile)
				if err := os.Chmod(path, 0640); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Chmod(path, 0600); err != nil {
						t.Error(err)
					}
				})
			}
			if err := os.WriteFile(filepath.Join(root, savedLoginFile), data, 0600); err != nil {
				t.Fatal(err)
			}
			api := &authFake{loginToken: "manual-session"}
			prompt := &authPrompt{}
			a := &Authenticator{API: api, Store: &CredentialStore{Root: root}, Prompt: prompt, ReadOnly: true}
			token, _, err := a.Authenticate(t.Context(), false)
			if err != nil || token != "manual-session" || !prompt.called || api.loginCalls != 1 || a.Warning == "" {
				t.Fatalf("expected one manual sign-in and a saved-login warning: prompt=%t, calls=%d, token=%q, err=%v", prompt.called, api.loginCalls, token, err)
			}
			if kind == "game folder copied without key" {
				if _, err := os.Stat(filepath.Join(os.Getenv("XDG_STATE_HOME"), savedLoginStateDir)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("reading a copied login created launcher state: %v", err)
				}
			}
		})
	}
}

func TestRememberedLoginKeepsKeyOutOfGameFolder(t *testing.T) {
	store := &CredentialStore{Root: t.TempDir()}
	alias := filepath.Join(t.TempDir(), "state")
	if err := os.Symlink(store.Root, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", alias)
	a := &Authenticator{API: &authFake{loginToken: "session"}, Store: store, Prompt: &authPrompt{remember: true}}
	token, _, err := a.Authenticate(t.Context(), false)
	if err != nil || token != "session" || a.Warning == "" {
		t.Fatalf("unsafe launcher state should fail to remember but keep the session: token=%q, err=%v", token, err)
	}
	for _, name := range []string{savedLoginFile, savedLoginStateDir} {
		if _, err := os.Lstat(filepath.Join(store.Root, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s written to game folder: %v", name, err)
		}
	}
}

func TestRememberedLoginRejectsUnsafeFiles(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	const saved = `{"username":"outside","password":"synthetic-saved-secret"}`
	for _, kind := range []string{"symlink", "hardlink", "readable by group", "FIFO"} {
		t.Run(kind, func(t *testing.T) {
			store := &CredentialStore{Root: t.TempDir()}
			path := filepath.Join(store.Root, savedLoginFile)
			outside := filepath.Join(t.TempDir(), "outside")
			mustWrite(t, outside, []byte(saved))
			if err := os.Chmod(outside, 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(outside, path)
			case "hardlink":
				err = os.Link(outside, path)
			case "readable by group":
				err = os.WriteFile(path, []byte(saved), 0640)
			case "FIFO":
				err = unix.Mkfifo(path, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			api := &authFake{loginToken: "manual-session"}
			a := &Authenticator{API: api, Store: store, Prompt: &authPrompt{remember: true}}
			token, _, err := a.Authenticate(t.Context(), false)
			if err != nil || token != "manual-session" || api.loginCalls != 1 || api.lastLogin.Username != "manual" {
				t.Fatalf("unsafe login did not fall back to manual sign-in: token=%q, err=%v", token, err)
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatal("unsafe credential path was changed")
			}
			if !strings.Contains(a.Warning, "Could not save the launcher password") {
				t.Fatal("the user was not told that remembering failed")
			}
			if got, err := os.ReadFile(outside); err != nil || string(got) != saved {
				t.Fatal("an external credential was changed")
			}
		})
	}
}

func TestReloginWithoutRememberingDiscardsOldPassword(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, savedLoginFile)
	if err := os.WriteFile(path, []byte(`{"username":"old","password":"synthetic-old-secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	seedToken(t, root, "old-token")
	api := &authFake{loginToken: "new-token"}
	a := &Authenticator{API: api, Store: &CredentialStore{Root: root}, Prompt: &authPrompt{}}
	token, _, err := a.Authenticate(t.Context(), true)
	if err != nil || token != "new-token" || api.loginCalls != 1 || api.lastLogin.Username != "manual" {
		t.Fatalf("relogin did not use the newly entered credentials: token=%q, err=%v", token, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("opting out left the old password: %v", err)
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
