package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyGameCVarsPreservesSettingsAndBackup(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "WTF", "Config.wtf")
	const before = "\xef\xbb\xbfSET GXWINDOW \"0\"\r\nSET realmList \"ptr.example\"\r\nset gxWindow \"0\"\r\nSET gxResolution \"2560x1440\"\r\nSET Sound_MasterVolume \"0.5\""
	const after = "\xef\xbb\xbfSET gxWindow \"1\"\r\nSET realmList \"ptr.example\"\r\nSET gxResolution \"2560x1440\"\r\nSET Sound_MasterVolume \"0.5\"\r\nSET gxMaximize \"1\"\r\n"
	mustWrite(t, path, []byte(before))
	const preset = "gxWindow=1\ngxMaximize=1"
	for _, wantChange := range []bool{true, false} {
		changed, err := ApplyGameCVars(root, preset)
		if err != nil || changed != wantChange {
			t.Fatalf("applying windowed preset: changed=%v, err=%v, want changed=%v", changed, err, wantChange)
		}
		for file, want := range map[string]string{path: after, path + ".before-launcher-cvars": before} {
			got, err := os.ReadFile(file)
			if err != nil || string(got) != want {
				t.Fatalf("%s = %q, %v; want %q", file, got, err, want)
			}
		}
	}
}

func TestApplyGameCVarsDoesNotWriteThroughAnotherProfile(t *testing.T) {
	production, ptr := t.TempDir(), t.TempDir()
	path := filepath.Join(production, "WTF", "Config.wtf")
	const before = "SET gxWindow \"0\"\n"
	mustWrite(t, path, []byte(before))
	if _, err := ApplyGameCVars(production, "gxWindow=1\nRealmList=ptr.example"); err == nil {
		t.Fatal("accepted a CVar edit that changes realm routing")
	}
	if err := os.Symlink(filepath.Dir(path), filepath.Join(ptr, "WTF")); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyGameCVars(ptr, "gxWindow=1"); err == nil {
		t.Fatal("followed a symlink into the other profile")
	}
	unlock, err := acquireFileLock(filepath.Join(production, installationLock))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := ApplyGameCVars(production, "gxWindow=1"); err == nil || !strings.Contains(err.Error(), "another launcher operation") {
		t.Fatalf("expected refusal while the client is updating, got %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != before {
		t.Fatalf("config after rejected edits = %q, %v", got, err)
	}
	if _, err := os.Lstat(path + ".before-launcher-cvars"); !os.IsNotExist(err) {
		t.Fatalf("unexpected backup after rejected edits: %v", err)
	}
}
