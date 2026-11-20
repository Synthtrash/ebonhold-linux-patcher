package ui

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/synthtrash/ebonhold-linux-patcher/internal/launcher"
)

func tapButton(t *testing.T, content fyne.CanvasObject, text string) {
	t.Helper()
	button, ok := findControl(content, func(button *widget.Button) bool { return button.Text == text })
	if !ok || button.Disabled() {
		t.Fatalf("%q is not available", text)
	}
	test.Tap(button)
}

type playTestAPI struct {
	windowTestAPI
	manifestRequests atomic.Int32
}

const playTestPatch = "updated patch"

func (a *playTestAPI) Games(ctx context.Context, token string) (launcher.Manifest, error) {
	a.manifestRequests.Add(1)
	return a.windowTestAPI.Games(ctx, token)
}

func (*playTestAPI) DownloadURL(context.Context, string, string) (string, error) {
	return "https://files.example/patch", nil
}

func (*playTestAPI) Download(_ context.Context, _ string, dst io.Writer) error {
	_, err := io.WriteString(dst, playTestPatch)
	return err
}

func TestSetupEnablesPlayAfterChoosingABottle(t *testing.T) {
	const patch = "Data/patch-test.MPQ"
	dir := t.TempDir()
	marker := filepath.Join(dir, "launched")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LAUNCH_TEST_MARKER", marker)
	if err := os.WriteFile(filepath.Join(dir, "bottles-cli"), []byte(`#!/bin/sh
set -eu
case "$1" in
--json) printf '%s\n' '{"Test bottle":{}}' ;;
run)
    if [ "${LAUNCH_TEST_NO_DISPLAY:-}" = "1" ]; then
        printf '\033[31m%s\033[0m\n' '011c:err:winediag:nodrv_CreateWindow L"Make sure that your X server is running and that $DISPLAY is set correctly."' >&2
        printf '%s\n' 'game path=/home/private/secret token=private-fixture' >&2
        exit 0
    fi
    {
        printf '%s\n' "$@"
        if [ -f Data/patch-test.MPQ ]; then
            cat Data/patch-test.MPQ
        else
            printf 'patch missing'
        fi
    } > "$LAUNCH_TEST_MARKER.tmp"
    mv "$LAUNCH_TEST_MARKER.tmp" "$LAUNCH_TEST_MARKER"
    ;;
*) exit 1 ;;
esac
`), 0700); err != nil {
		t.Fatal(err)
	}
	hash := md5.Sum([]byte(playTestPatch))
	api := &playTestAPI{}
	api.token = "test-token"
	api.manifest = launcher.Manifest{Success: true, Data: launcher.ManifestData{Games: []launcher.Game{{
		Slug: "roguelike-prod", Files: []launcher.ManifestFile{{ID: "1", Path: patch, Hash: base64.StdEncoding.EncodeToString(hash[:])}},
	}}}}
	ticks := make(chan time.Time, 1)
	w, driver, root := openTestWindowWithToken(t, api, ticks, api.token, func(context.Context) ([]launcher.NewsArticle, error) {
		return []launcher.NewsArticle{
			{ID: 2, Title: "Realm update"},
			{ID: 1, Title: "Release notes"},
		}, nil
	})
	driver.until(t, "Home launch action after sign-in", func() bool {
		_, found := findControl(w.Content(), func(button *widget.Button) bool { return button.Text == "Set up launch" })
		return found
	})
	tapButton(t, w.Content(), "Set up launch")
	tapButton(t, w.Content(), "Steam")
	tapButton(t, w.Content(), "Bottles (native)")
	tapButton(t, w.Content(), "Home")
	if _, found := findControl(w.Content(), func(label *widget.Label) bool { return label.Text == "Bottle name needed" }); !found {
		t.Fatal("Home does not explain the missing Bottles target")
	}
	tapButton(t, w.Content(), "Set up launch")
	tapButton(t, w.Content(), "Continue")
	if _, found := findControl(w.Content(), func(label *widget.Label) bool { return label.Text == "One more step before you can play" }); !found {
		t.Fatal("Setup does not explain why Play is unavailable")
	}
	if _, found := findControl(w.Content(), func(*widget.Accordion) bool { return true }); found {
		t.Fatal("Setup shows advanced launch commands before the bottle is selected")
	}
	tapButton(t, w.Content(), "Choose a bottle…")
	popup := w.Canvas().Overlays().Top()
	form, ok := findControl(popup, func(*widget.Form) bool { return true })
	if !ok {
		t.Fatal("launch target form is missing")
	}
	field := slices.IndexFunc(form.Items, func(item *widget.FormItem) bool { return item.Text == "Bottle name" })
	if field < 0 {
		t.Fatal("Bottles selection did not open a bottle name field")
	}
	picker := form.Items[field].Widget.(*widget.SelectEntry)
	driver.until(t, "discovered bottles", func() bool { return picker.PlaceHolder == "Select or type a bottle name" })
	test.Tap(picker.ActionItem.(*widget.Button))
	menu, ok := w.Canvas().Focused().(*widget.PopUpMenu)
	if !ok {
		t.Fatal("detected bottles are not selectable")
	}
	menu.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	menu.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if picker.Text != "Test bottle" {
		t.Fatalf("choosing the discovered bottle selected %q", picker.Text)
	}
	tapButton(t, popup, "Save")
	if _, ok := findControl(w.Content(), func(label *widget.Label) bool {
		return label.Text == "Bottles (native) · Test bottle"
	}); !ok {
		t.Fatal("review does not show the saved Bottles target")
	}
	tapButton(t, w.Content(), "Go to Home")
	if _, found := findControl(w.Content(), func(button *widget.Button) bool { return button.Text == "Set up launch" }); found {
		t.Fatal("Home still asks for launch setup after selecting a bottle")
	}
	tapButton(t, w.Content(), "Settings")
	settings := w.Canvas().Overlays().Top()
	flags, ok := findControl(settings, func(entry *widget.Entry) bool { return entry.PlaceHolder == "-windowed" })
	if !ok {
		t.Fatal("Bottles launch arguments are missing from Settings")
	}
	flags.SetText("-windowed\n--name=two words")
	tapButton(t, settings, "Save")

	expectLaunch := func(patchState string) {
		t.Helper()
		want := "run\n--bottle=Test bottle\n--executable=" + filepath.Join(root, "Wow.exe") + "\n--\n-windowed\n--name=two words\n" + patchState
		for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			args, err := os.ReadFile(marker)
			if err == nil {
				if string(args) != want {
					t.Fatalf("Bottles received %q, want %q", args, want)
				}
				if err := os.Remove(marker); err != nil {
					t.Fatal(err)
				}
				return
			}
			if !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
		t.Fatal("Bottles did not launch")
	}

	token := filepath.Join(root, ".updaterToken")
	if err := os.Remove(token); err != nil {
		t.Fatal(err)
	}
	ticks <- time.Now()
	driver.until(t, "updater signed out", func() bool {
		_, found := findControl(w.Content(), func(button *widget.Button) bool { return button.Text == "Sign in" })
		return found
	})
	driver.until(t, "both Home news headlines loaded", func() bool {
		_, found := findControl(w.Content(), func(link *widget.Hyperlink) bool { return link.Text == "Release notes" })
		return found
	})
	desktopSize := w.Canvas().Size()
	w.Resize(fyne.NewSize(710, 382))
	play, ok := findControl(w.Content(), func(button *widget.Button) bool { return button.Text == "Play" })
	if !ok {
		t.Fatal("Play is missing on Home")
	}
	test.TapCanvas(w.Canvas(), driver.AbsolutePositionForObject(play).AddXY(play.Size().Width/2, play.Size().Height/2))
	expectLaunch("patch missing")
	w.Resize(desktopSize)
	if got := api.manifestRequests.Load(); got != 0 {
		t.Fatalf("Play made %d manifest requests with update checks off", got)
	}

	t.Setenv("LAUNCH_TEST_NO_DISPLAY", "1")
	tapButton(t, w.Content(), "Play")
	driver.until(t, "Wine launch failure even though Bottles returned zero", func() bool {
		_, found := findControl(w.Canvas().Overlays().Top(), func(label *widget.Label) bool {
			return label.Text == "Wine could not open the X11 display."
		})
		return found
	})
	tapButton(t, w.Canvas().Overlays().Top(), "Copy details")
	details := w.Clipboard().Content()
	if !strings.Contains(details, "Wine could not open the X11 display.") ||
		!strings.Contains(details, "Desktop session:") || !strings.Contains(details, "Game app: Bottles (native)") ||
		strings.Contains(details, "/home/private/secret") || strings.Contains(details, "private-fixture") || strings.Contains(details, "nodrv_CreateWindow") {
		t.Fatal("copied launch details are incomplete or included private log output")
	}
	tapButton(t, w.Canvas().Overlays().Top(), "View local output")
	if _, found := findControl(w.Canvas().Overlays().Top(), func(entry *widget.Entry) bool {
		return strings.Contains(entry.Text, "nodrv_CreateWindow") && !strings.ContainsRune(entry.Text, '\x1b')
	}); !found {
		t.Fatal("local launch output is missing or retains terminal escapes")
	}
	tapButton(t, w.Canvas().Overlays().Top(), "Close")
	tapButton(t, w.Canvas().Overlays().Top(), "Close")
	t.Setenv("LAUNCH_TEST_NO_DISPLAY", "")

	if err := os.WriteFile(token, []byte(api.token), 0600); err != nil {
		t.Fatal(err)
	}
	ticks <- time.Now()
	driver.until(t, "updater signed in", func() bool {
		_, found := findControl(w.Content(), func(button *widget.Button) bool { return button.Text == "Update files" })
		return found
	})
	tapButton(t, w.Content(), "Settings")
	settings = w.Canvas().Overlays().Top()
	check, ok := findControl(settings, func(check *widget.Check) bool { return check.Text == "Check for updates before Play" })
	if !ok {
		t.Fatal("optional update check is missing from Settings")
	}
	test.Tap(check)
	tapButton(t, settings, "Save")
	tapButton(t, w.Content(), "Setup")
	tapButton(t, w.Content(), "Continue")
	tapButton(t, w.Content(), "Continue")
	tapButton(t, w.Content(), "Test launch")
	expectLaunch("patch missing")
	if got := api.manifestRequests.Load(); got != 0 {
		t.Fatalf("Test launch made %d manifest requests with update-before-Play enabled", got)
	}
	tapButton(t, w.Content(), "Go to Home")
	tapButton(t, w.Content(), "Play")
	driver.until(t, "update and launch handoff", func() bool {
		_, found := findControl(w.Content(), func(label *widget.Label) bool {
			return label.Text == "Launch requested from Bottles (native).\nGame cache refreshed."
		})
		return found
	})
	expectLaunch(playTestPatch)
}

