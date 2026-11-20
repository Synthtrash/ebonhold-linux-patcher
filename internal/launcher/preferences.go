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
	ProductionRoot     string       `json:"production_root,omitempty"`
	PTRRoot            string       `json:"ptr_root,omitempty"`
	GameSlug           string       `json:"game_slug"`
	ProductionLaunch   LaunchTarget `json:"production_launch,omitzero"`
	PTRLaunch          LaunchTarget `json:"ptr_launch,omitzero"`
	ProductionSettings GameSettings `json:"production_settings,omitzero"`
	PTRSettings        GameSettings `json:"ptr_settings,omitzero"`
	LauncherLanguage   string       `json:"launcher_language,omitempty"`
}

type GameSettings struct {
	Language         string `json:"language,omitempty"`
	ManageHD         bool   `json:"manage_hd,omitempty"`
	HDCharacters     bool   `json:"hd_characters,omitempty"`
	HDCreatures      bool   `json:"hd_creatures,omitempty"`
	UpdateBeforePlay bool   `json:"update_before_play,omitempty"`
	BottlesFlags     string `json:"bottles_flags,omitempty"`
}

// BottlesArguments returns one game argument per line, preserving internal spaces.
func (s GameSettings) BottlesArguments() []string {
	var args []string
	for line := range strings.SplitSeq(s.BottlesFlags, "\n") {
		if arg := strings.TrimSpace(line); arg != "" {
			args = append(args, arg)
		}
	}
	return args
}

func (p *Preferences) UnmarshalJSON(data []byte) error {
	type current Preferences
	var stored struct {
		current
		ClientRoot string `json:"client_root"`
		Manager    string `json:"manager"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return err
	}
	*p = Preferences(stored.current)
	if p.ProductionLaunch.Manager == "" && stored.Manager != "unknown" {
		p.ProductionLaunch.Manager = stored.Manager
	}
	if stored.ClientRoot != "" && p.ProductionRoot == "" {
		// The legacy folder's profile is unknowable; keep it out of PTR to avoid updating a shared Production install.
		p.ProductionRoot = stored.ClientRoot
	}
	return nil
}

func (p Preferences) RootForProfile(gameSlug string) string {
	if gameSlug == "roguelike" {
		return p.PTRRoot
	}
	return p.ProductionRoot
}

func (p *Preferences) SetRootForProfile(gameSlug, root string) {
	if p.RootForProfile(gameSlug) != root {
		target := p.LaunchTargetForProfile(gameSlug)
		target.ID = ""
		p.SetLaunchTargetForProfile(gameSlug, target)
	}
	if gameSlug == "roguelike" {
		p.PTRRoot = root
	} else {
		p.ProductionRoot = root
	}
}

func (p Preferences) LaunchTargetForProfile(gameSlug string) LaunchTarget {
	if gameSlug == "roguelike" {
		return p.PTRLaunch
	}
	return p.ProductionLaunch
}

func (p *Preferences) SetLaunchTargetForProfile(gameSlug string, target LaunchTarget) {
	if gameSlug == "roguelike" {
		p.PTRLaunch = target
	} else {
		p.ProductionLaunch = target
	}
}

func (p Preferences) ValidateLaunchTargetForProfile(gameSlug string, target LaunchTarget) error {
	if err := target.Validate(); err != nil {
		return err
	}
	if target.Manager == "steam" || target.Manager == "lutris" {
		other := p.PTRLaunch
		if gameSlug == "roguelike" {
			other = p.ProductionLaunch
		}
		if target == other {
			return errors.New("Production and PTR need different shortcuts in this manager")
		}
	}
	return nil
}

func (p Preferences) GameSettingsForProfile(gameSlug string) GameSettings {
	switch gameSlug {
	case "roguelike":
		return p.PTRSettings
	case "roguelike-prod":
		return p.ProductionSettings
	default:
		return GameSettings{}
	}
}

func (p *Preferences) SetGameSettingsForProfile(gameSlug string, settings GameSettings) {
	switch gameSlug {
	case "roguelike":
		p.PTRSettings = settings
	case "roguelike-prod":
		p.ProductionSettings = settings
	}
}

func (p Preferences) ValidateRootForProfile(gameSlug, root string) error {
	p.SetRootForProfile(gameSlug, root)
	return p.ValidateRoots()
}

func (p Preferences) ValidateRoots() error {
	if strings.TrimSpace(p.ProductionRoot) == "" || strings.TrimSpace(p.PTRRoot) == "" {
		return nil
	}
	production, err := resolvedRoot(p.ProductionRoot)
	if err != nil {
		return errors.New("could not resolve the Production folder")
	}
	ptr, err := resolvedRoot(p.PTRRoot)
	if err != nil {
		return errors.New("could not resolve the PTR folder")
	}
	prodInfo, prodErr := os.Stat(production)
	ptrInfo, ptrErr := os.Stat(ptr)
	if inside(production, ptr) || inside(ptr, production) || (prodErr == nil && ptrErr == nil && os.SameFile(prodInfo, ptrInfo)) {
		return errors.New("Production and PTR must use separate client folders")
	}
	return nil
}

func resolvedRoot(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		absolute = resolved
	}
	return filepath.Clean(absolute), nil
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
	if err := p.ValidateRoots(); err != nil {
		return err
	}
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

func (t LaunchTarget) Preset(launcherPath, clientRoot, gameSlug string, gameArgs ...string) string {
	if clientRoot == "" {
		return "Choose a client folder before applying this preset."
	}
	prefix := shellQuote(launcherPath) + " --client-dir=" + shellQuote(clientRoot) + " --game=" + shellQuote(gameSlug) + " --quiet"
	switch t.Manager {
	case "steam":
		return prefix + " -- %command%"
	case "lutris":
		return prefix + " --"
	case "bottles-flatpak", "bottles-native":
		if err := t.Validate(); err != nil {
			return ""
		}
		command := "bottles-cli"
		if t.Manager == "bottles-flatpak" {
			command = "flatpak run --command=bottles-cli com.usebottles.bottles"
		}
		var script strings.Builder
		script.WriteString("#!/bin/sh\nexec " + prefix + " -- \\\n  " + command + " run --bottle=" + shellQuote(t.ID) + " -e " + shellQuote(filepath.Join(clientRoot, "Wow.exe")))
		if len(gameArgs) > 0 {
			script.WriteString(" --")
			for _, arg := range gameArgs {
				script.WriteByte(' ')
				script.WriteString(shellQuote(arg))
			}
		}
		return script.String()
	default:
		return ""
	}
}
