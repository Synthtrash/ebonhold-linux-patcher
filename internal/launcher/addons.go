package launcher

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type AddonStatus struct {
	Addon  Addon
	State  string
	Detail string
}

type AddonManager struct {
	api  API
	root string
}

type addonRecord struct {
	Name      string   `json:"name"`
	UpdatedAt string   `json:"updated_at"`
	Folders   []string `json:"folders"`
}

type addonState struct {
	Addons map[string]addonRecord `json:"addons"`
}

func NewAddonManager(api API, root string) *AddonManager {
	return &AddonManager{api: api, root: root}
}

func (m *AddonManager) Catalog(ctx context.Context, token string) ([]Addon, error) {
	items, err := m.api.Addons(ctx, token)
	if err != nil {
		return nil, err
	}
	seen := make(map[int64]bool, len(items))
	for _, addon := range items {
		if addon.ID <= 0 || strings.TrimSpace(addon.Name) == "" || seen[addon.ID] {
			return nil, errors.New("invalid addon catalog")
		}
		seen[addon.ID] = true
	}
	return items, nil
}

func splitAddonNames(value string) ([]string, error) {
	parts := strings.Split(value, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		if parts[i] == "" {
			return nil, errors.New("no addon names or IDs were provided")
		}
	}
	return parts, nil
}

