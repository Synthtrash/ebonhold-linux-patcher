package launcher

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type updaterAPI struct {
	mu            sync.Mutex
	data          map[string][]byte
	urlErrors     map[string][]error
	urlCalls      map[string]int
	loginCalls    int
	downloadCalls int
	badDownload   bool
	onDownload    func() error
}

func (a *updaterAPI) Login(context.Context, string, string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.loginCalls++
	return "renewed", nil
}
func (*updaterAPI) Games(context.Context, string) (Manifest, error) {
	return Manifest{Success: true}, nil
}
func (*updaterAPI) Status(context.Context, string) (ServerStatus, error) { return ServerStatus{}, nil }
func (*updaterAPI) Addons(context.Context, string) ([]Addon, error)      { return nil, nil }
func (a *updaterAPI) DownloadURL(_ context.Context, token, id string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.urlCalls[id]++
	if seq := a.urlErrors[id]; len(seq) > 0 {
		err := seq[0]
		a.urlErrors[id] = seq[1:]
		return "", err
	}
	return "https://cdn.example/" + id, nil
}
func (*updaterAPI) AddonDownloads(context.Context, string, []int64) ([]AddonDownload, error) {
	return nil, nil
}
func (a *updaterAPI) Download(_ context.Context, downloadURL string, dst io.Writer) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.downloadCalls++
	if a.onDownload != nil {
		if err := a.onDownload(); err != nil {
			return err
		}
	}
	id := filepath.Base(downloadURL)
	if a.badDownload {
		_, err := dst.Write([]byte("wrong"))
		return err
	}
	_, err := dst.Write(a.data[id])
	return err
}

func md5Hash(data []byte) string {
	sum := md5.Sum(data)
	return base64.StdEncoding.EncodeToString(sum[:])
}
func file(id, path string, data []byte, option *string) ManifestFile {
	return ManifestFile{ID: id, Path: path, Hash: md5Hash(data), OptionSlug: option}
}
func manifest(common []ManifestFile, games ...Game) Manifest {
	var m Manifest
	m.Success = true
	m.Data.Common.Files = common
	m.Data.Games = games
	return m
}
func game(files ...ManifestFile) Game {
	return Game{Slug: "retail", Realmlist: "realm.example", Files: files}
}
func setupUpdater(t *testing.T, api *updaterAPI) (string, *Updater) {
	t.Helper()
	root := t.TempDir()
	api.urlCalls = map[string]int{}
	if api.urlErrors == nil {
		api.urlErrors = map[string][]error{}
	}
	if api.data == nil {
		api.data = map[string][]byte{}
	}
	return root, NewUpdater(api, nil, root)
}
func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestUpdaterModeSelection(t *testing.T) {
	opt := "optional"
	common := file("1", "common.dat", []byte("c"), nil)
	gameFiles := []ManifestFile{file("2", "Data/patch.MPQ", []byte("p"), nil), file("3", "Data/base.MPQ", []byte("b"), nil), file("4", "Data/optional", []byte("o"), &opt)}
	m := manifest([]ManifestFile{common}, game(gameFiles...))
	for _, tc := range []struct {
		name string
		opts UpdateOptions
		want []string
	}{{"default", UpdateOptions{GameSlug: "retail", DryRun: true}, []string{"common.dat", "Data/patch.MPQ", "Data/base.MPQ"}}, {"quick", UpdateOptions{GameSlug: "retail", Quick: true, DryRun: true}, []string{"Data/patch.MPQ"}}, {"full", UpdateOptions{GameSlug: "retail", Full: true, DryRun: true}, []string{"common.dat", "Data/patch.MPQ", "Data/base.MPQ", "Data/optional"}}} {
		t.Run(tc.name, func(t *testing.T) {
			_, u := setupUpdater(t, &updaterAPI{})
			got, err := u.Run(context.Background(), m, "token", tc.opts, nil)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got.PlannedPaths, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("paths %v, want %v", got.PlannedPaths, tc.want)
			}
		})
	}
}

