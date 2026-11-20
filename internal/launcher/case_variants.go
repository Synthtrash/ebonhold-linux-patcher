package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/sys/unix"
)

// visitStaleCaseVariants groups selected names by directory identity and scans
// each folder once. The visitor uses the same pinned directory as the scan.
func visitStaleCaseVariants(ctx context.Context, root *os.Root, files []ManifestFile, visit func(*os.File, string) error) error {
	type directory struct {
		path  string
		id    updateFileID
		names map[string]bool
	}
	byPath := make(map[string]*directory)
	byID := make(map[updateFileID]*directory)
	var dirs []*directory
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		parent := filepath.Dir(file.Path)
		dir, known := byPath[parent]
		if !known {
			folder, err := openUpdateDir(root, parent, unix.O_PATH, false)
			if errors.Is(err, os.ErrNotExist) {
				byPath[parent] = nil
				continue
			}
			if err != nil {
				return fmt.Errorf("inspect game folder: %w", err)
			}
			id, err := updateDirectoryID(folder)
			folder.Close()
			if err != nil {
				return fmt.Errorf("inspect game folder: %w", err)
			}
			dir = byID[id]
			if dir == nil {
				dir = &directory{path: parent, id: id, names: make(map[string]bool)}
				byID[id] = dir
				dirs = append(dirs, dir)
			}
			byPath[parent] = dir
		}
		if dir != nil {
			dir.names[filepath.Base(file.Path)] = true
		}
	}
	for _, dir := range dirs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visitDirectoryVariants(root, dir.path, dir.id, dir.names, visit); err != nil {
			return err
		}
	}
	return nil
}

func visitDirectoryVariants(root *os.Root, parent string, id updateFileID, names map[string]bool, visit func(*os.File, string) error) error {
	dir, err := openUpdateDir(root, parent, os.O_RDONLY, false)
	if err != nil {
		return err
	}
	defer dir.Close()
	current, err := updateDirectoryID(dir)
	if err != nil || current != id {
		return fmt.Errorf("game folder changed during case cleanup: %s", parent)
	}
	entries, err := dir.Readdirnames(-1)
	if err != nil {
		return fmt.Errorf("list game files for case cleanup: %w", err)
	}
	slices.Sort(entries)
	folded := make(map[string][]string, len(names))
	for name := range names {
		key := caseKey(name)
		folded[key] = append(folded[key], name)
	}
	checked := make(map[string][]updateFileID)
	fd := int(dir.Fd())
	var stat unix.Stat_t
	for _, name := range entries {
		if names[name] {
			continue
		}
		key := caseKey(name)
		selected := folded[key]
		if len(selected) == 0 {
			continue
		}
		if err := unix.Fstatat(fd, name, &stat, unix.AT_SYMLINK_NOFOLLOW); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return &os.PathError{Op: "check case-only game file", Path: filepath.Join(parent, name), Err: err}
		}
		if stat.Mode&unix.S_IFMT != unix.S_IFREG {
			continue
		}
		other := updateFileID{uint64(stat.Dev), uint64(stat.Ino)}
		ids, known := checked[key]
		if !known {
			for _, target := range selected {
				if err := unix.Fstatat(fd, target, &stat, unix.AT_SYMLINK_NOFOLLOW); errors.Is(err, os.ErrNotExist) {
					continue
				} else if err != nil {
					return &os.PathError{Op: "check selected game file", Path: filepath.Join(parent, target), Err: err}
				}
				if stat.Mode&unix.S_IFMT == unix.S_IFREG {
					ids = append(ids, updateFileID{uint64(stat.Dev), uint64(stat.Ino)})
				}
			}
			checked[key] = ids
		}
		if len(ids) != 0 && !slices.Contains(ids, other) {
			if err := visit(dir, filepath.Join(parent, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func caseKey(name string) string {
	return strings.Map(func(r rune) rune {
		key := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			key = min(next, key)
		}
		return key
	}, name)
}