func (m *AddonManager) Resolve(catalog []Addon, requested string) ([]int64, error) {
	parts, err := splitAddonNames(requested)
	if err != nil {
		return nil, err
	}

	ids := make([]int64, 0, len(parts))
	seen := make(map[int64]bool, len(parts))
	for _, part := range parts {
		var id int64
		matches := 0
		for _, addon := range catalog {
			if strconv.FormatInt(addon.ID, 10) == part || strings.EqualFold(addon.Name, part) {
				id = addon.ID
				matches++
			}
		}
		if matches != 1 {
			return nil, fmt.Errorf("unknown or ambiguous addon %q", part)
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	return ids, nil
}

func (m *AddonManager) addonsDir(create bool) (string, error) {
	root, err := os.Lstat(m.root)
	if err != nil || !root.IsDir() || root.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("unsafe installation directory")
	}

	interfaceDir := filepath.Join(m.root, "Interface")
	if _, err := os.Lstat(interfaceDir); errors.Is(err, os.ErrNotExist) && create {
		if err := os.Mkdir(interfaceDir, 0755); err != nil && !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	if info, err := os.Lstat(interfaceDir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("unsafe Interface directory")
	}

	addonsDir := filepath.Join(interfaceDir, "AddOns")
	if _, err := os.Lstat(addonsDir); errors.Is(err, os.ErrNotExist) && create {
		if err := os.Mkdir(addonsDir, 0755); err != nil && !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	if info, err := os.Lstat(addonsDir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("unsafe AddOns directory")
	}
	return addonsDir, nil
}

func (m *AddonManager) Installed() ([]AddonStatus, error) {
	_, unlock, err := m.locked()
	if err != nil {
		return nil, err
	}
	defer unlock()

	state, err := m.loadState()
	if err != nil {
		return nil, err
	}
	installed := make([]AddonStatus, 0, len(state.Addons))
	for rawID, record := range state.Addons {
		id, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil || id <= 0 {
			return nil, errors.New("invalid addon state ID")
		}
		installed = append(installed, AddonStatus{
			Addon: Addon{ID: id, Name: record.Name, UpdatedAt: record.UpdatedAt},
			State: "INSTALLED",
		})
	}
	sort.Slice(installed, func(i, j int) bool {
		return strings.ToLower(installed[i].Addon.Name) < strings.ToLower(installed[j].Addon.Name)
	})
	return installed, nil
}

func (m *AddonManager) Check(ctx context.Context, token string) ([]AddonStatus, error) {
	catalog, err := m.Catalog(ctx, token)
	if err != nil {
		return nil, err
	}
	addonsDir, unlock, err := m.locked()
	if err != nil {
		return nil, err
	}
	defer unlock()

	state, err := m.loadState()
	if err != nil {
		return nil, err
	}
	installedFolders, err := os.ReadDir(addonsDir)
	if err != nil {
		return nil, err
	}

	statuses := make([]AddonStatus, 0, len(catalog))
	for _, addon := range catalog {
		status := AddonStatus{Addon: addon, State: "NOT INSTALLED"}
		record, managed := state.Addons[strconv.FormatInt(addon.ID, 10)]
		if managed {
			if !m.managedAddonComplete(addonsDir, record) {
				status.State = "UPDATE AVAILABLE"
				status.Detail = "repair required"
			} else if record.UpdatedAt != addon.UpdatedAt {
				status.State = "UPDATE AVAILABLE"
			} else {
				status.State = "CURRENT"
			}
			statuses = append(statuses, status)
			continue
		}

		manualFolder := ""
		for _, entry := range installedFolders {
			if entry.IsDir() && strings.EqualFold(entry.Name(), addon.Name) {
				manualFolder = entry.Name()
				break
			}
		}
		if manualFolder != "" {
			path := filepath.Join(addonsDir, manualFolder)
			if hasFiles(path) {
				remoteTime, parseErr := time.Parse(time.RFC3339Nano, addon.UpdatedAt)
				localTime, found := newestFileTime(path)
				switch {
				case parseErr != nil || !found:
					status.State = "UNKNOWN"
					status.Detail = "could not compare addon timestamps"
				case remoteTime.After(localTime):
					status.State = "UPDATE AVAILABLE"
				default:
					status.State = "CURRENT"
				}
			}
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (m *AddonManager) managedAddonComplete(addonsDir string, record addonRecord) bool {
	if len(record.Folders) == 0 {
		return false
	}
	for _, folder := range record.Folders {
		if !safeFolder(folder) {
			return false
		}
		path := filepath.Join(addonsDir, folder)
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !hasFiles(path) {
			return false
		}
	}
	return true
}

func newestFileTime(root string) (time.Time, bool) {
	var newest time.Time
	found := false
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := os.Lstat(path)
		if err == nil && info.Mode().IsRegular() && (!found || info.ModTime().After(newest)) {
			newest = info.ModTime()
			found = true
		}
		return nil
	})
	return newest, err == nil && found
}

func (m *AddonManager) locked() (string, func() error, error) {
	addonsDir, err := m.addonsDir(true)
	if err != nil {
		return "", nil, err
	}
	unlock, err := acquireFileLock(filepath.Join(addonsDir, ".ebonhold-launcher.lock"))
	if err != nil {
		return "", nil, err
	}
	return addonsDir, unlock, nil
}

func (m *AddonManager) statePath() string {
	return filepath.Join(m.root, "Interface", "AddOns", ".ebonhold-launcher-addons.json")
}

func (m *AddonManager) loadState() (addonState, error) {
	state := addonState{Addons: make(map[string]addonRecord)}
	path := m.statePath()
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return state, errors.New("unsafe addon state file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return state, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil || state.Addons == nil {
		return addonState{}, errors.New("invalid addon state")
	}
	return state, nil
}

func (m *AddonManager) saveState(state addonState) error {
	path := m.statePath()
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("unsafe addon state file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	file, err := os.CreateTemp(filepath.Dir(path), ".ebonhold-launcher-addons.*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if err := json.NewEncoder(file).Encode(state); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func hasFiles(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func safeFolder(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, "/\\") &&
		!strings.ContainsFunc(name, func(r rune) bool { return r < 32 || r == 127 })
}

func validArchivePath(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\") ||
		strings.ContainsFunc(name, func(r rune) bool { return r < 32 || r == 127 }) {
		return false
	}
	name = strings.TrimSuffix(name, "/")
	if name == "" {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func extractZip(archive *zip.ReadCloser, stage string) ([]string, error) {
	tops := make(map[string]bool)
	for _, entry := range archive.File {
		if !validArchivePath(entry.Name) {
			return nil, errors.New("unsafe archive path")
		}
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || (!mode.IsDir() && !mode.IsRegular()) {
			return nil, errors.New("unsupported archive entry")
		}
		if !mode.IsDir() && !strings.Contains(entry.Name, "/") {
			return nil, errors.New("addon archive files must be inside a folder")
		}

		top := strings.SplitN(entry.Name, "/", 2)[0]
		tops[top] = true
		path := filepath.Join(stage, filepath.FromSlash(entry.Name))
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0755); err != nil {
				return nil, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, err
		}
		source, err := entry.Open()
		if err != nil {
			return nil, err
		}
		destination, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			source.Close()
			return nil, err
		}
		_, copyErr := io.Copy(destination, source)
		sourceErr := source.Close()
		destinationErr := destination.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if sourceErr != nil {
			return nil, sourceErr
		}
		if destinationErr != nil {
			return nil, destinationErr
		}
	}
	if len(tops) == 0 {
		return nil, errors.New("empty addon archive")
	}
	folders := make([]string, 0, len(tops))
	for folder := range tops {
		folders = append(folders, folder)
	}
	sort.Strings(folders)
	return folders, nil
}

func (m *AddonManager) Install(ctx context.Context, token string, ids []int64) error {
	if len(ids) == 0 {
		return errors.New("no addon IDs provided")
	}
	catalog, err := m.Catalog(ctx, token)
	if err != nil {
		return err
	}
	byID := make(map[int64]Addon, len(catalog))
	for _, addon := range catalog {
		byID[addon.ID] = addon
	}
	requested := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id <= 0 || byID[id].ID == 0 || requested[id] {
			return fmt.Errorf("invalid addon ID %d", id)
		}
		requested[id] = true
	}

	addonsDir, unlock, err := m.locked()
	if err != nil {
		return err
	}
	defer unlock()

	downloads, err := m.api.AddonDownloads(ctx, token, ids)
	if err != nil {
		return err
	}
	byFileID := make(map[int64]AddonDownload, len(downloads))
	for _, download := range downloads {
		if !requested[download.FileID] || byFileID[download.FileID].FileID != 0 ||
			!validDownloadURL(download.URL) ||
			!strings.HasPrefix(download.Filename, "Interface/AddOns/") ||
			!strings.HasSuffix(download.Filename, ".zip") || !validArchivePath(download.Filename) {
			return errors.New("invalid addon download response")
		}
		byFileID[download.FileID] = download
	}
	if len(byFileID) != len(ids) {
		return errors.New("incomplete addon download response")
	}
	state, err := m.loadState()
	if err != nil {
		return err
	}

	for _, id := range ids {
		if err := m.installOne(ctx, addonsDir, state, byID[id], byFileID[id]); err != nil {
			return err
		}
	}
	return nil
}

func (m *AddonManager) installOne(ctx context.Context, addonsDir string, state addonState, addon Addon, download AddonDownload) error {
	tempZip, err := os.CreateTemp("", "ebonhold-addon-*.zip")
	if err != nil {
		return err
	}
	zipPath := tempZip.Name()
	if err := m.api.Download(ctx, download.URL, tempZip); err != nil {
		tempZip.Close()
		os.Remove(zipPath)
		return err
	}
	if err := tempZip.Close(); err != nil {
		os.Remove(zipPath)
		return err
	}
	defer os.Remove(zipPath)

	archive, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp(addonsDir, ".ebonhold-addon-stage.")
	if err != nil {
		archive.Close()
		return err
	}
	defer os.RemoveAll(stage)
	folders, extractErr := extractZip(archive, stage)
	archiveErr := archive.Close()
	if extractErr != nil {
		return extractErr
	}
	if archiveErr != nil {
		return archiveErr
	}

	for _, folder := range folders {
		if !safeFolder(folder) {
			return errors.New("unsafe addon folder")
		}
		path := filepath.Join(addonsDir, folder)
		info, err := os.Lstat(path)
		if err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.IsDir()) {
			return errors.New("unsafe addon destination")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if sharedFolder(state, addon.ID, folder) {
			return fmt.Errorf("cannot replace shared addon folder: %s", folder)
		}
	}

	old := state.Addons[strconv.FormatInt(addon.ID, 10)]
	replace := make([]string, 0, len(folders)+len(old.Folders))
	for _, folder := range folders {
		replace = appendUnique(replace, folder)
	}
	for _, folder := range old.Folders {
		if !safeFolder(folder) {
			return errors.New("unsafe addon state folder")
		}
		if !containsFolder(folders, folder) && !sharedFolder(state, addon.ID, folder) {
			replace = appendUnique(replace, folder)
		}
	}

	backup, err := os.MkdirTemp(addonsDir, ".ebonhold-addon-backup.")
	if err != nil {
		return err
	}
	defer os.RemoveAll(backup)
	var moved, installed []string
	rollback := func() {
		for _, folder := range installed {
			_ = os.RemoveAll(filepath.Join(addonsDir, folder))
		}
		for i := len(moved) - 1; i >= 0; i-- {
			folder := moved[i]
			_ = os.Rename(filepath.Join(backup, folder), filepath.Join(addonsDir, folder))
		}
	}
	for _, folder := range replace {
		path := filepath.Join(addonsDir, folder)
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			rollback()
			return err
		}
		if err := os.Rename(path, filepath.Join(backup, folder)); err != nil {
			rollback()
			return err
		}
		moved = append(moved, folder)
	}
	for _, folder := range folders {
		if err := os.Rename(filepath.Join(stage, folder), filepath.Join(addonsDir, folder)); err != nil {
			rollback()
			return err
		}
		installed = append(installed, folder)
	}

	candidate := cloneAddonState(state)
	candidate.Addons[strconv.FormatInt(addon.ID, 10)] = addonRecord{
		Name:      addon.Name,
		UpdatedAt: addon.UpdatedAt,
		Folders:   folders,
	}
	if err := m.saveState(candidate); err != nil {
		rollback()
		return err
	}
	state.Addons[strconv.FormatInt(addon.ID, 10)] = candidate.Addons[strconv.FormatInt(addon.ID, 10)]
	return nil
}

func appendUnique(folders []string, folder string) []string {
	if containsFolder(folders, folder) {
		return folders
	}
	return append(folders, folder)
}

func cloneAddonState(state addonState) addonState {
	copy := addonState{Addons: make(map[string]addonRecord, len(state.Addons))}
	for id, record := range state.Addons {
		record.Folders = append([]string(nil), record.Folders...)
		copy.Addons[id] = record
	}
	return copy
}

func containsFolder(folders []string, want string) bool {
	for _, folder := range folders {
		if folder == want {
			return true
		}
	}
	return false
}

func sharedFolder(state addonState, id int64, folder string) bool {
	for key, record := range state.Addons {
		if key == strconv.FormatInt(id, 10) {
			continue
		}
		if containsFolder(record.Folders, folder) {
			return true
		}
	}
	return false
}

func validDownloadURL(raw string) bool {
	return requireHTTPS(raw) == nil && !strings.ContainsFunc(raw, func(r rune) bool {
		return r < 32 || r == 127
	})
}

func (m *AddonManager) Remove(requested string) ([]string, error) {
	parts, err := splitAddonNames(requested)
	if err != nil {
		return nil, err
	}
	addonsDir, unlock, err := m.locked()
	if err != nil {
		return nil, err
	}
	defer unlock()

	state, err := m.loadState()
	if err != nil {
		return nil, err
	}
	ids, err := resolveInstalledIDs(state, parts)
	if err != nil {
		return nil, err
	}
	backupRoot, err := m.removedAddonBackupRoot()
	if err != nil {
		return nil, err
	}

	var messages []string
	for _, rawID := range ids {
		id, _ := strconv.ParseInt(rawID, 10, 64)
		record := state.Addons[rawID]
		if !safeFolder(record.Name) {
			return messages, errors.New("unsafe addon name in state")
		}
		for _, folder := range record.Folders {
			if !safeFolder(folder) {
				return messages, errors.New("unsafe addon state folder")
			}
		}

		restoreDir, moved, err := m.moveAddonToBackup(addonsDir, backupRoot, state, id, record, &messages)
		if err != nil {
			return messages, err
		}
		candidate := cloneAddonState(state)
		delete(candidate.Addons, rawID)
		if err := m.saveState(candidate); err != nil {
			for i := len(moved) - 1; i >= 0; i-- {
				_ = os.Rename(filepath.Join(restoreDir, moved[i]), filepath.Join(addonsDir, moved[i]))
			}
			_ = os.Remove(restoreDir)
			return messages, err
		}
		state = candidate
		if restoreDir != "" {
			messages = append(messages, "Restore location: "+restoreDir)
		}
	}
	return messages, nil
}

func resolveInstalledIDs(state addonState, requested []string) ([]string, error) {
	ids := make([]string, 0, len(requested))
	seen := make(map[string]bool, len(requested))
	for _, part := range requested {
		matched := ""
		for rawID, record := range state.Addons {
			id, err := strconv.ParseInt(rawID, 10, 64)
			if err != nil || id <= 0 {
				return nil, errors.New("invalid addon state ID")
			}
			if rawID == part || strings.EqualFold(record.Name, part) {
				if matched != "" {
					return nil, errors.New("ambiguous addon")
				}
				matched = rawID
			}
		}
		if matched == "" {
			return nil, fmt.Errorf("addon %q is not installed via the launcher", part)
		}
		if !seen[matched] {
			ids = append(ids, matched)
			seen[matched] = true
		}
	}
	return ids, nil
}

func (m *AddonManager) removedAddonBackupRoot() (string, error) {
	path := filepath.Join(m.root, ".ebonhold-removed-addons")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0700); err != nil {
			return "", err
		}
		info, err = os.Lstat(path)
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		!ownedByCurrentUser(info) || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("unsafe addon backup directory")
	}
	return path, nil
}

func (m *AddonManager) moveAddonToBackup(addonsDir, backupRoot string, state addonState, id int64, record addonRecord, messages *[]string) (string, []string, error) {
	var restoreDir string
	var moved []string
	rollback := func() {
		for i := len(moved) - 1; i >= 0; i-- {
			folder := moved[i]
			_ = os.Rename(filepath.Join(restoreDir, folder), filepath.Join(addonsDir, folder))
		}
		if restoreDir != "" {
			_ = os.Remove(restoreDir)
		}
	}

	for _, folder := range record.Folders {
		if sharedFolder(state, id, folder) {
			*messages = append(*messages, folder+" is shared; left in place")
			continue
		}
		source := filepath.Join(addonsDir, folder)
		info, err := os.Lstat(source)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			rollback()
			return "", nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			rollback()
			return "", nil, errors.New("unsafe addon folder")
		}
		if restoreDir == "" {
			restoreDir, err = createAddonRestoreDir(backupRoot, record.Name)
			if err != nil {
				rollback()
				return "", nil, err
			}
		}
		if err := os.Rename(source, filepath.Join(restoreDir, folder)); err != nil {
			rollback()
			return "", nil, err
		}
		moved = append(moved, folder)
		*messages = append(*messages, fmt.Sprintf("Moved %s to %s", folder, restoreDir))
	}
	return restoreDir, moved, nil
}

func createAddonRestoreDir(root, addonName string) (string, error) {
	stamp := time.Now().Format("20060102-150405")
	for suffix := 0; ; suffix++ {
		name := fmt.Sprintf("%s-%s", addonName, stamp)
		if suffix > 0 {
			name = fmt.Sprintf("%s-%s-%d", addonName, stamp, suffix)
		}
		path := filepath.Join(root, name)
		err := os.Mkdir(path, 0700)
		if err == nil {
			return path, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
}
