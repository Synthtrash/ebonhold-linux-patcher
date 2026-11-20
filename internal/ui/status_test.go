package ui

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/synthtrash/ebonhold-linux-patcher/internal/launcher"
)

type statusTestAPI struct {
	token       string
	statusToken string
	statusCalls int
}

func (a *statusTestAPI) Login(context.Context, string, string) (string, error) {
	return "", errors.New("unexpected login")
}
func (a *statusTestAPI) Games(_ context.Context, token string) (launcher.Manifest, error) {
	if token != a.token {
		return launcher.Manifest{}, &launcher.HTTPError{StatusCode: 401}
	}
	return launcher.Manifest{}, nil
}
func (a *statusTestAPI) Status(_ context.Context, token string) (launcher.ServerStatus, error) {
	a.statusCalls++
	a.statusToken = token
	if token != a.token {
		return launcher.ServerStatus{}, &launcher.HTTPError{StatusCode: 401}
	}
	return launcher.ServerStatus{Online: true, ServerName: "test"}, nil
}
func (*statusTestAPI) Addons(context.Context, string) ([]launcher.Addon, error) { return nil, nil }
func (*statusTestAPI) DownloadURL(context.Context, string, string) (string, error) {
	return "", nil
}
func (*statusTestAPI) AddonDownloads(context.Context, string, []int64) ([]launcher.AddonDownload, error) {
	return nil, nil
}
func (*statusTestAPI) Download(context.Context, string, io.Writer) error { return nil }