func TestManualPTRSetupPointsToInstall(t *testing.T) {
	api := &windowTestAPI{statusTestAPI: statusTestAPI{token: "fixture-session"}}
	w, driver, production := openTestWindowWithToken(t, api, nil, "", nil)
	ptr := filepath.Join(t.TempDir(), "PTR")
	if err := launcher.CopyClient(context.Background(), production, ptr); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ptr, ".updaterToken"), []byte(api.token), 0600); err != nil {
		t.Fatal(err)
	}
	profiles, ok := findControl(w.Content(), func(s *widget.Select) bool {
		return slices.Contains(s.Options, "Public test realm (PTR)")
	})
	if !ok {
		t.Fatal("game profiles are unavailable")
	}
	profiles.SetSelected("Public test realm (PTR)")
	tapButton(t, w.Content(), "Setup")
	folder, ok := findControl(w.Content(), func(e *widget.Entry) bool {
		return strings.Contains(e.PlaceHolder, "PTR game folder")
	})
	if !ok {
		t.Fatal("PTR folder is unavailable in Setup")
	}
	folder.SetText(ptr)
	tapButton(t, w.Content(), "Continue")
	tapButton(t, w.Content(), "Continue")
	if _, ok := findControl(w.Content(), func(label *widget.Label) bool {
		return strings.Contains(label.Text, "choose Install PTR") && strings.Contains(label.Text, "usual app")
	}); !ok {
		t.Fatal("Setup did not point to first-time PTR installation before manual launch")
	}
	tapButton(t, w.Content(), "Go to Home")
	driver.until(t, "enabled Install PTR action", func() bool {
		install, ok := findControl(w.Content(), func(b *widget.Button) bool { return b.Text == "Install PTR" })
		return ok && !install.Disabled()
	})
}

