package launcher

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyClientCopiesIndependentGameFilesAndSkipsPrivateState(t *testing.T) {
	source := t.TempDir()
	data := filepath.Join(source, "Data")
	if err := os.MkdirAll(filepath.Join(data, "Cache"), 0755); err != nil {
		t.Fatal(err)
	}
	for path, contents := range map[string]string{
		"Wow.exe":                 "game executable",
		"Launcher.exe":            "launcher binary",
		"Data/base.MPQ":           "game data",
		"Data/Cache/private.wdb":  "cache",
		"WTF/Account/Saved.lua":   "account state",
		".launcher-token":         "secret",
		"Screenshots/private.png": "screenshot",
	} {
		full := filepath.Join(source, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	parent := t.TempDir()
	destination := filepath.Join(parent, "copy")
	if err := CopyClient(context.Background(), source, destination); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"Wow.exe": "game executable", "Launcher.exe": "launcher binary", "Data/base.MPQ": "game data"} {
		got, err := os.ReadFile(filepath.Join(destination, filepath.FromSlash(path)))
		if err != nil || string(got) != want {
			t.Fatalf("copied %s = %q, %v; want %q", path, got, err, want)
		}
	}
	if err := os.WriteFile(filepath.Join(destination, "Wow.exe"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(source, "Wow.exe"))
	if err != nil || string(original) != "game executable" {
		t.Fatalf("source changed through copy: %q, %v", original, err)
	}
	for _, path := range []string{"Data/Cache/private.wdb", "WTF/Account/Saved.lua", ".launcher-token", "Screenshots/private.png"} {
		if _, err := os.Lstat(filepath.Join(destination, filepath.FromSlash(path))); !os.IsNotExist(err) {
			t.Fatalf("private path %s was copied (err %v)", path, err)
		}
	}
	api := &updaterAPI{}
	u := NewUpdater(api, &Authenticator{API: api}, destination)
	ptr := manifest(nil, Game{Slug: "roguelike", Realmlist: "ptr.example", Files: []ManifestFile{
		file("1", "Data/base.MPQ", []byte("game data"), nil),
	}})
	if !PTRNeedsInstall(destination) || PTRNeedsInstall(source) {
		t.Fatal("only the new copy should require a PTR update")
	}
	if _, err := u.Run(context.Background(), ptr, "token", UpdateOptions{GameSlug: "roguelike", Verify: true}, nil); err != nil {
		t.Fatal(err)
	}
	if !PTRNeedsInstall(destination) {
		t.Fatal("verification allowed play without installing PTR")
	}
	if _, err := u.Run(context.Background(), ptr, "token", UpdateOptions{GameSlug: "roguelike"}, nil); err != nil {
		t.Fatal(err)
	}
	realm, err := os.ReadFile(filepath.Join(destination, "Data", "enUS", "realmlist.wtf"))
	if err != nil || string(realm) != "set realmlist ptr.example\n" || PTRNeedsInstall(destination) {
		t.Fatalf("updated copy is not ready for PTR: realm=%q err=%v", realm, err)
	}
}

func TestClientCopySpaceCountsCopiedFilesWithoutCreatingDestination(t *testing.T) {
	source, parent := t.TempDir(), t.TempDir()
	for path, contents := range map[string]string{
		"Wow.exe":                "wow",
		"Client.DLL":              "dll",
		"Data/base.MPQ":          "data",
		"Data/Cache/private.wdb": "private",
		"Data/.hidden":           "hidden",
		"WTF/account.lua":        "private",
	} {
		full := filepath.Join(source, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	destination := filepath.Join(parent, "copy")
	needed, available, err := ClientCopySpace(context.Background(), source, destination)
	if err != nil {
		t.Fatal(err)
	}
	if want := uint64(len("wow") + len("dll") + len("data")); needed != want {
		t.Fatalf("needed = %d; want %d", needed, want)
	}
	if available == 0 {
		t.Fatal("destination parent reported no available space")
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("preview created destination: %v", err)
	}
}
