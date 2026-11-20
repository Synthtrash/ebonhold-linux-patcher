package launcher

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

type Updater struct {
	api  API
	auth *Authenticator
	root string
}

func NewUpdater(api API, auth *Authenticator, root string) *Updater {
	return &Updater{api: api, auth: auth, root: root}
}

const cachePendingName = ".ebonhold-cache-pending"

var numericFileID = regexp.MustCompile(`^[1-9][0-9]*$`)

func (u *Updater) Run(ctx context.Context, manifest Manifest, token string, opts UpdateOptions, progress ProgressFunc) (result UpdateResult, runErr error) {
	result.Token = token
	var game *Game
	for i := range manifest.Data.Games {
		if manifest.Data.Games[i].Slug == opts.GameSlug {
			game = &manifest.Data.Games[i]
			break
		}
	}
	if game == nil {
		return result, fmt.Errorf("game %q not found in manifest", opts.GameSlug)
	}
	var selectedOptions map[string]bool
	var options []GameOption
	if len(opts.OptionSlugs) > 0 || opts.ManageHD || opts.HDCharacters || opts.HDCreatures || opts.Language != "" && opts.Language != "enUS" {
		selectedOptions = make(map[string]bool)
		options = AvailableGameOptions(manifest, game.Slug)
	}
	locale := opts.Language
	if locale != "" && locale != "enUS" && locale != "frFR" && locale != "deDE" && locale != "esES" {
		return result, fmt.Errorf("unsupported game language %q", locale)
	}
	var localeRealmlist string
	if locale != "" && locale != "enUS" {
		localeRealmlist = filepath.Join("Data", locale, "realmlist.wtf")
	}
	if locale != "" && locale != "enUS" {
		found := false
		for _, option := range options {
			if option.Kind == "language" && option.Locale == locale {
				selectedOptions[option.Slug] = true
				found = true
			}
		}
		if !found {
			if err := u.validateLocalePack(locale); err != nil {
				return result, fmt.Errorf("language %s has no manifest pack; install it using https://project-ebonhold.com/support/installation-guide: %w", locale, err)
			}
		}
	}
	for _, option := range options {
		if option.Kind == "hd-characters" && opts.HDCharacters || option.Kind == "hd-creatures" && opts.HDCreatures {
			selectedOptions[option.Slug] = true
		}
	}
	if opts.HDCharacters && !hasGameOption(options, "hd-characters") {
		return result, errors.New("character HD option is not available in the manifest")
	}
	if opts.HDCreatures && !hasGameOption(options, "hd-creatures") {
		return result, errors.New("creature HD option is not available in the manifest")
	}
	for _, slug := range opts.OptionSlugs {
		selectedOptions[slug] = true
	}
	for slug := range selectedOptions {
		found := false
		for _, option := range options {
			found = found || option.Slug == slug
		}
		if !found {
			return result, fmt.Errorf("game option %q not found in manifest", slug)
		}
	}
	files := make([]ManifestFile, 0, len(manifest.Data.Common.Files)+len(game.Files))
	appendSet := func(entries []ManifestFile, common bool) {
		for _, f := range entries {
			if strings.EqualFold(f.Path, "Data/enUS/realmlist.wtf") || localeRealmlist != "" && strings.EqualFold(f.Path, localeRealmlist) {
				continue
			}
			if opts.Full || (f.OptionSlug == nil && (!opts.Quick || (!common && patchPath(f.Path)))) || (f.OptionSlug != nil && selectedOptions[*f.OptionSlug]) {
				files = append(files, f)
			}
		}
	}
	appendSet(manifest.Data.Common.Files, true)
	appendSet(game.Files, false)
	if len(files) == 0 {
		return result, errors.New("no files found in manifest for game")
	}
	if err := u.validateFiles(files); err != nil {
		return result, err
	}
	if opts.Verify {
		return u.verify(ctx, files, result, progress)
	}
	var markCachePending func() error
	var installation *os.Root
	if !opts.DryRun {
		unlock, err := acquireFileLock(filepath.Join(u.root, installationLock))
		if err != nil {
			return result, fmt.Errorf("could not lock installation: %w", err)
		}
		defer unlock()
		installation, err = openInstallation(u.root)
		if err != nil {
			return result, err
		}
		defer installation.Close()
		if game.Slug != "roguelike" {
			if _, err := installation.Lstat(ptrInstallMarker); !errors.Is(err, os.ErrNotExist) {
				return result, errors.New("this folder needs its first PTR install; select the PTR profile before updating")
			}
		}
		info, err := installation.Lstat(cachePendingName)
		if err == nil && !info.Mode().IsRegular() {
			return result, errors.New("unsafe pending cache marker")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return result, fmt.Errorf("check pending cache refresh: %w", err)
		}
		cachePending := err == nil
		markCachePending = sync.OnceValue(func() error {
			if cachePending {
				return nil
			}
			marker, err := installation.OpenFile(cachePendingName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return fmt.Errorf("mark cache for refresh: %w", err)
			}
			cachePending = true
			return marker.Close()
		})
		// Keep the pending marker across runs until refresh succeeds, even on a partial update.
		defer func() {
			if cachePending {
				removed, err := clearCache(installation, progress)
				result.RemovedPaths = append(result.RemovedPaths, removed...)
				if err == nil {
					err = installation.Remove(cachePendingName)
				}
				if err != nil {
					runErr = errors.Join(runErr, fmt.Errorf("could not finish game cache refresh: %w", err))
				}
			}
			if runErr == nil {
				current, pathErr := os.Lstat(u.root)
				pinned, statErr := installation.Stat(".")
				if pathErr != nil || statErr != nil || !os.SameFile(current, pinned) {
					runErr = errors.New("game folder changed during the update")
				}
			}
			if runErr == nil && game.Slug == "roguelike" && !opts.Quick {
				if err := installation.Remove(ptrInstallMarker); err != nil && !errors.Is(err, os.ErrNotExist) {
					runErr = fmt.Errorf("could not mark PTR installed: %w", err)
				}
			}
		}()
	}

	needed := make([]ManifestFile, 0, len(files))
	for _, f := range files {
		path, err := u.destination(f.Path)
		if err != nil {
			return result, err
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			result.Missing++
			needed = append(needed, f)
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return result, fmt.Errorf("unsafe destination: %s", f.Path)
		}
		actual, err := fileMD5(path)
		if err != nil {
			return result, err
		}
		want, _ := manifestMD5(f.Hash)
		if actual != want {
			result.Mismatched++
			needed = append(needed, f)
		} else {
			result.Current++
		}
	}
	result.Checked = len(files)
	for _, f := range needed {
		result.PlannedPaths = append(result.PlannedPaths, f.Path)
		if progress != nil {
			progress(Progress{Stage: "plan", Path: f.Path})
		}
	}
	if opts.DryRun {
		return result, nil
	}
	failed, newToken := u.downloadFiles(ctx, installation, needed, result.Token, markCachePending, progress)
	result.Token = newToken
	result.Downloaded = len(needed) - len(failed)
	result.FilesUpdated = result.Downloaded > 0
	if len(failed) != 0 {
		result.FailedFiles = failed
		return result, fileFailuresError("failed to download", failed)
	}
	if err := visitStaleCaseVariants(ctx, installation, files, func(dir *os.File, stale string) error {
		if err := markCachePending(); err != nil {
			return err
		}
		if err := unix.Unlinkat(int(dir.Fd()), filepath.Base(stale), 0); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return fmt.Errorf("remove stale game file %s: %w", stale, err)
		}
		result.RemovedPaths = append(result.RemovedPaths, stale)
		result.FilesUpdated = true
		if progress != nil {
			progress(Progress{Stage: "remove", Path: stale})
		}
		return nil
	}); err != nil {
		return result, err
	}
	if locale != "" {
		if err := u.validateLocalePack(locale); err != nil {
			return result, err
		}
	}
	if game.Realmlist != "" {
		realmPaths := [2]string{"Data/enUS/realmlist.wtf", localeRealmlist}
		contents := []byte("set realmlist " + game.Realmlist + "\n")
		for _, rel := range realmPaths {
			if rel == "" {
				continue
			}
			updated, err := writeRealmlist(installation, rel, contents, markCachePending)
			if err != nil {
				return result, err
			}
			if updated {
				result.RealmlistUpdated = true
				if progress != nil {
					progress(Progress{Stage: "update", Path: rel})
				}
			}
		}
	}
	if locale != "" {
		updated, err := writeGameCVars(installation, []gameCVar{{"locale", locale}}, false, markCachePending)
		if err != nil {
			return result, err
		}
		if updated {
			result.FilesUpdated = true
			if progress != nil {
				progress(Progress{Stage: "update", Path: "WTF/Config.wtf"})
			}
		}
	}
	if opts.ManageHD && !opts.Full && !opts.Quick {
		removed, err := disableHDPatches(installation, options, selectedOptions, markCachePending, progress)
		result.RemovedPaths = append(result.RemovedPaths, removed...)
		result.FilesUpdated = result.FilesUpdated || len(removed) > 0
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func patchPath(path string) bool {
	for _, part := range strings.Split(strings.ToLower(filepath.ToSlash(path)), "/") {
		if strings.HasPrefix(part, "patch") {
			return true
		}
	}
	return false
}

func manifestMD5(encoded string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(b) != md5.Size {
		return "", errors.New("invalid manifest checksum")
	}
	return hex.EncodeToString(b), nil
}

func (u *Updater) validateFiles(files []ManifestFile) error {
	for _, f := range files {
		if !numericFileID.MatchString(f.ID) {
			return fmt.Errorf("invalid manifest file ID for %s", f.Path)
		}
		if _, err := manifestMD5(f.Hash); err != nil {
			return fmt.Errorf("invalid manifest checksum for %s", f.Path)
		}
		clean, err := safeRelativePath(f.Path)
		if err != nil {
			return fmt.Errorf("unsafe manifest path %q", f.Path)
		}
		top, _, _ := strings.Cut(clean, "/")
		if strings.EqualFold(top, savedLoginFile) || strings.EqualFold(top, tokenFile) {
			return fmt.Errorf("manifest path %q is reserved for launcher sign-in", f.Path)
		}
		if _, err := u.destination(clean); err != nil {
			return err
		}
	}
	return nil
}

func safeRelativePath(path string) (string, error) {
	if path == "" || filepath.IsAbs(path) || strings.ContainsAny(path, "|\x00\r\n") {
		return "", errors.New("unsafe path")
	}
	path = filepath.ToSlash(path)
	for _, p := range strings.Split(path, "/") {
		if p == "" || p == "." || p == ".." {
			return "", errors.New("unsafe path")
		}
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("unsafe path")
	}
	return clean, nil
}

// destination validates a path for readers. Writes use pinned directory handles.
func (u *Updater) destination(rel string) (string, error) {
	clean, err := safeRelativePath(rel)
	if err != nil {
		return "", fmt.Errorf("unsafe destination %q", rel)
	}
	root, err := filepath.Abs(u.root)
	if err != nil {
		return "", err
	}
	if info, err := os.Lstat(root); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return "", errors.New("unsafe installation root")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	current := root
	parts := strings.Split(clean, string(filepath.Separator))
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return "", statErr
		}
		if statErr == nil && (info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir())) {
			return "", fmt.Errorf("unsafe destination parent: %s", current)
		}
	}
	return current, nil
}

