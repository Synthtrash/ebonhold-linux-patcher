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
	if !opts.DryRun {
		unlock, err := acquireFileLock(filepath.Join(u.root, installationLock))
		if err != nil {
			return result, fmt.Errorf("could not lock installation: %w", err)
		}
		defer unlock()
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
		if progress != nil {
			progress(Progress{Stage: "plan", Path: f.Path})
		}
	}
	if opts.DryRun {
		return result, nil
	}

	failed, newToken := u.downloadFiles(ctx, needed, result.Token, progress)
	result.Token = newToken
	result.Downloaded = len(needed) - len(failed)
	result.FilesUpdated = result.Downloaded > 0
	if len(failed) != 0 {
		result.FailedFiles = failed
		return result, fileFailuresError("failed to download", failed)
	}
	changed := result.FilesUpdated
	manifestPaths := make(map[string]bool, len(files))
	for _, f := range files {
		path, _ := u.destination(f.Path, false)
		manifestPaths[path] = true
	}
	for _, f := range files {
		path, _ := u.destination(f.Path, false)
		for _, stale := range removeCaseVariants(path, manifestPaths) {
			stale = u.relativePath(stale)
			result.RemovedPaths = append(result.RemovedPaths, stale)
			if progress != nil {
				progress(Progress{Stage: "remove", Path: stale})
			}
			changed = true
			result.FilesUpdated = true
		}
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
			path, err := u.destination(rel, true)
			if err != nil {
				return result, err
			}
			old, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) || (err == nil && !bytes.Equal(old, contents)) {
				if err := writeAtomic(path, contents); err != nil {
					return result, err
				}
				result.RealmlistUpdated = true
				changed = true
				if progress != nil {
					progress(Progress{Stage: "update", Path: rel})
				}
			} else if err != nil {
				return result, err
			}
		}
	}
	if locale != "" {
		updated, err := u.writeGameCVars([]gameCVar{{"locale", locale}}, false)
		if err != nil {
			return result, err
		}
		if updated {
			changed = true
			result.FilesUpdated = true
			if progress != nil {
				progress(Progress{Stage: "update", Path: "WTF/Config.wtf"})
			}
		}
	}
	if opts.ManageHD && !opts.Full && !opts.Quick {
		removed, err := u.disableHDPatches(options, selectedOptions, progress)
		result.RemovedPaths = append(result.RemovedPaths, removed...)
		if err != nil {
			return result, err
		}
		if len(removed) > 0 {
			changed = true
			result.FilesUpdated = true
		}
	}
	if changed {
		removed, err := u.clearCache(progress)
		result.RemovedPaths = append(result.RemovedPaths, removed...)
		if err != nil {
			return result, fmt.Errorf("updated files but could not clear cache: %w", err)
		}
	}
	if game.Slug == "roguelike" && !opts.Quick {
		if err := os.Remove(filepath.Join(u.root, ptrInstallMarker)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return result, fmt.Errorf("could not mark PTR installed: %w", err)
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
	var bad []FileFailure
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
			if progress != nil {
				for _, stale := range findCaseVariants(path, manifestPaths) {
					progress(Progress{Stage: "stale", Message: "case-only stale file", Path: stale, Current: i + 1, Total: len(files)})
				}
			}
		}
		if issue != "" {
			bad = append(bad, FileFailure{Path: f.Path, Reason: detail})
		}
		if progress != nil {
			progress(Progress{Stage: "verify", Path: f.Path, Current: i + 1, Total: len(files), Issue: issue, Message: detail})
		}
	}
	if len(bad) != 0 {
		result.FailedFiles = bad
		return result, fileFailuresError("verification failed", bad)
	}
	return result, nil
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

func isAuthError(err error) bool {
	var httpErr *HTTPError
	return errors.As(err, &httpErr) && (httpErr.StatusCode == 401 || httpErr.StatusCode == 403)
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

func (u *Updater) downloadFiles(ctx context.Context, files []ManifestFile, token string, progress ProgressFunc) ([]FileFailure, string) {
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
		case isAuthError(r.err):
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
	failed = append(failed, u.transfer(ctx, downloadJobs, progress)...)
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

func (u *Updater) transfer(ctx context.Context, jobs []downloadJob, progress ProgressFunc) []FileFailure {
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

func removeCaseVariants(target string, manifestPaths map[string]bool) []string {
	var removed []string
	for _, variant := range findCaseVariants(target, manifestPaths) {
		if err := os.Remove(variant); err == nil {
			removed = append(removed, variant)
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

func (u *Updater) relativePath(path string) string {
	relative, err := filepath.Rel(u.root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}

func (u *Updater) clearCache(progress ProgressFunc) ([]string, error) {
	path, err := u.destination("Cache", true)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, err
	}
	var removed []string
	if err := filepath.WalkDir(path, func(cachePath string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() || !strings.EqualFold(filepath.Ext(entry.Name()), ".wdb") {
			return nil
		}
		if err := os.Remove(cachePath); err != nil {
			return err
		}
		relative := u.relativePath(cachePath)
		removed = append(removed, relative)
		if progress != nil {
			progress(Progress{Stage: "remove", Path: relative})
		}
		return nil
	}); err != nil {
		return removed, err
	}
	invalid, err := u.destination("Cache/invalid", true)
	if err != nil {
		return removed, err
	}
	f, err := os.OpenFile(invalid, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return removed, err
	}
	if err := f.Close(); err != nil {
		return removed, err
	}
	return removed, nil
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
	path, err := u.destination(rel, false)
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

func (u *Updater) disableHDPatches(options []GameOption, enabled map[string]bool, progress ProgressFunc) ([]string, error) {
	var backup string
	var removed []string
	for _, option := range options {
		if enabled[option.Slug] || option.Kind != "hd-characters" && option.Kind != "hd-creatures" {
			continue
		}
		for _, rel := range option.Paths {
			if !strings.EqualFold(rel, "Data/patch-H.MPQ") && !strings.EqualFold(rel, "Data/patch-G.MPQ") {
				continue
			}
			path, err := u.destination(rel, false)
			if err != nil {
				return removed, err
			}
			candidates := findCaseVariants(path, nil)
			candidates = append(candidates, path)
			for _, candidate := range candidates {
				info, err := os.Lstat(candidate)
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					return removed, err
				}
				if !info.Mode().IsRegular() {
					return removed, fmt.Errorf("cannot disable unsafe HD file %s", u.relativePath(candidate))
				}
				if backup == "" {
					backup, err = os.MkdirTemp(u.root, ".ebonhold-disabled-hd-")
					if err != nil {
						return removed, fmt.Errorf("could not create HD patch backup: %w", err)
					}
				}
				savedPath := filepath.Join(backup, filepath.Base(candidate))
				if err := os.Rename(candidate, savedPath); err != nil {
					return removed, fmt.Errorf("could not move %s to HD patch backup: %w", u.relativePath(candidate), err)
				}
				removed = append(removed, u.relativePath(candidate))
				if progress != nil {
					progress(Progress{Stage: "remove", Path: u.relativePath(candidate), Message: "HD patch backed up to " + u.relativePath(savedPath)})
				}
			}
		}
	}
	return removed, nil
}
