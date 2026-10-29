package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreferencesSaveAndLoadPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ebonhold-launcher", "config.json")
	store := &PreferencesStore{path: path}
	want := Preferences{ClientRoot: "/home/player/Games/Ebonhold", GameSlug: "roguelike", Manager: "steam"}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil || got != want {
		t.Fatalf("Load() = %+v, %v; want %+v", got, err, want)
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil || dir.Mode().Perm() != 0700 {
		t.Fatalf("preferences directory mode = %v, %v", dir, err)
	}
	file, err := os.Stat(path)
	if err != nil || file.Mode().Perm() != 0600 {
		t.Fatalf("preferences file mode = %v, %v", file, err)
	}
}

func TestPreferencesSaveRefusesSymlinkTarget(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	victim := filepath.Join(root, "victim.json")
	if err := os.WriteFile(victim, []byte(`{"client_root":"untouched"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, path); err != nil {
		t.Fatal(err)
	}
	if err := (&PreferencesStore{path: path}).Save(Preferences{ClientRoot: "/games"}); err == nil {
		t.Fatal("Save accepted a symlink target")
	}
	data, err := os.ReadFile(victim)
	if err != nil || string(data) != `{"client_root":"untouched"}` {
		t.Fatalf("symlink target changed: %q, %v", data, err)
	}
}

func TestManagerPresetsIncludeClientDirectoryAndQuotePaths(t *testing.T) {
	launcherPath := "/home/player/Studio's launcher/ebonhold"
	clientRoot := "/home/player/Games/Ebonhold Client"
	steam := ManagerPreset("steam", launcherPath, clientRoot, "roguelike")
	if !strings.Contains(steam, shellQuote(launcherPath)+" --client-dir="+shellQuote(clientRoot)+" --game='roguelike'") || !strings.HasSuffix(steam, "-- %command%") {
		t.Fatalf("Steam preset does not safely target the client folder: %q", steam)
	}
	bottles := ManagerPreset("bottles-flatpak", launcherPath, clientRoot, "roguelike")
	if !strings.Contains(bottles, "--client-dir="+shellQuote(clientRoot)) || !strings.Contains(bottles, "--game='roguelike'") || !strings.Contains(bottles, "REPLACE_WITH_BOTTLE_NAME") {
		t.Fatalf("Bottles preset is incomplete: %q", bottles)
	}
}