func fileMD5(path string) (string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file: %s", path)
	}
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (u *Updater) verify(ctx context.Context, files []ManifestFile, result UpdateResult, progress ProgressFunc) (UpdateResult, error) {
	var bad []FileFailure
	for i, f := range files {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		var got string
		path, err := u.destination(f.Path)
		if err == nil {
			got, err = fileMD5(path)
		}
		want, _ := manifestMD5(f.Hash)
		result.Checked++
		var issue, detail string
		switch {
		case errors.Is(err, os.ErrNotExist):
			result.Missing++
			issue, detail = "missing", "file is missing"
		case err != nil:
			result.Mismatched++
			issue, detail = "unreadable", err.Error()
		case got != want:
			result.Mismatched++
			issue, detail = "mismatch", "checksum mismatch"
		default:
			result.Current++
		}
		if issue != "" {
			bad = append(bad, FileFailure{Path: f.Path, Reason: detail})
		}
		if progress != nil {
			progress(Progress{Stage: "verify", Path: f.Path, Current: i + 1, Total: len(files), Issue: issue, Message: detail})
		}
	}
	var verifyErr error
	if len(bad) != 0 {
		result.FailedFiles = bad
		verifyErr = fileFailuresError("verification failed", bad)
	}
	if progress != nil {
		installation, err := openInstallation(u.root)
		if err != nil {
			return result, errors.Join(verifyErr, err)
		}
		defer installation.Close()
		err = visitStaleCaseVariants(ctx, installation, files, func(_ *os.File, stale string) error {
			progress(Progress{Stage: "stale", Message: "case-only stale file", Path: stale})
			return nil
		})
		return result, errors.Join(verifyErr, err)
	}
	return result, verifyErr
}

