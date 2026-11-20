package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// LaunchTarget identifies a user-selected entry in a game manager.
type LaunchTarget struct {
	Manager string `json:"manager,omitempty"`
	ID      string `json:"id,omitempty"`
}

func (t LaunchTarget) Validate() error {
	switch t.Manager {
	case "bottles-native", "bottles-flatpak":
		if strings.TrimSpace(t.ID) == "" || strings.ContainsAny(t.ID, "\x00\r\n") {
			return errors.New("enter the exact bottle name")
		}
	case "steam", "lutris":
		id, err := strconv.ParseUint(t.ID, 10, 64)
		if err != nil || id == 0 || strconv.FormatUint(id, 10) != t.ID {
			return errors.New("enter the numeric game ID from the manager's shortcut")
		}
	default:
		return errors.New("choose a launch manager in Setup")
	}
	return nil
}

// Command builds a direct handoff to the manager, without changing its settings.
func (t LaunchTarget) Command(ctx context.Context, root string, gameArgs ...string) (*exec.Cmd, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	var cmd *exec.Cmd
	switch t.Manager {
	case "steam":
		cmd = exec.CommandContext(ctx, "xdg-open", "steam://rungameid/"+t.ID)
	case "lutris":
		cmd = exec.CommandContext(ctx, "xdg-open", "lutris:rungameid/"+t.ID)
	case "bottles-native", "bottles-flatpak":
		for _, arg := range gameArgs {
			if strings.ContainsRune(arg, 0) {
				return nil, errors.New("a game argument contains a NUL byte")
			}
		}
		args := []string{"run", "--bottle=" + t.ID, "--executable=" + filepath.Join(root, "Wow.exe")}
		if len(gameArgs) > 0 {
			args = append(append(args, "--"), gameArgs...)
		}
		cmd = bottlesCommand(ctx, t.Manager, args...)
		cmd.Dir = root
		cmd.Env = gameEnvironment(cmd.Environ())
	}
	if cmd.Err != nil {
		return nil, cmd.Err
	}
	return cmd, nil
}

func bottlesCommand(ctx context.Context, manager string, args ...string) *exec.Cmd {
	if manager == "bottles-flatpak" {
		return exec.CommandContext(ctx, "flatpak", append([]string{"run", "--command=bottles-cli", "com.usebottles.bottles"}, args...)...)
	}
	return exec.CommandContext(ctx, "bottles-cli", args...)
}

// ListBottles reads the names of existing bottles from the selected Bottles installation.
func ListBottles(ctx context.Context, manager string) ([]string, error) {
	if manager != "bottles-native" && manager != "bottles-flatpak" {
		return nil, errors.New("choose a Bottles installation")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	output, err := bottlesCommand(ctx, manager, "--json", "list", "bottles").Output()
	if err != nil {
		return nil, err
	}
	var bottles map[string]struct{}
	if err := json.Unmarshal(output, &bottles); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(bottles))
	for name := range bottles {
		if (LaunchTarget{Manager: manager, ID: name}).Validate() == nil {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names, nil
}

// GameShortcut is an existing Steam or Lutris entry for the selected game files.
type GameShortcut struct {
	ID   string
	Name string
}

// ListGameShortcuts looks for existing entries that point to this client's Wow.exe.
func ListGameShortcuts(ctx context.Context, manager, root string) []GameShortcut {
	if !filepath.IsAbs(root) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var shortcuts []GameShortcut
	switch manager {
	case "steam":
		shortcuts = steamShortcuts(ctx, root)
	case "lutris":
		shortcuts = lutrisShortcuts(ctx, root)
	}
	slices.SortFunc(shortcuts, func(a, b GameShortcut) int {
		if cmp := strings.Compare(a.Name, b.Name); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.ID, b.ID)
	})
	seen := make(map[string]bool, len(shortcuts))
	return slices.DeleteFunc(shortcuts, func(shortcut GameShortcut) bool {
		if seen[shortcut.ID] || (LaunchTarget{Manager: manager, ID: shortcut.ID}).Validate() != nil ||
			strings.TrimSpace(shortcut.Name) == "" || strings.ContainsAny(shortcut.Name, "\x00\r\n") {
			return true
		}
		seen[shortcut.ID] = true
		return false
	})
}

func matchesClientExecutable(root, exe, dir string) bool {
	if exe == "" {
		return false
	}
	expandPath := func(path string) string {
		if unquoted, ok := strings.CutPrefix(path, `"`); ok {
			path, _ = strings.CutSuffix(unquoted, `"`)
		}
		if relative, ok := strings.CutPrefix(path, "~/"); ok {
			if home, err := os.UserHomeDir(); err == nil {
				path = filepath.Join(home, relative)
			}
		}
		return path
	}
	exe = expandPath(exe)
	if !filepath.IsAbs(exe) {
		dir = expandPath(dir)
		if !filepath.IsAbs(dir) {
			return false
		}
		exe = filepath.Join(dir, exe)
	}
	// Compare paths, not inodes: distinct profiles may contain hard-linked files.
	target, err := filepath.EvalSymlinks(filepath.Join(root, "Wow.exe"))
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(exe)
	return err == nil && resolved == target
}

func readManagerFile(path string, limit int64) []byte {
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil
	}
	return data
}

func gameEnvironment(env []string) []string {
	env = slices.DeleteFunc(env, func(value string) bool {
		return strings.HasPrefix(value, "TZ=") ||
			strings.HasPrefix(value, "PROTON_FORCE_LARGE_ADDRESS_AWARE=") ||
			strings.HasPrefix(value, "WINE_LARGE_ADDRESS_AWARE=")
	})
	return append(env, "PROTON_FORCE_LARGE_ADDRESS_AWARE=1", "WINE_LARGE_ADDRESS_AWARE=1")
}