func TestPTRCopyRecreatesDeletedFolderAndShowsProgress(t *testing.T) {
	const token = "fixture-session"
	ticks := make(chan time.Time, 1)
	w, driver, production := openTestWindowWithToken(t, &windowTestAPI{statusTestAPI: statusTestAPI{token: token}}, ticks, token, nil)
	profiles, ok := findControl(w.Content(), func(s *widget.Select) bool {
		return slices.Contains(s.Options, "Public test realm (PTR)")
	})
	if !ok {
		t.Fatal("game profiles are unavailable")
	}
	profiles.SetSelected("Public test realm (PTR)")
	tapButton(t, w.Content(), "Setup")
	folder, ok := findControl(w.Content(), func(e *widget.Entry) bool {
		return strings.Contains(e.PlaceHolder, "PTR game folder")
	})
	if !ok {
		t.Fatal("PTR folder is unavailable in Setup")
	}
	destination := folder.Text
	tapButton(t, w.Content(), "Continue")
	tapButton(t, w.Content(), "Home")
	if err := os.Rename(destination, filepath.Join(t.TempDir(), "deleted-PTR")); err != nil {
		t.Fatal(err)
	}
	tapButton(t, w.Content(), "Setup")
	tapButton(t, w.Content(), "Create PTR copy…")
	form, ok := findControl(w.Canvas().Overlays().Top(), func(f *widget.Form) bool { return true })
	if !ok {
		t.Fatal("PTR destination form is unavailable")
	}
	row := slices.IndexFunc(form.Items, func(item *widget.FormItem) bool { return item.Text == "PTR folder" })
	if row < 0 {
		t.Fatal("PTR destination field is unavailable")
	}
	if got := form.Items[row].Widget.(*widget.Entry).Text; got != destination {
		t.Fatalf("suggested copy destination = %q, want the previous PTR folder %q", got, destination)
	}
	requireActivity := func(text string) {
		t.Helper()
		if _, ok := findControl(w.Content(), func(label *widget.Label) bool { return label.Text == text }); !ok {
			t.Fatalf("missing progress message %q", text)
		}
		if _, ok := findControl(w.Content(), (*widget.ProgressBarInfinite).Running); !ok {
			t.Fatal("PTR activity bar is not running")
		}
	}
	tapButton(t, w.Canvas().Overlays().Top(), "Check space")
	requireActivity("Checking space for the PTR copy…")
	driver.until(t, "copy confirmation", func() bool {
		_, ok := findControl(w.Canvas().Overlays().Top(), func(b *widget.Button) bool { return b.Text == "Create copy" })
		return ok
	})
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("the space check created the destination: %v", err)
	}
	tapButton(t, w.Canvas().Overlays().Top(), "Create copy")
	requireActivity("Copying game files for PTR…")
	tapButton(t, w.Content(), "News")
	requireActivity("Copying game files for PTR…")
	driver.until(t, "copy completion", func() bool {
		_, active := findControl(w.Content(), (*widget.ProgressBarInfinite).Running)
		return !active
	})
	if _, ok := findControl(w.Content(), func(*widget.ProgressBarInfinite) bool { return true }); ok {
		t.Fatal("copy activity stayed visible after completion")
	}
	tapButton(t, w.Content(), "Setup")
	if _, ok := findControl(w.Content(), func(label *widget.Label) bool {
		return strings.Contains(label.Text, "Install PTR from Home before playing")
	}); !ok {
		t.Fatal("Setup no longer shows the next step after copying")
	}
	prefsStore, err := launcher.NewPreferencesStore()
	if err != nil {
		t.Fatal(err)
	}
	prefs, err := prefsStore.Load()
	if err != nil || prefs.ProductionRoot != production || prefs.PTRRoot != destination || !launcher.PTRNeedsInstall(destination) {
		t.Fatalf("invalid preferences after copy: %+v, %v", prefs, err)
	}
	if _, err := os.Stat(filepath.Join(destination, ".updaterToken")); !os.IsNotExist(err) {
		t.Fatalf("PTR inherited Production's session: %v", err)
	}
	if err := os.WriteFile(filepath.Join(destination, ".updaterToken"), []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	tapButton(t, w.Content(), "Home")
	ticks <- time.Now()
	driver.until(t, "enabled Install PTR action after recreating the folder", func() bool {
		install, ok := findControl(w.Content(), func(b *widget.Button) bool { return b.Text == "Install PTR" })
		return ok && !install.Disabled()
	})
}

