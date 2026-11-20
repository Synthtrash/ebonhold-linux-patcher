package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallApplicationShortcut(t *testing.T) {
	dataHome := filepath.Join(t.TempDir(), "user data")
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("HOME", t.TempDir())

	applications := filepath.Join(dataHome, "applications")
	if err := os.MkdirAll(applications, 0755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "existing entry")
	if err := os.WriteFile(victim, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	entryPath := filepath.Join(applications, desktopEntryID)
	if err := os.Symlink(victim, entryPath); err != nil {
		t.Fatal(err)
	}

	executable := `/home/user/Ebon hold 50% "quoted" $dollar \slash`
	if err := InstallApplicationShortcut(executable, []byte("png")); err != nil {
		t.Fatal(err)
	}
	entry, err := os.ReadFile(entryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(entry), "50%%") ||
		!strings.Contains(string(entry), strings.Repeat("\\", 4)+"slash") ||
		!strings.Contains(string(entry), "--gui") {
		t.Fatalf("Exec did not preserve argv path: %s", entry)
	}
	info, err := os.Lstat(entryPath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("entry was not safely replaced: info=%v err=%v", info, err)
	}
	preserved, err := os.ReadFile(victim)
	if err != nil || string(preserved) != "preserve" {
		t.Fatalf("symlink target changed: contents=%q err=%v", preserved, err)
	}
	iconPath := filepath.Join(dataHome, "icons", "hicolor", "256x256", "apps", "com.project-ebonhold.launcher.png")
	icon, err := os.ReadFile(iconPath)
	if err != nil || string(icon) != "png" {
		t.Fatalf("icon not installed: contents=%q err=%v", icon, err)
	}
}
