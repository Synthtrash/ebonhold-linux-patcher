package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreferencesSaveAndLoadPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ebonhold-launcher", "config.json")
	store := &PreferencesStore{path: path}
	want := Preferences{
		ProductionRoot:     "/home/player/Games/Ebonhold",
		PTRRoot:            "/home/player/Games/Ebonhold-PTR",
		GameSlug:           "roguelike-prod",
		ProductionLaunch:   LaunchTarget{Manager: "steam", ID: "314159"},
		PTRLaunch:          LaunchTarget{Manager: "bottles-native", ID: "Ebonhold PTR"},
		ProductionSettings: GameSettings{Language: "frFR", HDCharacters: true},
		PTRSettings:        GameSettings{Language: "enUS", HDCreatures: true},
		LauncherLanguage:   "en",
	}
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

func TestLegacyClientRootMigratesToProduction(t *testing.T) {
	for _, profile := range []string{"roguelike-prod", "roguelike"} {
		t.Run(profile, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			root := filepath.Join(t.TempDir(), "client")
			legacy, err := json.Marshal(map[string]string{
				"client_root": root,
				"game_slug":   profile,
				"manager":     "steam",
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, legacy, 0600); err != nil {
				t.Fatal(err)
			}
			store := &PreferencesStore{path: path}
			got, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if got.ProductionRoot != root {
				t.Fatalf("migrated Production root = %q, want %q", got.ProductionRoot, root)
			}
			if got.PTRRoot != "" {
				t.Fatalf("legacy root was also assigned to PTR: %+v", got)
			}
			if got.ProductionLaunch.Manager != "steam" || got.PTRLaunch != (LaunchTarget{}) {
				t.Fatalf("legacy manager migrated to wrong profile: %+v", got)
			}
			if err := store.Save(got); err != nil {
				t.Fatal(err)
			}
			saved, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(saved, &fields); err != nil {
				t.Fatal(err)
			}
			if _, ok := fields["client_root"]; ok {
				t.Fatal("save retained the legacy shared-root field")
			}
		})
	}
}

func TestProfileRootsStayIsolatedWhenSwitching(t *testing.T) {
	production := t.TempDir()
	ptr := t.TempDir()
	prefs := Preferences{ProductionRoot: production, GameSlug: "roguelike-prod"}
	if got := prefs.RootForProfile(prefs.GameSlug); got != production {
		t.Fatalf("Production root = %q, want %q", got, production)
	}
	prefs.GameSlug = "roguelike"
	if got := prefs.RootForProfile(prefs.GameSlug); got != "" {
		t.Fatalf("PTR fell back to Production root %q", got)
	}
	prefs.SetRootForProfile(prefs.GameSlug, ptr)
	if err := prefs.ValidateRoots(); err != nil {
		t.Fatal(err)
	}
	prefs.GameSlug = "roguelike-prod"
	if got := prefs.RootForProfile(prefs.GameSlug); got != production {
		t.Fatalf("switching back changed Production root to %q", got)
	}
	prefs.GameSlug = "roguelike"
	if got := prefs.RootForProfile(prefs.GameSlug); got != ptr {
		t.Fatalf("switching back changed PTR root to %q", got)
	}
}

func TestPreferencesRejectSharedProfileFolders(t *testing.T) {
	production := t.TempDir()
	ptrParent := t.TempDir()
	ptr := filepath.Join(ptrParent, "ptr")
	if err := os.Symlink(production, ptr); err != nil {
		t.Fatal(err)
	}
	prefs := Preferences{ProductionRoot: production, PTRRoot: ptr}
	if err := prefs.ValidateRoots(); err == nil {
		t.Fatal("ValidateRoots accepted two paths resolving to the same folder")
	}
	if err := (&PreferencesStore{path: filepath.Join(t.TempDir(), "config.json")}).Save(prefs); err == nil {
		t.Fatal("Save accepted shared Production/PTR folder")
	}
}

func TestProfileFolderChangeInvalidatesOnlyItsLaunchTarget(t *testing.T) {
	prefs := Preferences{
		ProductionRoot: "/games/live", PTRRoot: "/games/ptr",
		ProductionLaunch: LaunchTarget{Manager: "steam", ID: "42"},
		PTRLaunch:        LaunchTarget{Manager: "bottles-native", ID: "Test Bottle"},
	}
	prefs.SetRootForProfile("roguelike", "/games/live/nested-ptr")
	if err := prefs.ValidateRoots(); err == nil {
		t.Fatal("nested client folders share writable data")
	}
	if prefs.PTRLaunch.ID != "" || prefs.PTRLaunch.Manager != "bottles-native" || prefs.ProductionLaunch.ID != "42" {
		t.Fatalf("changed folder retained or reset the wrong target: %+v", prefs)
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
	if err := (&PreferencesStore{path: path}).Save(Preferences{ProductionRoot: "/games"}); err == nil {
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
	steam := (LaunchTarget{Manager: "steam"}).Preset(launcherPath, clientRoot, "roguelike")
	if !strings.Contains(steam, shellQuote(launcherPath)+" --client-dir="+shellQuote(clientRoot)+" --game='roguelike'") || !strings.HasSuffix(steam, "-- %command%") {
		t.Fatalf("Steam preset does not safely target the client folder: %q", steam)
	}
	bottle := "Trash's Test Bottle"
	bottles := (LaunchTarget{Manager: "bottles-flatpak", ID: bottle}).Preset(launcherPath, clientRoot, "roguelike")
	if !strings.Contains(bottles, "--client-dir="+shellQuote(clientRoot)) || !strings.Contains(bottles, "--game='roguelike'") || !strings.Contains(bottles, "--bottle="+shellQuote(bottle)) || !strings.Contains(bottles, "-e "+shellQuote(filepath.Join(clientRoot, "Wow.exe"))) {
		t.Fatalf("Bottles preset is incomplete: %q", bottles)
	}
	nativeBottles := (LaunchTarget{Manager: "bottles-native", ID: bottle}).Preset(launcherPath, clientRoot, "roguelike")
	if !strings.Contains(nativeBottles, "-e "+shellQuote(filepath.Join(clientRoot, "Wow.exe"))) {
		t.Fatalf("native Bottles preset does not use the selected game's executable: %q", nativeBottles)
	}
}