func fileFailuresError(summary string, failed []FileFailure) error {
	var report strings.Builder
	report.WriteString(summary)
	report.WriteString(":")
	for _, file := range failed {
		report.WriteByte('\n')
		report.WriteString(file.Path)
		report.WriteString(": ")
		report.WriteString(file.Reason)
	}
	return errors.New(report.String())
}

func downloadCause(err error) string {
	var status *HTTPError
	if errors.As(err, &status) {
		// The response message may include a signed URL. Only report the status.
		return strings.TrimSpace(fmt.Sprintf("HTTP %d %s", status.StatusCode, http.StatusText(status.StatusCode)))
	}
	var request *url.Error
	if errors.As(err, &request) {
		if request.Timeout() {
			return "network request timed out"
		}
		return "network request failed"
	}
	return err.Error()
}

type downloadJob struct {
	file ManifestFile
	url  string
}
type downloadResult struct {
	job downloadJob
	err error
}

func (u *Updater) downloadLink(ctx context.Context, token string, f ManifestFile) (downloadJob, error) {
	raw, err := u.api.DownloadURL(ctx, token, f.ID)
	if err == nil {
		err = validHTTPS(raw)
	}
	return downloadJob{file: f, url: raw}, err
}

func (u *Updater) downloadFiles(ctx context.Context, installation *os.Root, files []ManifestFile, token string, beforeWrite func() error, progress ProgressFunc) ([]FileFailure, string) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	results := make(chan downloadResult, len(files))
	for _, f := range files {
		wg.Add(1)
		go func(f ManifestFile) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			job, err := u.downloadLink(ctx, token, f)
			results <- downloadResult{job: job, err: err}
		}(f)
	}
	wg.Wait()
	close(results)
	var downloadJobs []downloadJob
	var authJobs []downloadResult
	var failed []FileFailure
	failLink := func(path string, err error) {
		failed = append(failed, FileFailure{Path: path, Reason: "request download link: " + downloadCause(err)})
	}
	for r := range results {
		switch {
		case r.err == nil:
			downloadJobs = append(downloadJobs, r.job)
		case unauthorized(r.err):
			authJobs = append(authJobs, r)
		default:
			failLink(r.job.file.Path, r.err)
		}
	}
	if len(authJobs) > 0 {
		if u.auth == nil {
			for _, r := range authJobs {
				failLink(r.job.file.Path, r.err)
			}
		} else {
			renewed, _, err := u.auth.Refresh(ctx)
			if err != nil || renewed == "" {
				if err == nil {
					err = errors.New("no session token returned")
				}
				reason := "renew sign-in: " + downloadCause(err)
				for _, r := range authJobs {
					failed = append(failed, FileFailure{Path: r.job.file.Path, Reason: reason})
				}
			} else {
				token = renewed
				for _, r := range authJobs {
					job, err := u.downloadLink(ctx, token, r.job.file)
					if err != nil {
						failLink(r.job.file.Path, err)
						continue
					}
					downloadJobs = append(downloadJobs, job)
				}
			}
		}
	}
	failed = append(failed, u.transfer(ctx, installation, downloadJobs, beforeWrite, progress)...)
	slices.SortFunc(failed, func(a, b FileFailure) int { return strings.Compare(a.Path, b.Path) })
	return failed, token
}

