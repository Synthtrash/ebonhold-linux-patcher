package launcher

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type clientSearchRoot struct {
	path     string
	maxDepth int
}

// FindClientRoots searches common game-manager install locations without walking the whole home directory.
// Matches require both the Data directory and Wow.exe; results are suggestions, not an automatic selection.
func FindClientRoots() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return nil
	}
	dataHome := userDataHome(home)
	roots := []clientSearchRoot{
		{filepath.Join(home, "Games"), 2},
		{filepath.Join(dataHome, "Steam", "steamapps", "common"), 1},
		{filepath.Join(home, ".steam", "steam", "steamapps", "common"), 1},
		{filepath.Join(home, ".steam", "root", "steamapps", "common"), 1},
		{filepath.Join(home, ".var", "app", "com.valvesoftware.Steam", ".local", "share", "Steam", "steamapps", "common"), 1},
		{filepath.Join(dataHome, "bottles", "bottles"), 4},
		{filepath.Join(home, ".var", "app", "com.usebottles.bottles", "data", "bottles", "bottles"), 4},
	}
	found := make(map[string]struct{})
	visited := make(map[string]struct{})
	for _, root := range roots {
		resolved, err := filepath.EvalSymlinks(root.path)
		if err != nil {
			continue
		}
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			continue
		}
		if _, ok := visited[resolved]; ok {
			continue
		}
		visited[resolved] = struct{}{}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			continue
		}
		_ = filepath.WalkDir(resolved, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry == nil || !entry.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(resolved, path)
			if err != nil {
				return nil
			}
			depth := 0
			if rel != "." {
				depth = strings.Count(rel, string(filepath.Separator)) + 1
			}
			if looksLikeClientRoot(path) {
				candidate, err := filepath.EvalSymlinks(path)
				if err == nil {
					candidate, err = filepath.Abs(candidate)
				}
				if err == nil {
					found[candidate] = struct{}{}
				}
				return filepath.SkipDir
			}
			if depth >= root.maxDepth {
				return filepath.SkipDir
			}
			return nil
		})
	}
	candidates := make([]string, 0, len(found))
	for candidate := range found {
		candidates = append(candidates, candidate)
	}
	sort.Strings(candidates)
	return candidates
}

func userDataHome(home string) string {
	if dataHome := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(dataHome) {
		return dataHome
	}
	return filepath.Join(home, ".local", "share")
}

func looksLikeClientRoot(path string) bool {
	data, err := os.Lstat(filepath.Join(path, "Data"))
	if err != nil || !data.IsDir() || data.Mode()&os.ModeSymlink != 0 {
		return false
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.Name() != "Wow.exe" {
			continue
		}
		info, err := os.Lstat(filepath.Join(path, entry.Name()))
		return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
	}
	return false
}