func TestSettingsAppliesLauncherLanguageOnSave(t *testing.T) {
	w, _ := openTestWindow(t, &windowTestAPI{}, nil)
	profiles, ok := findControl(w.Content(), func(s *widget.Select) bool {
		return slices.Contains(s.Options, "Public test realm (PTR)")
	})
	if !ok {
		t.Fatal("game profiles are unavailable")
	}
	profiles.SetSelected("Public test realm (PTR)")
	tapButton(t, w.Content(), "Settings")
	settings := w.Canvas().Overlays().Top()
	pick, ok := findControl(settings, func(s *widget.Select) bool {
		return slices.Equal(s.Options, []string{"English", "Français"})
	})
	if !ok {
		t.Fatal("interface language is unavailable")
	}
	pick.SetSelected("Français")
	tapButton(t, settings, "Game")
	gameLanguage, ok := findControl(settings, func(s *widget.Select) bool {
		return slices.Contains(s.Options, "Leave unchanged")
	})
	if !ok {
		t.Fatal("game language is unavailable")
	}
	gameLanguage.SetSelected("Español")
	tapButton(t, settings, "Save")
	if _, ok := findControl(w.Content(), func(b *widget.Button) bool { return b.Text == "Accueil" }); !ok {
		t.Fatal("the open window did not switch to French after Save")
	}
	prefsStore, err := launcher.NewPreferencesStore()
	if err != nil {
		t.Fatal(err)
	}
	prefs, err := prefsStore.Load()
	if err != nil || prefs.LauncherLanguage != "fr" || prefs.GameSlug != "roguelike" ||
		prefs.PTRSettings.Language != "esES" || prefs.ProductionSettings.Language != "" {
		t.Fatalf("saved interface language and profile settings = %+v, %v", prefs, err)
	}
}