func validHTTPS(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || strings.ContainsAny(raw, "\r\n\x00") {
		return errors.New("refusing invalid or non-HTTPS download URL")
	}
	return nil
}

func (u *Updater) transfer(ctx context.Context, installation *os.Root, jobs []downloadJob, beforeWrite func() error, progress ProgressFunc) []FileFailure {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	var mu sync.Mutex
	var failed []FileFailure
	var progressMu sync.Mutex
	completed := 0
	for _, job := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			parent := filepath.Dir(job.file.Path)
			dir, err := openUpdateDir(installation, parent, unix.O_PATH, true)
			if err == nil {
				defer dir.Close()
				for attempt := 0; attempt < 3; attempt++ {
					err = downloadAtomic(ctx, u.api, job.url, dir, filepath.Base(job.file.Path), job.file.Hash, beforeWrite)
					if err == nil {
						err = checkUpdateDirectory(installation, parent, dir)
						break
					}
				}
			}
			if err != nil {
				reason := downloadCause(err)
				mu.Lock()
				failed = append(failed, FileFailure{Path: job.file.Path, Reason: reason})
				mu.Unlock()
			} else if progress != nil {
				progressMu.Lock()
				completed++
				progress(Progress{Stage: "download", Path: job.file.Path, Current: completed, Total: len(jobs)})
				progressMu.Unlock()
			}
		}()
	}
	wg.Wait()
	return failed
}

