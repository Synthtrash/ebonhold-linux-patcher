package launcher

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type addonFake struct {
	catalog   []Addon
	downloads []AddonDownload
	archive   []byte
}

func (f *addonFake) Login(context.Context, string, string) (string, error)       { return "", nil }
func (f *addonFake) Games(context.Context, string) (Manifest, error)             { return Manifest{}, nil }
func (f *addonFake) Status(context.Context, string) (ServerStatus, error)        { return ServerStatus{}, nil }
func (f *addonFake) Addons(context.Context, string) ([]Addon, error)             { return f.catalog, nil }
func (f *addonFake) DownloadURL(context.Context, string, string) (string, error) { return "", nil }
func (f *addonFake) AddonDownloads(context.Context, string, []int64) ([]AddonDownload, error) {
	return f.downloads, nil
}
func (f *addonFake) Download(_ context.Context, _ string, w io.Writer) error {
	_, e := io.Copy(w, bytes.NewReader(f.archive))
	return e
}
func zipBytes(t *testing.T, name string, mode os.FileMode) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	h := &zip.FileHeader{Name: name, Method: zip.Store}
	h.SetMode(mode)
	w, e := z.CreateHeader(h)
	if e != nil {
		t.Fatal(e)
	}
	_, e = w.Write([]byte("payload"))
	if e != nil {
		t.Fatal(e)
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestAddonResolve(t *testing.T) {
	m := NewAddonManager(nil, t.TempDir())
	cat := []Addon{{ID: 12, Name: "Alpha"}, {ID: 14, Name: "Beta"}}
	ids, e := m.Resolve(cat, " alpha,14,ALPHA ")
	if e != nil || len(ids) != 2 || ids[0] != 12 || ids[1] != 14 {
		t.Fatalf("Resolve = %v, %v", ids, e)
	}
	if _, e = m.Resolve(cat, "missing"); e == nil {
		t.Fatal("unknown addon accepted")
	}
}
func TestAddonInstalledRejectsWritableDirectoryChain(t *testing.T) {
	for _, tc := range []struct {
		name, path string
	}{
		{name: "installation root"},
		{name: "Interface", path: "Interface"},
		{name: "AddOns", path: filepath.Join("Interface", "AddOns")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			m := NewAddonManager(&addonFake{}, root)
			if _, err := m.addonsDir(true); err != nil {
				t.Fatal(err)
			}
			target := root
			if tc.path != "" {
				target = filepath.Join(root, tc.path)
			}
			if err := os.Chmod(target, 0777); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Installed(); err == nil ||
				!strings.Contains(err.Error(), "writable by other users") ||
				!strings.Contains(err.Error(), target) {
				t.Fatalf("Installed() error = %v, want writable-directory rejection for %q", err, target)
			}
		})
	}
}
func TestAddonInstallValidatesResponse(t *testing.T) {
	for _, downloads := range [][]AddonDownload{{{FileID: 1, Filename: "Interface/AddOns/A.zip", URL: "https://download.test/a"}}, {{FileID: 1, Filename: "Interface/AddOns/A.zip", URL: "https://download.test/a"}, {FileID: 1, Filename: "Interface/AddOns/A.zip", URL: "https://download.test/a"}}} {
		t.Run(map[bool]string{true: "duplicate", false: "partial"}[len(downloads) > 1], func(t *testing.T) {
			f := &addonFake{catalog: []Addon{{ID: 1, Name: "A", UpdatedAt: "today"}, {ID: 2, Name: "B"}}, downloads: downloads}
			if e := NewAddonManager(f, t.TempDir()).Install(context.Background(), "", []int64{1, 2}); e == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}
func TestAddonInstallRejectsUnsafeArchive(t *testing.T) {
	for _, tc := range []struct {
		name, entry string
		mode        os.FileMode
	}{{"traversal", "../escape", 0644}, {"symlink", "A/link", os.ModeSymlink}} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			f := &addonFake{catalog: []Addon{{ID: 1, Name: "A", UpdatedAt: "now"}}, downloads: []AddonDownload{{FileID: 1, Filename: "Interface/AddOns/A.zip", URL: "https://x.test/a"}}, archive: zipBytes(t, tc.entry, tc.mode)}
			if e := NewAddonManager(f, root).Install(context.Background(), "", []int64{1}); e == nil {
				t.Fatal("unsafe archive accepted")
			}
			if _, e := os.Stat(filepath.Join(root, "escape")); !os.IsNotExist(e) {
				t.Fatal("traversal wrote outside addon directory")
			}
		})
	}
}
func TestAddonInstallAndRemoveBackup(t *testing.T) {
	root := t.TempDir()
	f := &addonFake{catalog: []Addon{{ID: 1, Name: "Alpha", UpdatedAt: "now"}}, downloads: []AddonDownload{{FileID: 1, Filename: "Interface/AddOns/Alpha.zip", URL: "https://x.test/a"}}, archive: zipBytes(t, "Alpha/file.lua", 0644)}
	m := NewAddonManager(f, root)
	if e := m.Install(context.Background(), "", []int64{1}); e != nil {
		t.Fatal(e)
	}
	state, e := m.loadState()
	if e != nil || state.Addons["1"].Name != "Alpha" {
		t.Fatalf("state: %#v %v", state, e)
	}
	lines, e := m.Remove("alpha")
	if e != nil || len(lines) == 0 {
		t.Fatalf("Remove = %v, %v", lines, e)
	}
	restore := ""
	for _, line := range lines {
		if strings.HasPrefix(line, "Restore location: ") {
			restore = strings.TrimPrefix(line, "Restore location: ")
		}
	}
	if restore == "" {
		t.Fatal("missing restore location")
	}
	if _, e = os.Stat(filepath.Join(restore, "Alpha", "file.lua")); e != nil {
		t.Fatal(e)
	}
	state, e = m.loadState()
	if e != nil || len(state.Addons) != 0 {
		t.Fatalf("state after removal: %#v %v", state, e)
	}
}
func TestAddonRemovalLeavesSharedFolder(t *testing.T) {
	root := t.TempDir()
	m := NewAddonManager(&addonFake{}, root)
	d, e := m.addonsDir(true)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(filepath.Join(d, "Shared"), 0755); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(d, "Shared", "file"), []byte("x"), 0644)
	if e = m.saveState(addonState{Addons: map[string]addonRecord{"1": {Name: "One", Folders: []string{"Shared"}}, "2": {Name: "Two", Folders: []string{"Shared"}}}}); e != nil {
		t.Fatal(e)
	}
	lines, e := m.Remove("1")
	if e != nil || len(lines) == 0 {
		t.Fatalf("Remove = %v, %v", lines, e)
	}
	if _, e = os.Stat(filepath.Join(d, "Shared", "file")); e != nil {
		t.Fatal("shared folder removed", e)
	}
}
func TestAddonCheckDetectsManualInstall(t *testing.T) {
	root := t.TempDir()
	f := &addonFake{catalog: []Addon{{ID: 9, Name: "My Addon", UpdatedAt: "2099-01-01T00:00:00Z"}}}
	m := NewAddonManager(f, root)
	d, e := m.addonsDir(true)
	if e != nil {
		t.Fatal(e)
	}
	folder := filepath.Join(d, "my addon")
	if e = os.Mkdir(folder, 0755); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(folder, "addon.lua"), []byte("x"), 0644); e != nil {
		t.Fatal(e)
	}
	status, e := m.Check(context.Background(), "")
	if e != nil || len(status) != 1 || status[0].State != "UPDATE AVAILABLE" {
		t.Fatalf("Check = %#v, %v", status, e)
	}
}
func TestAddonInstallRemovesStaleUnsharedFolder(t *testing.T) {
	root := t.TempDir()
	f := &addonFake{catalog: []Addon{{ID: 1, Name: "A", UpdatedAt: "new"}}, downloads: []AddonDownload{{FileID: 1, Filename: "Interface/AddOns/A.zip", URL: "https://x.test/a"}}, archive: zipBytes(t, "New/file.lua", 0644)}
	m := NewAddonManager(f, root)
	d, e := m.addonsDir(true)
	if e != nil {
		t.Fatal(e)
	}
	os.Mkdir(filepath.Join(d, "Old"), 0755)
	os.WriteFile(filepath.Join(d, "Old", "old.lua"), []byte("x"), 0644)
	if e = m.saveState(addonState{Addons: map[string]addonRecord{"1": {Name: "A", UpdatedAt: "old", Folders: []string{"Old"}}}}); e != nil {
		t.Fatal(e)
	}
	if e = m.Install(context.Background(), "", []int64{1}); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(d, "Old")); !os.IsNotExist(e) {
		t.Fatal("stale folder remains")
	}
	if _, e = os.Stat(filepath.Join(d, "New", "file.lua")); e != nil {
		t.Fatal(e)
	}
}
func TestAddonRemoveRejectsUnsafeNameAndBackupSymlink(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsafe-name", true: "backup-symlink"}[symlink], func(t *testing.T) {
			root := t.TempDir()
			m := NewAddonManager(&addonFake{}, root)
			d, e := m.addonsDir(true)
			if e != nil {
				t.Fatal(e)
			}
			os.Mkdir(filepath.Join(d, "Folder"), 0755)
			name := "Safe"
			backup := filepath.Join(root, ".ebonhold-removed-addons")
			if symlink {
				target := t.TempDir()
				if e = os.Symlink(target, backup); e != nil {
					t.Fatal(e)
				}
			} else {
				name = "../escape"
			}
			if e = m.saveState(addonState{Addons: map[string]addonRecord{"1": {Name: name, Folders: []string{"Folder"}}}}); e != nil {
				t.Fatal(e)
			}
			if _, e = m.Remove("1"); e == nil {
				t.Fatal("unsafe backup path accepted")
			}
		})
	}
}

func TestExtractZipRejectsLooseFiles(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "addon.zip")
	if err := os.WriteFile(archivePath, zipBytes(t, "README.txt", 0644), 0600); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()

	if _, err := extractZip(archive, t.TempDir()); err == nil {
		t.Fatal("archive containing a loose top-level file was accepted")
	}
}
