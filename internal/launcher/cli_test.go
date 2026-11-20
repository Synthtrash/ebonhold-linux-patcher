package launcher

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type cliManifestAPI struct {
	*updaterAPI
	manifestCalls int
	manifest      Manifest
}

func (a *cliManifestAPI) Games(context.Context, string) (Manifest, error) {
	a.manifestCalls++
	return a.manifest, nil
}

func TestParseArgsPreservesManagerArguments(t *testing.T) {
	got, err := ParseArgs([]string{
		"--quick",
		"--client-dir=/home/player/Games/Ebonhold",
		"--",
		"/usr/bin/flatpak",
		"run",
		"--game=manager-option",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Quick || got.ClientDir != "/home/player/Games/Ebonhold" {
		t.Fatalf("parsed options = %+v", got)
	}
	if strings.Join(got.GameArguments, " ") != "/usr/bin/flatpak run --game=manager-option" {
		t.Fatalf("game arguments = %q", got.GameArguments)
	}
}

func TestParseArgsParsesManifestOptionOverrides(t *testing.T) {
	got, err := ParseArgs([]string{"--language=frFR", "--hd-characters", "--option=hd-g", "--", "game"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Language != "frFR" || !got.HDCharacters || strings.Join(got.OptionSlugs, ",") != "hd-g" || strings.Join(got.GameArguments, ",") != "game" {
		t.Fatalf("parsed options = %+v", got)
	}
}

func TestParseArgsRejectsInvalidModeCombinations(t *testing.T) {
	for _, args := range [][]string{
		{"--quick", "--full"},
		{"--verify", "--dry-run"},
		{"--client-dir="},
		{"--gui", "--client-dir=/games"},
	} {
		if _, err := ParseArgs(args); err == nil {
			t.Errorf("ParseArgs(%q) accepted an invalid combination", args)
		}
	}
}

func TestRunCLITargetsSelectedClientFolder(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	clientRoot := t.TempDir()
	if err := (&CredentialStore{Root: clientRoot}).saveToken("client-token"); err != nil {
		t.Fatal(err)
	}
	launcherRoot := t.TempDir()
	api := &authFake{gamesErr: map[string]error{}}
	var stdout, stderr strings.Builder
	console := NewConsole(strings.NewReader(""), &stdout, &stderr, false, false)

	err := RunCLI(context.Background(), api, launcherRoot, []string{"--client-dir=" + clientRoot, "--status"}, console)
	if err != nil {
		t.Fatal(err)
	}
	if api.loginCalls != 0 {
		t.Fatalf("used interactive login instead of client-folder token: %d login calls", api.loginCalls)
	}
}

func TestRunCLIRemovesAddonsWithoutNetworkAccess(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	root := t.TempDir()
	manager := NewAddonManager(nil, root)
	addonsDir, err := manager.addonsDir(true)
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(addonsDir, "Example")
	if err := os.Mkdir(folder, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "example.lua"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := manager.saveState(addonState{Addons: map[string]addonRecord{
		"7": {Name: "Example", Folders: []string{"Example"}},
	}}); err != nil {
		t.Fatal(err)
	}
	api := &authFake{loginErr: errors.New("network must not be used")}
	var stdout, stderr strings.Builder
	console := NewConsole(strings.NewReader(""), &stdout, &stderr, false, false)

	if err := RunCLI(context.Background(), api, root, []string{"--remove-addons=Example"}, console); err != nil {
		t.Fatal(err)
	}
	if api.loginCalls != 0 {
		t.Fatalf("offline removal made %d login requests", api.loginCalls)
	}
	if _, err := os.Stat(folder); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("addon folder still exists: %v", err)
	}
}

func TestRunGamePassesArgumentsUnchanged(t *testing.T) {
	var stdout, stderr strings.Builder
	console := NewConsole(strings.NewReader(""), &stdout, &stderr, false, false)
	args := []string{"sh", "-c", `printf '<%s>\n' "$@"`, "game", "--quiet", "--game=forwarded"}
	if err := runGame(context.Background(), args, console); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "<--quiet>\n<--game=forwarded>\n"; got != want {
		t.Fatalf("game args output = %q, want %q", got, want)
	}
}

func TestRunGamePreservesChildExitStatus(t *testing.T) {
	var stdout, stderr strings.Builder
	console := NewConsole(strings.NewReader(""), &stdout, &stderr, false, false)
	err := runGame(context.Background(), []string{"sh", "-c", "exit 37"}, console)

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("runGame error = %v, want child exit error", err)
	}
	if got := exitErr.ExitCode(); got != 37 {
		t.Fatalf("child exit status = %d, want 37", got)
	}
}

func TestRunCLIFailedQuickUpdateDoesNotLaunchGame(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	if err := (&CredentialStore{Root: root}).saveToken("token"); err != nil {
		t.Fatal(err)
	}
	common := file("1", "common.dat", []byte("common"), nil)
	dll := file("2", "Data/game.dll", []byte("dll"), nil)
	api := &cliManifestAPI{
		updaterAPI: &updaterAPI{badDownload: true, urlCalls: make(map[string]int)},
		manifest:   manifest([]ManifestFile{common}, Game{Slug: "roguelike-prod", Files: []ManifestFile{dll}}),
	}
	marker := filepath.Join(t.TempDir(), "launched")
	console := NewConsole(strings.NewReader(""), &strings.Builder{}, &strings.Builder{}, false, false)
	err := RunCLI(context.Background(), api, root, []string{"--quick", "--", "sh", "-c", "touch \"$1\"", "game", marker}, console)
	if err == nil {
		t.Fatal("RunCLI succeeded despite failed required-file downloads")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("game launched after update failure: %v", err)
	}
	if api.urlCalls["1"] == 0 || api.urlCalls["2"] == 0 {
		t.Fatalf("missing download request for shared file or game DLL: %v (%v)", api.urlCalls, err)
	}
}

func TestRunCLIUpdatesPTRBeforeLaunching(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	production, ptr := t.TempDir(), t.TempDir()
	const dllPath = "version.dll"
	const realmPath = "Data/enUS/realmlist.wtf"
	mustWrite(t, filepath.Join(production, dllPath), []byte("production DLL"))
	mustWrite(t, filepath.Join(production, realmPath), []byte("set realmlist prod.example\n"))
	if err := (&CredentialStore{Root: ptr}).saveToken("ptr-session"); err != nil {
		t.Fatal(err)
	}
	dll := []byte("PTR DLL")
	api := &cliManifestAPI{
		updaterAPI: &updaterAPI{data: map[string][]byte{"2": dll}, urlCalls: map[string]int{}},
		manifest: manifest(nil,
			Game{Slug: "roguelike-prod", Realmlist: "prod.example", Files: []ManifestFile{file("1", dllPath, []byte("production DLL"), nil)}},
			Game{Slug: "roguelike", Realmlist: "ptr.example", Files: []ManifestFile{file("2", dllPath, dll, nil)}},
		),
	}
	// The child reads the files itself, so it fails if launch precedes the update.
	const checkClient = `test "$(cat "$1/version.dll")" = 'PTR DLL' &&
		test "$(cat "$1/Data/enUS/realmlist.wtf")" = 'set realmlist ptr.example' &&
		printf 'PTR client started\n'`
	var stdout, stderr strings.Builder
	console := NewConsole(strings.NewReader(""), &stdout, &stderr, false, false)
	args := []string{"--game=roguelike", "--client-dir=" + ptr, "--", "sh", "-c", checkClient, "game", ptr}
	if err := RunCLI(t.Context(), api, production, args, console); err != nil {
		t.Fatalf("PTR launch: %v\n%s%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "PTR client started\n") {
		t.Fatal("PTR was updated but the launch command did not run")
	}
	for path, want := range map[string]string{dllPath: "production DLL", realmPath: "set realmlist prod.example\n"} {
		got, err := os.ReadFile(filepath.Join(production, path))
		if err != nil || string(got) != want {
			t.Errorf("Production %s changed: got %q, err %v; want %q", path, got, err, want)
		}
	}
}

func TestRunCLIRejectsMismatchedSavedRealm(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	production, ptr := t.TempDir(), t.TempDir()
	store, err := NewPreferencesStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(Preferences{ProductionRoot: production, PTRRoot: ptr}); err != nil {
		t.Fatal(err)
	}
	const dll = "version.dll"
	games := manifest(nil,
		Game{Slug: "roguelike-prod", Files: []ManifestFile{file("1", dll, []byte("Production DLL"), nil)}},
		Game{Slug: "roguelike", Files: []ManifestFile{file("2", dll, []byte("PTR DLL"), nil)}},
	)
	for _, tc := range []struct {
		name, root, contents, correctFlag string
		args                              []string
	}{
		{"PTR into Production", production, "Production DLL", "--game=roguelike-prod", []string{"--game=roguelike"}},
		{"default Production into PTR", ptr, "PTR DLL", "--game=roguelike", []string{"--full"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(tc.root, dll)
			starting := "old " + tc.contents
			mustWrite(t, path, []byte(starting))
			if err := (&CredentialStore{Root: tc.root}).saveToken("fixture-token"); err != nil {
				t.Fatal(err)
			}
			api := &cliManifestAPI{
				updaterAPI: &updaterAPI{
					data:     map[string][]byte{"1": []byte("Production DLL"), "2": []byte("PTR DLL")},
					urlCalls: make(map[string]int),
				},
				manifest: games,
			}
			console := NewConsole(strings.NewReader(""), &strings.Builder{}, &strings.Builder{}, false, false)
			args := append([]string{"--client-dir=" + tc.root, "--quiet"}, tc.args...)
			err := RunCLI(t.Context(), api, tc.root, args, console)
			got, readErr := os.ReadFile(path)
			if err == nil || !strings.Contains(err.Error(), tc.correctFlag) || api.manifestCalls != 0 || readErr != nil || string(got) != starting {
				t.Fatalf("wanted refusal before authentication or writes; err=%v, manifest requests=%d, DLL=%q, read err=%v", err, api.manifestCalls, got, readErr)
			}
			if err := RunCLI(t.Context(), api, tc.root, []string{"--client-dir=" + tc.root, "--quiet", tc.correctFlag}, console); err != nil {
				t.Fatalf("update with the matching profile: %v", err)
			}
			got, readErr = os.ReadFile(path)
			if readErr != nil || string(got) != tc.contents {
				t.Fatalf("matching profile did not update the DLL: got %q, err %v; want %q", got, readErr, tc.contents)
			}
		})
	}
}
