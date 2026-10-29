package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Preferences struct {
	ClientRoot string `json:"client_root"`
	GameSlug   string `json:"game_slug"`
	Manager    string `json:"manager"`
}

type PreferencesStore struct {
	path string
}

func NewPreferencesStore() (*PreferencesStore, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		var err error
		base, err = os.UserConfigDir()
		if err != nil {
			return nil, errors.New("could not determine the user configuration directory")
		}
	}
	return &PreferencesStore{path: filepath.Join(base, "ebonhold-launcher", "config.json")}, nil
}

func (s *PreferencesStore) Load() (Preferences, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Preferences{}, nil
		}
		return Preferences{}, errors.New("could not read launcher preferences")
	}
	var p Preferences
	if err := json.Unmarshal(data, &p); err != nil {
		return Preferences{}, errors.New("launcher preferences are invalid; choose the client folder again")
	}
	return p, nil
}

func (s *PreferencesStore) Save(p Preferences) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("could not create launcher preferences directory")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !ownedByCurrentUser(info) {
		return errors.New("launcher preferences directory is unsafe")
	}
	if info.Mode().Perm() != 0700 {
		if err := os.Chmod(dir, 0700); err != nil {
			return errors.New("could not secure launcher preferences directory")
		}
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return errors.New("could not create temporary preferences file")
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return errors.New("could not secure preferences file")
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return errors.New("could not write launcher preferences")
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return errors.New("could not sync launcher preferences")
	}
	if err := tmp.Close(); err != nil {
		return errors.New("could not close launcher preferences")
	}
	if info, err := os.Lstat(s.path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !ownedByCurrentUser(info) {
			return errors.New("refusing an unsafe launcher preferences file")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return errors.New("could not inspect launcher preferences file")
	}
	if err := os.Rename(name, s.path); err != nil {
		return errors.New("could not save launcher preferences")
	}
	ok = true
	return nil
}

func ResolveLauncherRoot(argv0 string) (string, error) {
	if !strings.ContainsRune(argv0, filepath.Separator) {
		resolved, err := exec.LookPath(argv0)
		if err != nil {
			return "", errors.New("could not resolve the launcher executable path")
		}
		argv0 = resolved
	}
	abs, err := filepath.Abs(argv0)
	if err != nil {
		return "", errors.New("could not resolve the launcher directory")
	}
	root := filepath.Dir(abs)
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return root, nil
}

type Manager struct {
	ID        string
	Name      string
	Installed bool
	Details   string
}

func DetectManagers(ctx context.Context) []Manager {
	flatpaks := flatpakApplications(ctx)
	managers := []Manager{
		{ID: "steam", Name: "Steam", Installed: commandOrFlatpak("steam", "com.valvesoftware.Steam", flatpaks), Details: "Launch Options"},
		{ID: "lutris", Name: "Lutris", Installed: commandOrFlatpak("lutris", "net.lutris.Lutris", flatpaks), Details: "System options · Command prefix"},
		{ID: "bottles", Name: "Bottles", Installed: commandOrFlatpak("bottles", "com.usebottles.bottles", flatpaks), Details: "bottles-cli wrapper"},
		{ID: "unknown", Name: "Not sure yet", Installed: true, Details: "Setup instructions only"},
	}
	return managers
}

func flatpakApplications(ctx context.Context) map[string]bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "flatpak", "list", "--app", "--columns=application")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}
	apps := make(map[string]bool)
	for _, line := range strings.Split(string(output), "\n") {
		apps[strings.TrimSpace(line)] = true
	}
	return apps
}

func commandOrFlatpak(command, appID string, flatpaks map[string]bool) bool {
	if _, err := exec.LookPath(command); err == nil {
		return true
	}
	return flatpaks[appID]
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func ManagerPreset(manager, launcherPath, clientRoot string) string {
	if clientRoot == "" {
		return "Choose a client folder before applying this preset."
	}
	prefix := shellQuote(launcherPath) + " --client-dir=" + shellQuote(clientRoot) + " --quick --quiet"
	switch manager {
	case "steam":
		return prefix + " -- %command%"
	case "lutris":
		return prefix + " --"
	case "bottles-flatpak":
		return "#!/bin/sh\nexec " + prefix + " -- \\\n  flatpak run --command=bottles-cli com.usebottles.bottles run \\\n  -b 'REPLACE_WITH_BOTTLE_NAME' -e " + shellQuote("/path/to/Wow.exe")
	case "bottles-native":
		return "#!/bin/sh\nexec " + prefix + " -- \\\n  bottles-cli run -b 'REPLACE_WITH_BOTTLE_NAME' -e " + shellQuote("/path/to/Wow.exe")
	default:
		return ""
	}
}
