package launcher

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/unix"
)

func disableHDPatches(installation *os.Root, options []GameOption, enabled map[string]bool, beforeWrite func() error, progress ProgressFunc) ([]string, error) {
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
			moved, err := backUpHDPatch(installation, rel, &backup, beforeWrite, progress)
			removed = append(removed, moved...)
			if err != nil {
				return removed, err
			}
		}
	}
	return removed, nil
}

func backUpHDPatch(installation *os.Root, rel string, backup *string, beforeWrite func() error, progress ProgressFunc) ([]string, error) {
	parent, base := filepath.Dir(rel), filepath.Base(rel)
	source, err := openUpdateDir(installation, parent, os.O_RDONLY, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer source.Close()
	entries, err := source.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	slices.Sort(entries)
	from := int(source.Fd())
	var files []string
	for _, name := range entries {
		if !strings.EqualFold(name, base) {
			continue
		}
		var stat unix.Stat_t
		if err := unix.Fstatat(from, name, &stat, unix.AT_SYMLINK_NOFOLLOW); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, &os.PathError{Op: "inspect HD file for backup", Path: filepath.Join(parent, name), Err: err}
		}
		if stat.Mode&unix.S_IFMT != unix.S_IFREG {
			return nil, fmt.Errorf("cannot disable unsafe HD file %s", filepath.Join(parent, name))
		}
		files = append(files, name)
	}
	if len(files) == 0 {
		return nil, nil
	}
	if *backup == "" {
		name := ".ebonhold-disabled-hd-" + rand.Text()
		if err := installation.Mkdir(name, 0700); err != nil {
			return nil, fmt.Errorf("could not create HD patch backup: %w", err)
		}
		*backup = name
	}
	dst, err := openUpdateDir(installation, filepath.Join(*backup, parent), unix.O_PATH, true)
	if err != nil {
		return nil, err
	}
	defer dst.Close()
	info, err := dst.Stat()
	if err != nil {
		return nil, err
	}
	if !ownedByCurrentUser(info) || info.Mode().Perm()&0022 != 0 {
		return nil, errors.New("unsafe HD backup directory")
	}
	to := int(dst.Fd())
	var removed []string
	for _, name := range files {
		candidate := filepath.Join(parent, name)
		if err := beforeWrite(); err != nil {
			return removed, err
		}
		err := unix.Renameat2(from, name, to, name, unix.RENAME_NOREPLACE)
		if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP) {
			var existing unix.Stat_t
			if err = unix.Fstatat(to, name, &existing, unix.AT_SYMLINK_NOFOLLOW); errors.Is(err, os.ErrNotExist) {
				err = unix.Renameat(from, name, to, name)
			} else if err == nil {
				err = os.ErrExist
			}
		}
		if err != nil {
			return removed, fmt.Errorf("could not move %s to HD patch backup: %w", candidate, err)
		}
		removed = append(removed, candidate)
		if progress != nil {
			progress(Progress{Stage: "remove", Path: candidate, Message: "HD patch backed up to " + filepath.Join(*backup, candidate)})
		}
	}
	return removed, nil
}