func TestAvailableGameOptionsExposeManifestPaths(t *testing.T) {
	language, hd := "fr-pack", "hd-pack"
	m := manifest([]ManifestFile{file("1", "Data/frFR/locale.MPQ", []byte("fr"), &language)}, game(file("2", "Data/patch-H.MPQ", []byte("hd"), &hd)))
	options := AvailableGameOptions(m, "retail")
	if len(options) != 2 || options[0].Slug != "fr-pack" || options[0].Kind != "language" || options[0].Locale != "frFR" || options[1].Slug != "hd-pack" || options[1].Kind != "hd-characters" {
		t.Fatalf("options = %#v", options)
	}
}

func TestUpdaterSwitchesLanguageWithoutReplacingOtherSettings(t *testing.T) {
	frPack := []byte("French game data")
	api := &updaterAPI{data: map[string][]byte{"2": frPack}}
	root, u := setupUpdater(t, api)
	mustWrite(t, filepath.Join(root, "Wow.exe"), []byte("client"))
	mustWrite(t, filepath.Join(root, "Data", "enUS", "locale-enUS.MPQ"), []byte("English game data"))
	path := filepath.Join(root, "WTF", "Config.wtf")
	mustWrite(t, path, []byte("SET gxWindow \"1\"\nSET locale \"enUS\"\n"))
	slug := "fr-pack"
	relativePack := "Data/frFR/locale-frFR.MPQ"
	m := manifest(nil, game(file("1", "Wow.exe", []byte("client"), nil), file("2", relativePack, frPack, &slug)))
	for _, locale := range []string{"frFR", "enUS", "frFR"} {
		if _, err := u.Run(context.Background(), m, "token", UpdateOptions{GameSlug: "retail", Language: locale}, nil); err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf("SET gxWindow \"1\"\nSET locale %q\n", locale)
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Fatalf("config = %q, %v; want %q", got, err, want)
		}
		realm := filepath.Join(root, "Data", locale, "realmlist.wtf")
		if got, err := os.ReadFile(realm); err != nil || string(got) != "set realmlist realm.example\n" {
			t.Fatalf("realmlist for %s = %q, %v", locale, got, err)
		}
	}
	if api.downloadCalls != 1 {
		t.Fatalf("French pack downloaded %d times, want once", api.downloadCalls)
	}
}