type catalogTestAPI struct{ windowTestAPI }

func (a *catalogTestAPI) Addons(_ context.Context, token string) ([]launcher.Addon, error) {
	if token != a.token {
		return nil, &launcher.HTTPError{StatusCode: 401}
	}
	return []launcher.Addon{{ID: 209, Name: "ElvUI"}, {ID: 210, Name: "Another add-on"}}, nil
}

func TestAddonCatalogActionFollowsProfileSignIn(t *testing.T) {
	api := &catalogTestAPI{windowTestAPI: windowTestAPI{statusTestAPI: statusTestAPI{token: "test-token"}}}
	w, driver, _ := openTestWindowWithToken(t, api, nil, api.token, nil)
	tapButton(t, w.Content(), "Add-ons")
	driver.until(t, "the whole signed-in catalog", func() bool {
		_, ok := findControl(w.Content(), func(group *widget.CheckGroup) bool {
			return slices.Equal(group.Options, []string{"ElvUI · ID 209", "Another add-on · ID 210"})
		})
		return ok
	})
	tapButton(t, w.Content(), "Refresh catalog")
	if _, found := findControl(w.Content(), func(button *widget.Button) bool {
		return strings.Contains(button.Text, "Sign in") || button.Text == "Check installed"
	}); found {
		t.Fatal("signed-in Add-ons view has a redundant action")
	}
	tapButton(t, w.Content(), "Setup")
	profile, ok := findControl(w.Content(), func(selectBox *widget.Select) bool { return selectBox.Selected == "Production realm" })
	if !ok {
		t.Fatal("Setup profile selector is missing")
	}
	profile.SetSelected("Public test realm (PTR)")
	tapButton(t, w.Content(), "Add-ons")
	driver.until(t, "sign-in action for the other profile", func() bool {
		_, ok := findControl(w.Content(), func(button *widget.Button) bool { return button.Text == "Sign in" })
		return ok
	})
}
