package launcher

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestFindClientRootsSearchesCommonLocationsAndRejectsDecoys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataHome := filepath.Join(home, ".local", "share")
	t.Setenv("XDG_DATA_HOME", dataHome)

	gamesClient := filepath.Join(home, "Games", "Ebonhold")
	steamClient := filepath.Join(dataHome, "Steam", "steamapps", "common", "Ebonhold")
	bottlesClient := filepath.Join(dataHome, "bottles", "bottles", "Main", "drive_c", "Program Files", "World of Warcraft")
	for _, path := range []string{gamesClient, steamClient, bottlesClient} {
		makeClientRoot(t, path)
	}
	if err := os.MkdirAll(filepath.Join(home, "Games", "NotTheGame", "Data"), 0755); err != nil {
		t.Fatal(err)
	}
	outsideSearchRoots := filepath.Join(home, "Downloads", "Ebonhold")
	makeClientRoot(t, outsideSearchRoots)

	got := FindClientRoots()
	want := []string{gamesClient, steamClient, bottlesClient}
	for i := range want {
		want[i], _ = filepath.Abs(want[i])
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindClientRoots() = %q, want %q", got, want)
	}
}

func makeClientRoot(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, "Data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "Wow.exe"), []byte("mock executable"), 0644); err != nil {
		t.Fatal(err)
	}
}
