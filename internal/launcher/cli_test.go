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