func TestQueryServerStatusUsesCachedAuthentication(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	root := t.TempDir()
	const token = "cached-session-token"
	if err := os.WriteFile(filepath.Join(root, ".updaterToken"), []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	api := &statusTestAPI{token: token}

	status, authenticated, err := queryServerStatus(context.Background(), api, root)
	if err != nil || !authenticated || !status.Online {
		t.Fatalf("queryServerStatus() = %+v, authenticated=%t, err=%v", status, authenticated, err)
	}
	if api.statusToken != token || api.statusCalls != 1 {
		t.Fatalf("Status token/calls = %q/%d", api.statusToken, api.statusCalls)
	}
}

func TestQueryServerStatusWithoutSavedAuthDoesNotPrompt(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	api := &statusTestAPI{token: "cached-session-token"}

	_, authenticated, err := queryServerStatus(context.Background(), api, t.TempDir())
	if err == nil || authenticated || api.statusCalls != 0 {
		t.Fatalf("queryServerStatus() authenticated=%t, calls=%d, err=%v", authenticated, api.statusCalls, err)
	}
}

type queuedDriver struct {
	fyne.Driver
	tasks chan func()
}

func (d *queuedDriver) DoFromGoroutine(fn func(), _ bool) { d.tasks <- fn }

type queuedApp struct {
	fyne.App
	driver *queuedDriver
}

func (a *queuedApp) Driver() fyne.Driver { return a.driver }

type testWindowWithClipboard struct {
	fyne.Window
	clipboard fyne.Clipboard
}

func (w *testWindowWithClipboard) Clipboard() fyne.Clipboard { return w.clipboard }

type windowTestAPI struct {
	statusTestAPI
	online   atomic.Bool
	manifest launcher.Manifest
}

func (a *windowTestAPI) Games(ctx context.Context, token string) (launcher.Manifest, error) {
	_, err := a.statusTestAPI.Games(ctx, token)
	return a.manifest, err
}

func (a *windowTestAPI) Status(_ context.Context, token string) (launcher.ServerStatus, error) {
	if token != a.token {
		return launcher.ServerStatus{}, &launcher.HTTPError{StatusCode: 401}
	}
	return launcher.ServerStatus{Online: a.online.Load(), ServerName: "test"}, nil
}

func (d *queuedDriver) until(t *testing.T, description string, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !ready() {
		select {
		case update := <-d.tasks:
			update()
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", description)
		}
	}
}

func findControl[T fyne.CanvasObject](obj fyne.CanvasObject, match func(T) bool) (T, bool) {
	var none T
	if obj == nil || !obj.Visible() {
		return none, false
	}
	if control, ok := obj.(T); ok && match(control) {
		return control, true
	}
	switch node := obj.(type) {
	case *container.Scroll:
		return findControl(node.Content, match)
	case *widget.PopUp:
		return findControl(node.Content, match)
	case *fyne.Container:
		for _, child := range node.Objects {
			if control, ok := findControl(child, match); ok {
				return control, true
			}
		}
	}
	return none, false
}

func openTestWindow(t *testing.T, api *windowTestAPI, ticks <-chan time.Time) (fyne.Window, *queuedDriver) {
	w, driver, _ := openTestWindowWithToken(t, api, ticks, api.token, nil)
	return w, driver
}

func openTestWindowWithToken(t *testing.T, api launcher.API, ticks <-chan time.Time, savedToken string, fetchNews func(context.Context) ([]launcher.NewsArticle, error)) (fyne.Window, *queuedDriver, string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	production, ptr := t.TempDir(), t.TempDir()
	for _, root := range []string{production, ptr} {
		if err := os.Mkdir(filepath.Join(root, "Data"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "Wow.exe"), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if savedToken != "" {
		if err := os.WriteFile(filepath.Join(production, ".updaterToken"), []byte(savedToken), 0600); err != nil {
			t.Fatal(err)
		}
	}
	store, err := launcher.NewPreferencesStore()
	if err != nil {
		t.Fatal(err)
	}
	prefs := launcher.Preferences{GameSlug: "roguelike-prod", ProductionRoot: production, PTRRoot: ptr}
	base := test.NewTempApp(t)
	driver := &queuedDriver{Driver: base.Driver(), tasks: make(chan func(), 16)}
	a := &queuedApp{App: base, driver: driver}
	fyne.SetCurrentApp(a)
	w := &testWindowWithClipboard{Window: test.NewTempWindow(t, nil), clipboard: test.NewClipboard()}
	t.Cleanup(setupWindow(w, api, "launcher", store, prefs, ticks, fetchNews))
	w.Show()
	return w, driver, production
}

func TestHomeSignInRestoresUpdateActions(t *testing.T) {
	t.Run("session only", func(t *testing.T) { testHomeSignIn(t, false) })
	t.Run("remembered login without a token", func(t *testing.T) { testHomeSignIn(t, true) })
}

func testHomeSignIn(t *testing.T, remember bool) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	api := &signInTestAPI{windowTestAPI: &windowTestAPI{statusTestAPI: statusTestAPI{token: "valid-token"}}}
	w, driver, root := openTestWindowWithToken(t, api, nil, "expired-token", nil)
	button := func(text string) (*widget.Button, bool) {
		return findControl(w.Content(), func(b *widget.Button) bool { return b.Text == text })
	}
	waitButton := func(text string) *widget.Button {
		var b *widget.Button
		driver.until(t, text, func() bool {
			var visible bool
			b, visible = button(text)
			return visible && !b.Disabled()
		})
		return b
	}
	signIn := waitButton("Sign in")
	waitButton("Sign out…")
	for _, label := range []string{"Update files", "Verify files", "Play"} {
		if _, visible := button(label); visible {
			t.Fatalf("%q is visible without authentication", label)
		}
	}
	test.Tap(signIn)
	var form *widget.Form
	driver.until(t, "sign-in form", func() bool {
		var visible bool
		form, visible = findControl(w.Canvas().Overlays().Top(), func(*widget.Form) bool { return true })
		return visible
	})
	var rememberPassword *widget.Check
	for _, item := range form.Items {
		switch item.Text {
		case "Username":
			item.Widget.(*widget.Entry).SetText("test-user")
		case "Password":
			item.Widget.(*widget.Entry).SetText("test-password")
		}
		if check, ok := item.Widget.(*widget.Check); ok && check.Text == "Remember launcher password" {
			rememberPassword = check
		}
	}
	if rememberPassword == nil || rememberPassword.Checked {
		t.Fatal("remember password is missing or opted in by default")
	}
	if remember {
		test.Tap(rememberPassword)
	}
	confirm, ok := findControl(w.Canvas().Overlays().Top(), func(button *widget.Button) bool { return button.Text == "Sign in" })
	if !ok {
		t.Fatal("sign-in confirmation is unavailable")
	}
	test.Tap(confirm)
	driver.until(t, "authenticated update actions", func() bool {
		_, update := button("Update files")
		_, verify := button("Verify files")
		_, setup := button("Set up launch")
		return update && verify && setup
	})
	if _, visible := button("Play"); visible {
		t.Fatal("Home offers Play without a launch target")
	}
	token, err := (&launcher.CredentialStore{Root: root}).Token()
	if err != nil || token != "valid-token" {
		t.Fatalf("saved token = %q, err=%v", token, err)
	}
	if _, visible := button("Sign in"); visible {
		t.Fatal("Sign in still visible after authentication")
	}
	profile, ok := findControl(w.Content(), func(selectBox *widget.Select) bool { return selectBox.Selected == "Production realm" })
	if !ok {
		t.Fatal("Home profile selector is missing")
	}
	profile.SetSelected("Public test realm (PTR)")
	waitButton("Sign in")
	for _, label := range []string{"Update files", "Sign out…"} {
		if _, visible := button(label); visible {
			t.Fatalf("PTR inherited %q from Production", label)
		}
	}
	saved := filepath.Join(root, ".ebonhold-saved-login")
	info, err := os.Stat(saved)
	tokenPath := filepath.Join(root, ".updaterToken")
	if remember {
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("explicit consent did not save an owner-only password: %v", err)
		}
		if err := os.Remove(tokenPath); err != nil {
			t.Fatal(err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sign-in without consent left a saved password: %v", err)
	}
	profile.SetSelected("Production realm")
	stored := tokenPath
	if remember {
		waitButton("Sign in")
		stored = saved
	} else {
		waitButton("Update files")
		if _, visible := button("Sign in"); visible {
			t.Fatal("switching to PTR forgot the Production session")
		}
	}
	store, err := launcher.NewPreferencesStore()
	if err != nil {
		t.Fatal(err)
	}
	prefs, err := store.Load()
	if err != nil || !filepath.IsAbs(prefs.PTRRoot) || prefs.PTRRoot == root {
		t.Fatalf("separate PTR folder was not saved: %v", err)
	}
	const ptrToken = "ptr-session"
	if err := os.WriteFile(filepath.Join(prefs.PTRRoot, ".updaterToken"), []byte(ptrToken), 0600); err != nil {
		t.Fatal(err)
	}
	signOut := waitButton("Sign out…")
	test.Tap(signOut)
	tapButton(t, w.Canvas().Overlays().Top(), "Cancel")
	if _, err := os.Stat(stored); err != nil {
		t.Fatalf("cancelling sign-out removed the saved login: %v", err)
	}
	test.Tap(signOut)
	tapButton(t, w.Canvas().Overlays().Top(), "Sign out")
	waitButton("Sign in")
	if _, visible := button("Sign out…"); visible {
		t.Fatal("Home still offers sign-out without a saved login")
	}
	if _, err := os.Stat(saved); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sign-out left the password in place: %v", err)
	}
	if token, err := (&launcher.CredentialStore{Root: root}).Token(); err != nil || token != "" {
		t.Fatalf("sign-out left a session token: %v", err)
	}
	if token, err := (&launcher.CredentialStore{Root: prefs.PTRRoot}).Token(); err != nil || token != ptrToken {
		t.Fatalf("signing out of Production changed the PTR session: %v", err)
	}
}

type signInTestAPI struct {
	*windowTestAPI
}

func (a *signInTestAPI) Login(_ context.Context, user, password string) (string, error) {
	if user != "test-user" || password != "test-password" {
		return "", errors.New("unexpected test credentials")
	}
	return a.token, nil
}

func TestHeaderServerStatusRefreshesOnTick(t *testing.T) {
	api := &windowTestAPI{statusTestAPI: statusTestAPI{token: "cached-session-token"}}
	api.online.Store(true)
	ticks := make(chan time.Time, 1)
	w, driver := openTestWindow(t, api, ticks)
	waitForStatus := func(text string) {
		driver.until(t, text, func() bool {
			_, ok := findControl(w.Content(), func(label *widget.Label) bool { return label.Text == text })
			return ok
		})
	}
	waitForStatus("Shared server online · test")
	tab := func(text string) *widget.Button {
		t.Helper()
		button, ok := findControl(w.Content(), func(button *widget.Button) bool { return button.Text == text })
		if !ok {
			t.Fatalf("%s tab is missing", text)
		}
		return button
	}
	home, news := tab("Home"), tab("News")
	checkNav := func() {
		t.Helper()
		left := driver.AbsolutePositionForObject(home).X
		right := driver.AbsolutePositionForObject(news).X + news.Size().Width
		_, area := w.Canvas().InteractiveArea()
		if offset := (left + right - area.Width) / 2; offset < -1 || offset > 1 {
			t.Fatalf("tab group is %v px off-center", offset)
		}
	}
	checkNav()
	w.Resize(fyne.NewSize(820, 480))
	checkNav()
	api.online.Store(false)
	ticks <- time.Now()
	waitForStatus("Shared server offline · test")
	checkNav()
}

func TestVerifyFailureKeepsFileDetailsScrollable(t *testing.T) {
	hash := md5.Sum([]byte("fixture"))
	checksum := base64.StdEncoding.EncodeToString(hash[:])
	files := make([]launcher.ManifestFile, 32)
	for i := range files {
		files[i] = launcher.ManifestFile{
			ID:   fmt.Sprint(i + 1),
			Path: fmt.Sprintf("Data/%s/patch-%02d.MPQ", strings.Repeat("long-folder-", 16), i),
			Hash: checksum,
		}
	}
	api := &windowTestAPI{
		statusTestAPI: statusTestAPI{token: "cached-session-token"},
		manifest: launcher.Manifest{Success: true, Data: launcher.ManifestData{Games: []launcher.Game{
			{Slug: "roguelike-prod", Files: files},
		}}},
	}
	w, driver, root := openTestWindowWithToken(t, api, nil, api.token, nil)
	mismatched := filepath.Join(root, files[0].Path)
	if err := os.MkdirAll(filepath.Dir(mismatched), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mismatched, []byte("outdated"), 0600); err != nil {
		t.Fatal(err)
	}
	driver.until(t, "Verify files after status check", func() bool {
		verify, found := findControl(w.Content(), func(button *widget.Button) bool { return button.Text == "Verify files" })
		return found && !verify.Disabled()
	})
	w.Resize(fyne.NewSize(820, 480))
	windowSize := w.Canvas().Size()
	verify, ok := findControl(w.Content(), func(button *widget.Button) bool { return button.Text == "Verify files" })
	if !ok || verify.Disabled() {
		t.Fatal("Verify files is not available for the selected client")
	}
	test.Tap(verify)

	var technical *widget.Button
	driver.until(t, "verification results", func() bool {
		var found bool
		technical, found = findControl(w.Content(), func(button *widget.Button) bool { return button.Text == "Technical details" })
		return found
	})
	list, ok := findControl(w.Content(), func(list *widget.List) bool { return list.Length() == len(files) })
	if !ok {
		t.Fatal("failed files are missing from the Update view")
	}
	for id, hint := range []string{"Run an update to replace this file", "Run an update to download the missing file"} {
		list.Select(id)
		if _, found := findControl(w.Content(), func(label *widget.Label) bool {
			return strings.Contains(label.Text, hint) && strings.Contains(label.Text, files[id].Path)
		}); !found {
			t.Fatalf("file %d does not show its repair instructions inline", id)
		}
		if w.Canvas().Overlays().Top() != nil {
			t.Fatalf("selecting file %d opened a modal", id)
		}
	}
	if minimum := w.Content().MinSize(); minimum.Width > windowSize.Width || minimum.Height > windowSize.Height {
		t.Fatalf("inline file inspector does not fit %v: minimum %v", windowSize, minimum)
	}
	test.Tap(technical)

	var report *widget.Entry
	driver.until(t, "failure report with every file", func() bool {
		var found bool
		report, found = findControl(w.Canvas().Overlays().Top(), func(entry *widget.Entry) bool {
			return strings.Contains(entry.Text, files[len(files)-1].Path)
		})
		return found
	})
	if after, minimum := w.Canvas().Size(), w.Content().MinSize(); after != windowSize || minimum.Width > windowSize.Width || minimum.Height > windowSize.Height {
		t.Fatalf("failed verification requires a larger window: before %v, after %v, minimum %v", windowSize, after, minimum)
	}
	if size := report.Size(); size.Width >= windowSize.Width || size.Height >= windowSize.Height {
		t.Fatalf("failure report %v does not fit within window %v", size, windowSize)
	}
	for _, object := range test.WidgetRenderer(report).Objects() {
		if viewport, ok := object.(*container.Scroll); ok && viewport.Visible() {
			before := viewport.Offset.Y
			pos := driver.AbsolutePositionForObject(report).Add(fyne.NewPos(12, 12))
			test.Scroll(w.Canvas(), pos, 0, -250)
			if viewport.Offset.Y == before {
				t.Fatal("scrolling the visible failure report did not move the file log")
			}
			return
		}
	}
	t.Fatal("failure report has no scroll viewport")
}

func TestVerifyFailureWithoutResultsDoesNotSuggestRepair(t *testing.T) {
	api := &windowTestAPI{
		statusTestAPI: statusTestAPI{token: "fixture-session"},
		manifest: launcher.Manifest{Success: true, Data: launcher.ManifestData{Games: []launcher.Game{
			{Slug: "roguelike-prod"},
		}}},
	}
	w, driver, _ := openTestWindowWithToken(t, api, nil, api.token, nil)
	driver.until(t, "signed-in Verify action", func() bool {
		verify, ok := findControl(w.Content(), func(b *widget.Button) bool { return b.Text == "Verify files" })
		return ok && !verify.Disabled()
	})
	tapButton(t, w.Content(), "Verify files")
	driver.until(t, "failed check with no verification results", func() bool {
		_, empty := findControl(w.Content(), func(l *widget.Label) bool { return l.Text == "No verification results." })
		_, failed := findControl(w.Content(), func(l *widget.Label) bool { return l.Text == "Verification failed" })
		return empty && failed
	})
	if _, ok := findControl(w.Canvas().Overlays().Top(), func(e *widget.Entry) bool {
		return strings.Contains(e.Text, "no files found in manifest for game")
	}); !ok {
		t.Fatal("the available error does not explain why verification could not start")
	}
}

func TestUpdateRejectsPendingCopyUnderWrongProfileBeforeSignIn(t *testing.T) {
	api := &windowTestAPI{statusTestAPI: statusTestAPI{token: "valid-token"}}
	w, driver, root := openTestWindowWithToken(t, api, nil, "", nil)
	if err := os.WriteFile(filepath.Join(root, ".ebonhold-ptr-needs-update"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	tapButton(t, w.Content(), "Update")
	tapButton(t, w.Content(), "Update files")
	driver.until(t, "wrong profile recovery instructions instead of a sign-in prompt", func() bool {
		_, found := findControl(w.Canvas().Overlays().Top(), func(entry *widget.Entry) bool {
			return strings.Contains(entry.Text, "Switch to the PTR profile before updating.")
		})
		return found
	})
}

type rateLimitedDownloadAPI struct{ windowTestAPI }

func (*rateLimitedDownloadAPI) DownloadURL(context.Context, string, string) (string, error) {
	return "", &launcher.HTTPError{StatusCode: 429}
}

func TestUpdateShowsWhyAFileFailed(t *testing.T) {
	hash := md5.Sum([]byte("fixture"))
	const path = "Data/patch-5.MPQ"
	api := &rateLimitedDownloadAPI{}
	api.token = "test-session"
	api.manifest = launcher.Manifest{Success: true, Data: launcher.ManifestData{Games: []launcher.Game{{
		Slug: "roguelike-prod", Files: []launcher.ManifestFile{{ID: "1", Path: path, Hash: base64.StdEncoding.EncodeToString(hash[:])}},
	}}}}
	w, driver, _ := openTestWindowWithToken(t, api, nil, api.token, nil)
	driver.until(t, "signed-in update action", func() bool {
		button, found := findControl(w.Content(), func(b *widget.Button) bool { return b.Text == "Update files" })
		return found && !button.Disabled()
	})
	tapButton(t, w.Content(), "Update files")
	driver.until(t, "download failure", func() bool {
		_, found := findControl(w.Content(), func(b *widget.Button) bool { return b.Text == "Technical details" })
		return found
	})
	files, found := findControl(w.Content(), func(list *widget.List) bool { return list.Length() == 1 })
	if !found {
		t.Fatal("failed download is missing from the file pane")
	}
	files.Select(0)
	if _, found := findControl(w.Content(), func(label *widget.Label) bool {
		return strings.Contains(label.Text, path) && strings.Contains(label.Text, "HTTP 429")
	}); !found {
		t.Error("the file inspector does not explain the HTTP 429")
	}
	tapButton(t, w.Content(), "Technical details")
	if _, found := findControl(w.Canvas().Overlays().Top(), func(entry *widget.Entry) bool {
		return strings.HasPrefix(entry.Text, "Launcher version: "+launcher.BuildVersion()+"\n\n") &&
			strings.Contains(entry.Text, path+": request download link: HTTP 429")
	}); !found {
		t.Error("the copyable report must identify the build and tie the HTTP 429 to its path")
	}
	tapButton(t, w.Canvas().Overlays().Top(), "Copy details")
	copied := w.Clipboard().Content()
	if !strings.HasPrefix(copied, "Launcher version: "+launcher.BuildVersion()+"\n\n") || !strings.Contains(copied, path+": request download link: HTTP 429") {
		t.Fatalf("copied details missing build version or failure: %q", copied)
	}
}