func TestUpdaterBacksUpDisabledHDOnlyWhenManaged(t *testing.T) {
	root, u := setupUpdater(t, &updaterAPI{})
	slug := "hd"
	client := file("1", "Wow.exe", []byte("client"), nil)
	mustWrite(t, filepath.Join(root, client.Path), []byte("client"))
	hdPath := filepath.Join(root, "Data", "Patch-H.mpq")
	mustWrite(t, hdPath, []byte("user data"))
	otherHD := filepath.Join(root, "data", "Patch-H.mpq")
	mustWrite(t, otherHD, []byte("other user data"))
	m := manifest(nil, game(client,
		file("2", "Data/patch-H.MPQ", []byte("official"), &slug),
		file("3", "data/patch-H.MPQ", []byte("official"), &slug),
	))
	opts := UpdateOptions{GameSlug: "retail"}
	if _, err := u.Run(context.Background(), m, "token", opts, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(hdPath); err != nil || string(got) != "user data" {
		t.Fatalf("default update changed manual HD: %q, %v", got, err)
	}
	opts.ManageHD = true
	if _, err := u.Run(context.Background(), m, "token", opts, nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, folder, want string }{
		{hdPath, "Data", "user data"}, {otherHD, "data", "other user data"},
	} {
		if _, err := os.Stat(tc.path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("disabled HD is still active at %s: %v", tc.path, err)
		}
		backups, err := filepath.Glob(filepath.Join(root, ".ebonhold-disabled-hd-*", tc.folder, "Patch-H.mpq"))
		if err != nil || len(backups) != 1 {
			t.Fatalf("backups = %v, %v", backups, err)
		}
		if got, err := os.ReadFile(backups[0]); err != nil || string(got) != tc.want {
			t.Fatalf("disabled HD was not preserved: %q, %v", got, err)
		}
	}
}

func TestUpdaterRejectsUnsafePathsAndHashes(t *testing.T) {
	for _, tc := range []struct{ name, path, hash string }{{"traversal", "../outside", md5Hash([]byte("x"))}, {"absolute", "/tmp/file", md5Hash([]byte("x"))}, {"hash", "safe", base64.StdEncoding.EncodeToString([]byte("short"))}} {
		t.Run(tc.name, func(t *testing.T) {
			_, u := setupUpdater(t, &updaterAPI{})
			f := ManifestFile{ID: "1", Path: tc.path, Hash: tc.hash}
			if _, err := u.Run(context.Background(), manifest(nil, game(f)), "", UpdateOptions{GameSlug: "retail", DryRun: true}, nil); err == nil {
				t.Fatal("expected invalid manifest rejection")
			}
		})
	}
}

func TestUpdaterRejectsSymlinkedParent(t *testing.T) {
	root, u := setupUpdater(t, &updaterAPI{})
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	f := file("1", "link/file", []byte("new"), nil)
	if _, err := u.Run(context.Background(), manifest(nil, game(f)), "", UpdateOptions{GameSlug: "retail", DryRun: true}, nil); err == nil {
		t.Fatal("expected symlink parent rejection")
	}
}

func TestUpdaterAtomicReplacementAndChecksumFailure(t *testing.T) {
	data := []byte("new contents")
	api := &updaterAPI{data: map[string][]byte{"1": data}}
	root, u := setupUpdater(t, api)
	target := filepath.Join(root, "game.dat")
	mustWrite(t, target, []byte("old"))
	var events []Progress
	res, err := u.Run(context.Background(), manifest(nil, game(file("1", "game.dat", data, nil))), "token", UpdateOptions{GameSlug: "retail"}, func(p Progress) {
		events = append(events, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != string(data) || res.Downloaded != 1 {
		t.Fatalf("replacement = %q, result %+v", got, res)
	}
	if len(events) < 2 || events[0].Stage != "plan" || events[0].Path != "game.dat" {
		t.Fatalf("update plan was not reported before download: %+v", events)
	}
	if events[1].Stage != "download" || events[1].Path != "game.dat" {
		t.Fatalf("download progress = %+v", events)
	}
	api.badDownload = true
	api.data = map[string][]byte{"1": data}
	bad := file("2", "game.dat", []byte("expected"), nil)
	res, err = u.Run(context.Background(), manifest(nil, game(bad)), "token", UpdateOptions{GameSlug: "retail"}, nil)
	if err == nil || len(res.FailedFiles) != 1 {
		t.Fatalf("wanted failed mismatch, result=%+v err=%v", res, err)
	}
	got, _ = os.ReadFile(target)
	if string(got) != string(data) {
		t.Fatalf("target changed on failed checksum: %q", got)
	}
}

func TestUpdaterStopsWhenGameDirectoryMovesDuringDownload(t *testing.T) {
	for _, moved := range []string{"Data", "."} {
		t.Run(moved, func(t *testing.T) {
			const path = "Data/patch.MPQ"
			patch := []byte("new contents")
			api := &updaterAPI{data: map[string][]byte{"1": patch}}
			root, updater := setupUpdater(t, api)
			mustWrite(t, filepath.Join(root, path), []byte("old contents"))
			mustWrite(t, filepath.Join(root, ptrInstallMarker), nil)
			stash := filepath.Join(t.TempDir(), "original")
			api.onDownload = func() error {
				folder := filepath.Join(root, moved)
				if err := os.Rename(folder, stash); err != nil {
					return err
				}
				return os.Mkdir(folder, 0700)
			}
			g := Game{Slug: "roguelike", Files: []ManifestFile{file("1", path, patch, nil)}}
			res, err := updater.Run(context.Background(), manifest(nil, g), "", UpdateOptions{GameSlug: g.Slug}, nil)
			if err == nil || !strings.Contains(err.Error(), "folder changed during the update") {
				t.Fatalf("update did not report a moved folder: %+v, %v", res, err)
			}
			if moved == "Data" && res.Downloaded != 0 {
				t.Errorf("reported a completed download outside the active folder: %+v", res)
			}
			markerRoot := root
			if moved == "." {
				markerRoot = stash
			}
			if !PTRNeedsInstall(markerRoot) {
				t.Error("a partial update marked the copy ready to play")
			}
		})
	}
}

func TestUpdaterTraversesSearchOnlyAncestor(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read search-only directories")
	}
	const path = "packs/client/patch.MPQ"
	patch := []byte("new contents")
	root, updater := setupUpdater(t, &updaterAPI{data: map[string][]byte{"1": patch}})
	target := filepath.Join(root, path)
	ancestor := filepath.Join(root, "packs")
	mustWrite(t, target, []byte("old contents"))
	if err := os.Chmod(ancestor, 0100); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(ancestor, 0700); err != nil {
			t.Error(err)
		}
	})
	g := game(file("1", path, patch, nil))
	if _, err := updater.Run(context.Background(), manifest(nil, g), "", UpdateOptions{GameSlug: g.Slug}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != string(patch) {
		t.Fatalf("patch under search-only ancestor: %q, %v", got, err)
	}
}

func TestUpdaterReportsDownloadCauses(t *testing.T) {
	data := []byte("expected contents")
	api := &updaterAPI{
		data: map[string][]byte{"2": []byte("wrong"), "3": data},
		urlErrors: map[string][]error{"1": {&HTTPError{
			StatusCode: 429, Message: "https://cdn.example/file?signature=private-fixture",
		}}},
	}
	root, updater := setupUpdater(t, api)
	staleCache := filepath.Join(root, "Cache/old.wdb")
	mustWrite(t, staleCache, []byte("cached game data"))
	mustWrite(t, filepath.Join(root, ptrInstallMarker), nil)
	g := game(file("1", "link.dat", data, nil), file("2", "hash.dat", data, nil), file("3", "ok.dat", data, nil))
	g.Slug = "roguelike"
	result, err := updater.Run(context.Background(), manifest(nil, g), "token", UpdateOptions{GameSlug: g.Slug}, nil)
	if err == nil {
		t.Fatal("expected the failed downloads to be reported")
	}
	for _, want := range []string{"link.dat: request download link: HTTP 429", "hash.dat: download checksum mismatch"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("download report is missing %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "private-fixture") {
		t.Error("download report exposed a signed URL")
	}
	if result.Downloaded != 1 {
		t.Errorf("report counted %d successful downloads, want 1", result.Downloaded)
	}
	if _, err := os.Stat(staleCache); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale cache after partial update: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Cache/invalid")); err != nil {
		t.Errorf("cache invalidation marker missing: %v", err)
	}
	if !PTRNeedsInstall(root) {
		t.Error("partial update enabled first-time PTR launch")
	}
}

func TestUpdaterFinishesPendingCacheRefreshWithoutDownloadingAgain(t *testing.T) {
	data := []byte("patch")
	api := &updaterAPI{data: map[string][]byte{"1": data}}
	root, updater := setupUpdater(t, api)
	cache := filepath.Join(root, "Cache")
	mustWrite(t, cache, nil) // A file in the way prevents a refresh.
	mustWrite(t, filepath.Join(root, ptrInstallMarker), nil)
	g := Game{Slug: "roguelike", Files: []ManifestFile{file("1", "Data/patch.MPQ", data, nil)}}
	m := manifest(nil, g)
	ctx := context.Background()
	result, err := updater.Run(ctx, m, "", UpdateOptions{GameSlug: g.Slug}, nil)
	if err == nil || result.Downloaded != 1 || !PTRNeedsInstall(root) {
		t.Fatalf("partial completion: result=%+v, err=%v", result, err)
	}
	if err := os.Remove(cache); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(cache, "old.wdb")
	mustWrite(t, stale, []byte("stale"))
	for _, opts := range []UpdateOptions{{GameSlug: g.Slug, Verify: true}, {GameSlug: g.Slug, DryRun: true}} {
		if _, err := updater.Run(ctx, m, "", opts, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(stale); err != nil || !PTRNeedsInstall(root) {
		t.Fatalf("read-only mode completed a pending update: %v", err)
	}
	result, err = NewUpdater(api, nil, root).Run(ctx, m, "", UpdateOptions{GameSlug: g.Slug}, nil)
	if err != nil || result.Downloaded != 0 || api.downloadCalls != 1 {
		t.Fatalf("retry: result=%+v, %d downloads, err=%v", result, api.downloadCalls, err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retry retained stale cache: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, "invalid")); err != nil || PTRNeedsInstall(root) {
		t.Fatalf("retry did not make the game ready: %v", err)
	}
}

func TestUpdaterReadOnlyModesDoNotWrite(t *testing.T) {
	data := []byte("correct")
	api := &updaterAPI{data: map[string][]byte{"1": data}}
	root, u := setupUpdater(t, api)
	mustWrite(t, filepath.Join(root, "Data/enUS/realmlist.wtf"), []byte("old"))
	mustWrite(t, filepath.Join(root, "Cache/old.wdb"), []byte("cache"))
	before, err := os.Stat(filepath.Join(root, "Cache/old.wdb"))
	if err != nil {
		t.Fatal(err)
	}
	f := file("1", "Data/file", data, nil)
	mustWrite(t, filepath.Join(root, f.Path), data)
	for _, opts := range []UpdateOptions{{GameSlug: "retail", Verify: true}, {GameSlug: "retail", DryRun: true}} {
		if _, err := u.Run(context.Background(), manifest(nil, game(f)), "token", opts, nil); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.Stat(filepath.Join(root, "Cache/old.wdb"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("cache file replaced")
	}
	if _, err := os.Stat(filepath.Join(root, "Cache/invalid")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read-only run created cache invalidation")
	}
	realmlist, _ := os.ReadFile(filepath.Join(root, "Data/enUS/realmlist.wtf"))
	if string(realmlist) != "old" {
		t.Fatalf("realmlist changed: %q", realmlist)
	}
	if api.downloadCalls != 0 {
		t.Fatal("read-only mode downloaded")
	}
}

func TestUpdaterRemovesStaleCaseVariantOnlyOnUpdate(t *testing.T) {
	data := []byte("same")
	root, u := setupUpdater(t, &updaterAPI{})
	f := file("1", "Data/File.dat", data, nil)
	target := filepath.Join(root, f.Path)
	mustWrite(t, target, data)
	stale := filepath.Join(root, "Data/file.DAT")
	mustWrite(t, stale, []byte("stale"))
	alias := filepath.Join(root, "Data/FILE.dat")
	if err := os.Link(target, alias); err != nil {
		t.Fatal(err)
	}
	other := file("2", "Data/Other.dat", []byte("some other game file"), nil)
	mustWrite(t, filepath.Join(root, other.Path), []byte("some other game file"))
	unrelated := filepath.Join(root, "Data/fIlE.dat")
	if err := os.Link(filepath.Join(root, other.Path), unrelated); err != nil {
		t.Fatal(err)
	}
	m := manifest(nil, game(f, other))
	if _, err := u.Run(context.Background(), m, "", UpdateOptions{GameSlug: "retail", Verify: true}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("verify removed stale variant: %v", err)
	}
	res, err := u.Run(context.Background(), m, "", UpdateOptions{GameSlug: "retail"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("normal update retained stale variant")
	}
	if got, err := os.ReadFile(alias); err != nil || string(got) != string(data) {
		t.Fatalf("update did not preserve a hard-link alias of the current game file: %q, %v", got, err)
	}
	if _, err := os.Stat(unrelated); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("update retained a stale file linked to a different selected filename")
	}
	if !res.FilesUpdated {
		t.Fatal("stale cleanup should mark files updated")
	}
	if strings.Join(res.RemovedPaths, "|") != "Data/fIlE.dat|Data/file.DAT" {
		t.Fatalf("removed paths = %v", res.RemovedPaths)
	}
}

func TestUpdaterRefreshesOnceAndBoundsRenewed401(t *testing.T) {
	for _, second401 := range []bool{false, true} {
		t.Run(fmt.Sprint("renewed401=", second401), func(t *testing.T) {
			data := []byte("payload")
			api := &updaterAPI{data: map[string][]byte{"1": data}, urlErrors: map[string][]error{"1": {&HTTPError{StatusCode: 401}}}}
			if second401 {
				api.urlErrors["1"] = append(api.urlErrors["1"], &HTTPError{StatusCode: 403})
			}
			_, u := setupUpdater(t, api)
			authRoot := t.TempDir()
			store := &CredentialStore{Root: authRoot}
			seedToken(t, authRoot, "old")
			auth := &Authenticator{API: api, Store: store, Prompt: promptStub{}}
			u.auth = auth
			res, err := u.Run(context.Background(), manifest(nil, game(file("1", "x", data, nil))), "old", UpdateOptions{GameSlug: "retail"}, nil)
			if api.loginCalls != 1 || api.urlCalls["1"] != 2 {
				t.Fatalf("refresh calls login=%d URLs=%d", api.loginCalls, api.urlCalls["1"])
			}
			if second401 {
				if err == nil || len(res.FailedFiles) != 1 || !strings.Contains(res.FailedFiles[0].Reason, "HTTP 403") {
					t.Fatalf("renewed authorization failure not bounded: %+v %v", res, err)
				}
			} else if err != nil || res.Token != "renewed" {
				t.Fatalf("refresh failed: %+v %v", res, err)
			}
		})
	}
}

func TestUpdateRejectsRememberedLoginAsGameFile(t *testing.T) {
	api := &updaterAPI{}
	root, updater := setupUpdater(t, api)
	path := filepath.Join(root, savedLoginFile)
	const original = `{"username":"example","password":"synthetic-test-secret"}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	m := manifest(nil, game(file("1", savedLoginFile, []byte("not a game file"), nil)))
	if _, err := updater.Run(t.Context(), m, "token", UpdateOptions{GameSlug: "retail"}, nil); err == nil ||
		!strings.Contains(err.Error(), "reserved for launcher sign-in") {
		t.Fatalf("updater accepted a launcher credential as a game file: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != original {
		t.Fatal("update changed the saved launcher sign-in")
	}
}

type promptStub struct{}

func (promptStub) PromptLogin(context.Context, bool) (LoginCredentials, error) {
	return LoginCredentials{Username: "u", Password: "p"}, nil
}

func TestUpdaterRealmlistAndCacheAfterSuccess(t *testing.T) {
	data := []byte("payload")
	api := &updaterAPI{data: map[string][]byte{"1": data}}
	root, u := setupUpdater(t, api)
	mustWrite(t, filepath.Join(root, "Cache/a.wdb"), []byte("db"))
	mustWrite(t, filepath.Join(root, "Cache/keep.txt"), []byte("keep"))
	mustWrite(t, filepath.Join(root, "Cache/WDB/enUS/old.wdb"), []byte("nested"))
	mustWrite(t, filepath.Join(root, "Cache/WDB/enUS/keep.txt"), []byte("keep"))
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "linked.wdb"), []byte("outside"))
	if err := os.Symlink(outside, filepath.Join(root, "Cache/external")); err != nil {
		t.Fatal(err)
	}
	var events []Progress
	res, err := u.Run(context.Background(), manifest(nil, game(file("1", "Data/file", data, nil))), "", UpdateOptions{GameSlug: "retail"}, func(p Progress) {
		events = append(events, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.RealmlistUpdated || !res.FilesUpdated {
		t.Fatalf("result %+v", res)
	}
	removed := make(map[string]bool, len(res.RemovedPaths))
	for _, path := range res.RemovedPaths {
		removed[path] = true
	}
	if len(removed) != 2 || !removed["Cache/a.wdb"] || !removed["Cache/WDB/enUS/old.wdb"] {
		t.Fatalf("removed paths = %v", res.RemovedPaths)
	}
	foundRealmlist := false
	for _, event := range events {
		if event.Stage == "update" && event.Path == "Data/enUS/realmlist.wtf" {
			foundRealmlist = true
			break
		}
	}
	if !foundRealmlist {
		t.Fatal("realmlist update was not reported")
	}
	r, err := os.ReadFile(filepath.Join(root, "Data/enUS/realmlist.wtf"))
	if err != nil || string(r) != "set realmlist realm.example\n" {
		t.Fatalf("realmlist %q %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(root, "Cache/a.wdb")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("wdb was not cleared")
	}
	if _, err := os.Stat(filepath.Join(root, "Cache/invalid")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "Cache/keep.txt")); err != nil {
		t.Fatal("non-wdb cache removed")
	}
	if _, err := os.Stat(filepath.Join(root, "Cache/WDB/enUS/old.wdb")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("nested wdb was not cleared")
	}
	if _, err := os.Stat(filepath.Join(root, "Cache/WDB/enUS/keep.txt")); err != nil {
		t.Fatal("non-wdb file in nested cache was removed")
	}
	if _, err := os.Stat(filepath.Join(outside, "linked.wdb")); err != nil {
		t.Fatal("cache cleanup followed a symlink")
	}
}