func downloadAtomic(ctx context.Context, api API, rawURL string, dir *os.File, name, encodedHash string, beforeWrite func() error) error {
	if err := validHTTPS(rawURL); err != nil {
		return err
	}
	want, err := manifestMD5(encodedHash)
	if err != nil {
		return err
	}
	return commitUpdateFile(dir, name, func(dst io.Writer) error {
		hash := md5.New()
		if err := api.Download(ctx, rawURL, io.MultiWriter(dst, hash)); err != nil {
			return err
		}
		if hex.EncodeToString(hash.Sum(nil)) != want {
			return errors.New("download checksum mismatch")
		}
		return nil
	}, beforeWrite)
}

func writeRealmlist(root *os.Root, rel string, contents []byte, beforeWrite func() error) (bool, error) {
	dir, err := openUpdateDir(root, filepath.Dir(rel), unix.O_PATH, true)
	if err != nil {
		return false, err
	}
	defer dir.Close()
	name := filepath.Base(rel)
	old, err := readUpdateBytes(dir, name)
	if err == nil && bytes.Equal(old, contents) {
		return false, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := writeUpdateBytes(dir, name, contents, beforeWrite); err != nil {
		return false, err
	}
	return true, nil
}

func clearCache(installation *os.Root, progress ProgressFunc) ([]string, error) {
	cache, err := openUpdateDir(installation, "Cache", os.O_RDONLY, true)
	if err != nil {
		return nil, err
	}
	defer cache.Close()
	var removed []string
	if err := removeCacheFiles(cache, "Cache", &removed, progress); err != nil {
		return removed, err
	}
	fd, err := unix.Openat(int(cache.Fd()), "invalid", os.O_CREATE|os.O_WRONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0644)
	if err != nil {
		return removed, &os.PathError{Op: "write cache marker", Path: "Cache/invalid", Err: err}
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return removed, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return removed, errors.New("unsafe game cache marker")
	}
	return removed, nil
}

func removeCacheFiles(dir *os.File, relative string, removed *[]string, progress ProgressFunc) error {
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return err
	}
	slices.Sort(names)
	parent := int(dir.Fd())
	for _, name := range names {
		path := filepath.Join(relative, name)
		var entry unix.Stat_t
		if err := unix.Fstatat(parent, name, &entry, unix.AT_SYMLINK_NOFOLLOW); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return &os.PathError{Op: "inspect game cache", Path: path, Err: err}
		}
		switch entry.Mode & unix.S_IFMT {
		case unix.S_IFDIR:
			fd, err := unix.Openat(parent, name, updateDirectoryFlags|unix.O_CLOEXEC, 0)
			if err != nil {
				return &os.PathError{Op: "open game cache", Path: path, Err: err}
			}
			child := os.NewFile(uintptr(fd), path)
			err = removeCacheFiles(child, path, removed, progress)
			child.Close()
			if err != nil {
				return err
			}
		case unix.S_IFREG:
			if !strings.EqualFold(filepath.Ext(name), ".wdb") {
				continue
			}
			if err := unix.Unlinkat(parent, name, 0); errors.Is(err, os.ErrNotExist) {
				continue
			} else if err != nil {
				return &os.PathError{Op: "remove cached game data", Path: path, Err: err}
			}
			*removed = append(*removed, path)
			if progress != nil {
				progress(Progress{Stage: "remove", Path: path})
			}
		}
	}
	return nil
}

