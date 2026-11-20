package launcher

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLutrisGameShortcutsMatchSelectedExecutable(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Wow.exe"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "different-install")
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "Wow.exe"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	configDir := t.TempDir()
	gamesDir := filepath.Join(configDir, "games")
	if err := os.MkdirAll(gamesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gamesDir, "wow-entry.yml"), []byte("game:\n  exe: Wow.exe\n"), 0600); err != nil {
		t.Fatal(err)
	}

	games := []lutrisGame{
		{ID: 7, Name: "World of Warcraft", Directory: root, ConfigPath: "wow-entry"},
		{ID: 8, Name: "World of Warcraft", Directory: other, ConfigPath: "wow-entry"},
	}
	got := lutrisGameShortcuts(context.Background(), root, configDir, games)
	want := []GameShortcut{{ID: "7", Name: "World of Warcraft"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lutrisGameShortcuts() = %#v, want %#v", got, want)
	}
}
