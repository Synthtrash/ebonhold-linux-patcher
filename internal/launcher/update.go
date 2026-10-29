package launcher

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

type Updater struct {
	api  API
	auth *Authenticator
	root string
}

func NewUpdater(api API, auth *Authenticator, root string) *Updater {
	return &Updater{api: api, auth: auth, root: root}
}

var numericFileID = regexp.MustCompile(`^[1-9][0-9]*$`)

func (u *Updater) Run(ctx context.Context, manifest Manifest, token string, opts UpdateOptions, progress ProgressFunc) (UpdateResult, error) {
	result := UpdateResult{Token: token}
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
	result.Game = *game
	files := make([]ManifestFile, 0, len(manifest.Data.Common.Files)+len(game.Files))
	appendSet := func(entries []ManifestFile, common bool) {
		for _, f := range entries {
			if strings.EqualFold(filepath.ToSlash(f.Path), "Data/enUS/realmlist.wtf") {
				continue
			}
			if opts.Full || (opts.Quick && !common && f.OptionSlug == nil && patchPath(f.Path)) || (!opts.Full && !opts.Quick && f.OptionSlug == nil) {
				files = append(files, f)
			}
		}
	}
	if !opts.Quick {
		appendSet(manifest.Data.Common.Files, true)
	}
	appendSet(game.Files, false)
	result.Files = files
	if len(files) == 0 {
		return result, errors.New("no files found in manifest for game")
	}
	if err := u.validateFiles(files); err != nil {
		return result, err
	}
	if opts.Verify {
		return u.verify(ctx, files, result, progress)
	}

	needed := make([]ManifestFile, 0, len(files))
	for _, f := range files {
		path, err := u.destination(f.Path, false)
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
	}
	if opts.DryRun {
		return result, nil
	}
	unlock, err := acquireFileLock(filepath.Join(u.root, ".launcher-update.lock"))
	if err != nil {
		return result, fmt.Errorf("could not lock installation: %w", err)
	}
	defer unlock()

	changed := false
	failed, newToken := u.downloadFiles(ctx, needed, result.Token, progress)
	result.Token = newToken
	if len(failed) != 0 {
		result.FailedPaths = failed
		return result, fmt.Errorf("failed to download: %s", strings.Join(failed, ", "))
	}
	if len(needed) > 0 {
		result.Downloaded = len(needed)
		result.FilesUpdated = true
		changed = true
	}
	manifestPaths := make(map[string]bool, len(files))
	for _, f := range files {
		path, _ := u.destination(f.Path, false)
		manifestPaths[path] = true
	}
	for _, f := range files {
		path, _ := u.destination(f.Path, false)
		if removeCaseVariants(path, manifestPaths) {
			changed = true
			result.FilesUpdated = true
		}
	}
	if game.Realmlist != "" {
		path, err := u.destination("Data/enUS/realmlist.wtf", true)
		if err != nil {
			return result, err
		}
		contents := []byte("set realmlist " + game.Realmlist + "\n")
		old, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) || (err == nil && strings.TrimRight(string(old), "\n") != strings.TrimSuffix(string(contents), "\n")) {
			if err := writeAtomic(path, contents); err != nil {
				return result, err
			}
			result.RealmlistUpdated = true
			changed = true
		} else if err != nil {
			return result, err
		}
	}
	if changed {
		if err := u.clearCache(); err != nil {
			return result, fmt.Errorf("updated files but could not clear cache: %w", err)
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
		if _, err := u.destination(clean, false); err != nil {
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

// destination checks existing components with Lstat so symlinks cannot redirect writes outside root.
func (u *Updater) destination(rel string, create bool) (string, error) {
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
		if errors.Is(statErr, os.ErrNotExist) && create && i < len(parts)-1 {
			if err := os.Mkdir(current, 0755); err != nil && !errors.Is(err, os.ErrExist) {
				return "", err
			}
			info, statErr = os.Lstat(current)
		}
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
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (u *Updater) verify(ctx context.Context, files []ManifestFile, result UpdateResult, progress ProgressFunc) (UpdateResult, error) {
	var bad []string
	manifestPaths := make(map[string]bool, len(files))
	for _, f := range files {
		path, _ := u.destination(f.Path, false)
		manifestPaths[path] = true
	}
	for i, f := range files {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		path, _ := u.destination(f.Path, false)
		got, err := fileMD5(path)
		want, _ := manifestMD5(f.Hash)
		result.Checked++
		if errors.Is(err, os.ErrNotExist) {
			result.Missing++
			bad = append(bad, f.Path)
		} else if err != nil || got != want {
			result.Mismatched++
			bad = append(bad, f.Path)
		} else {
			result.Current++
			if progress != nil {
				for _, stale := range findCaseVariants(path, manifestPaths) {
					progress(Progress{Stage: "stale", Message: "case-only stale file", Path: stale, Current: i + 1, Total: len(files)})
				}
			}
		}
		if progress != nil {
			progress(Progress{Stage: "verify", Path: f.Path, Current: i + 1, Total: len(files)})
		}
	}
	if len(bad) != 0 {
		result.FailedPaths = bad
		return result, fmt.Errorf("verification failed: %s", strings.Join(bad, ", "))
	}
	return result, nil
}

func isAuthError(err error) bool {
	var httpErr *HTTPError
	return errors.As(err, &httpErr) && (httpErr.StatusCode == 401 || httpErr.StatusCode == 403)
}

type downloadJob struct {
	file ManifestFile
	url  string
}
type downloadResult struct {
	job  downloadJob
	err  error
	auth bool
}

func (u *Updater) downloadFiles(ctx context.Context, files []ManifestFile, token string, progress ProgressFunc) ([]string, string) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	results := make(chan downloadResult, len(files))
	for _, f := range files {
		wg.Add(1)
		go func(f ManifestFile) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			url, err := u.api.DownloadURL(ctx, token, f.ID)
			if err != nil {
				results <- downloadResult{job: downloadJob{file: f}, err: err, auth: isAuthError(err)}
				return
			}
			if err := validHTTPS(url); err != nil {
				results <- downloadResult{job: downloadJob{file: f, url: url}, err: err}
				return
			}
			results <- downloadResult{job: downloadJob{file: f, url: url}}
		}(f)
	}
	wg.Wait()
	close(results)
	var downloadJobs []downloadJob
	var authJobs []downloadJob
	var failed []string
	for r := range results {
		if r.auth {
			authJobs = append(authJobs, r.job)
		} else if r.err != nil {
			failed = append(failed, r.job.file.Path)
		} else {
			downloadJobs = append(downloadJobs, r.job)
		}
	}
	if len(authJobs) > 0 {
		if u.auth == nil {
			for _, j := range authJobs {
				failed = append(failed, j.file.Path)
			}
		} else {
			renewed, _, err := u.auth.Refresh(ctx)
			if err != nil || renewed == "" {
				for _, j := range authJobs {
					failed = append(failed, j.file.Path)
				}
			} else {
				token = renewed
				for _, j := range authJobs {
					url, err := u.api.DownloadURL(ctx, token, j.file.ID)
					if err != nil || validHTTPS(url) != nil {
						failed = append(failed, j.file.Path)
						continue
					}
					j.url = url
					downloadJobs = append(downloadJobs, j)
				}
			}
		}
	}
	failed = append(failed, u.transfer(ctx, downloadJobs, progress)...)
	sort.Strings(failed)
	return failed, token
}

func validHTTPS(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || strings.ContainsAny(raw, "\r\n\x00") {
		return errors.New("refusing invalid or non-HTTPS download URL")
	}
	return nil
}

func (u *Updater) transfer(ctx context.Context, jobs []downloadJob, progress ProgressFunc) []string {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	var mu sync.Mutex
	var failed []string
	var progressMu sync.Mutex
	for i, job := range jobs {
		wg.Add(1)
		go func(i int, job downloadJob) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			path, err := u.destination(job.file.Path, true)
			if err == nil {
				for attempt := 0; attempt < 3; attempt++ {
					err = downloadAtomic(ctx, u.api, job.url, path, job.file.Hash)
					if err == nil {
						break
					}
				}
			}
			if err != nil {
				mu.Lock()
				failed = append(failed, job.file.Path)
				mu.Unlock()
			} else if progress != nil {
				progressMu.Lock()
				progress(Progress{Stage: "download", Path: job.file.Path, Current: i + 1, Total: len(jobs)})
				progressMu.Unlock()
			}
		}(i, job)
	}
	wg.Wait()
	return failed
}

func downloadAtomic(ctx context.Context, api API, rawURL, target, encodedHash string) error {
	if err := validHTTPS(rawURL); err != nil {
		return err
	}
	want, err := manifestMD5(encodedHash)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".launcher-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	err = api.Download(ctx, rawURL, tmp)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	got, err := fileMD5(name)
	if err != nil {
		return err
	}
	if got != want {
		return errors.New("download checksum mismatch")
	}
	if err := os.Rename(name, target); err != nil {
		return err
	}
	return nil
}

func findCaseVariants(target string, manifestPaths map[string]bool) []string {
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		return nil
	}
	base := filepath.Base(target)
	var variants []string
	for _, entry := range entries {
		variant := filepath.Join(filepath.Dir(target), entry.Name())
		if entry.Name() != base && strings.EqualFold(entry.Name(), base) && entry.Type().IsRegular() && !manifestPaths[variant] {
			variants = append(variants, variant)
		}
	}
	return variants
}

func removeCaseVariants(target string, manifestPaths map[string]bool) bool {
	removed := false
	for _, variant := range findCaseVariants(target, manifestPaths) {
		if err := os.Remove(variant); err == nil {
			removed = true
		}
	}
	return removed
}

func writeAtomic(target string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".launcher-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, target)
}

func (u *Updater) clearCache() error {
	path, err := u.destination("Cache", true)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0755); err != nil {
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.EqualFold(filepath.Ext(entry.Name()), ".wdb") && entry.Type().IsRegular() {
			if err := os.Remove(filepath.Join(path, entry.Name())); err != nil {
				return err
			}
		}
	}
	invalid, err := u.destination("Cache/invalid", true)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(invalid, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	return f.Close()
}