func AvailableGameOptions(manifest Manifest, gameSlug string) []GameOption {
	bySlug := make(map[string]*GameOption)
	add := func(file ManifestFile) {
		if file.OptionSlug == nil {
			return
		}
		option := bySlug[*file.OptionSlug]
		if option == nil {
			option = &GameOption{Slug: *file.OptionSlug, Kind: "other"}
			bySlug[*file.OptionSlug] = option
		}
		option.Paths = append(option.Paths, file.Path)
		switch strings.ToLower(filepath.ToSlash(file.Path)) {
		case "data/patch-h.mpq":
			option.Kind = "hd-characters"
		case "data/patch-g.mpq":
			option.Kind = "hd-creatures"
		}
		parts := strings.Split(filepath.ToSlash(file.Path), "/")
		if len(parts) > 1 && strings.EqualFold(parts[0], "Data") {
			switch strings.ToLower(parts[1]) {
			case "enus":
				option.Kind, option.Locale = "language", "enUS"
			case "frfr":
				option.Kind, option.Locale = "language", "frFR"
			case "dede":
				option.Kind, option.Locale = "language", "deDE"
			case "eses":
				option.Kind, option.Locale = "language", "esES"
			}
		}
	}
	for _, file := range manifest.Data.Common.Files {
		add(file)
	}
	for _, game := range manifest.Data.Games {
		if game.Slug == gameSlug {
			for _, file := range game.Files {
				add(file)
			}
		}
	}
	options := make([]GameOption, 0, len(bySlug))
	for _, option := range bySlug {
		options = append(options, *option)
	}
	sort.Slice(options, func(i, j int) bool { return options[i].Slug < options[j].Slug })
	return options
}
func hasGameOption(options []GameOption, kind string) bool {
	for _, option := range options {
		if option.Kind == kind {
			return true
		}
	}
	return false
}

func (u *Updater) validateLocalePack(locale string) error {
	rel := filepath.Join("Data", locale)
	path, err := u.destination(rel)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("game language pack is missing under %s", rel)
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.EqualFold(filepath.Ext(entry.Name()), ".mpq") {
			return nil
		}
	}
	return fmt.Errorf("no game language files (*.MPQ) found under %s", rel)
}
