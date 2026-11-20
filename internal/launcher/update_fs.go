package launcher

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const updateDirectoryFlags = unix.O_DIRECTORY | unix.O_NOFOLLOW

func openInstallation(path string) (*os.Root, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("unsafe installation root")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	actual, err := root.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		root.Close()
		return nil, errors.New("installation root changed while opening it")
	}
	return root, nil
}

// openUpdateDir keeps a handle to the directory used for the final rename.
// Missing parents are created beneath the already pinned installation root.
func openUpdateDir(root *os.Root, rel string, mode int, create bool) (*os.File, error) {
	if !filepath.IsLocal(rel) || filepath.Clean(rel) != rel {
		return nil, fmt.Errorf("unsafe update directory: %q", rel)
	}
	if rel == "." {
		return root.OpenFile(".", mode|updateDirectoryFlags, 0)
	}
	base, err := root.OpenFile(".", unix.O_PATH|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	defer base.Close()
	baseFD := int(base.Fd())
	at := baseFD
	for remaining := rel; ; {
		part, rest, nested := strings.Cut(remaining, string(filepath.Separator))
		flags := mode | updateDirectoryFlags | unix.O_CLOEXEC
		if nested {
			flags = unix.O_PATH | updateDirectoryFlags | unix.O_CLOEXEC
		}
		fd, err := unix.Openat(at, part, flags, 0)
		if create && errors.Is(err, os.ErrNotExist) {
			if err = unix.Mkdirat(at, part, 0755); err == nil || errors.Is(err, os.ErrExist) {
				fd, err = unix.Openat(at, part, flags, 0)
			}
		}
		if at != baseFD {
			unix.Close(at)
		}
		if err != nil {
			return nil, &os.PathError{Op: "open update directory", Path: rel, Err: err}
		}
		if !nested {
			return os.NewFile(uintptr(fd), rel), nil
		}
		at, remaining = fd, rest
	}
}

type updateFileID struct{ device, inode uint64 }

func updateDirectoryID(file *os.File) (updateFileID, error) {
	var stat unix.Stat_t
	err := unix.Fstat(int(file.Fd()), &stat)
	return updateFileID{uint64(stat.Dev), uint64(stat.Ino)}, err
}

func checkUpdateDirectory(root *os.Root, rel string, pinned *os.File) error {
	current, err := openUpdateDir(root, rel, unix.O_PATH, false)
	if err != nil {
		return fmt.Errorf("reopen destination folder: %w", err)
	}
	defer current.Close()
	from, err := updateDirectoryID(pinned)
	if err != nil {
		return err
	}
	to, err := updateDirectoryID(current)
	if err != nil {
		return err
	}
	if from != to {
		return errors.New("destination folder changed during the update")
	}
	return nil
}

// commitUpdateFile stages on the destination filesystem in a directory only this
// user can write. The checked file and the final rename use pinned descriptors.
func commitUpdateFile(parent *os.File, name string, write func(io.Writer) error, beforeCommit func() error) error {
	at := int(parent.Fd())
	stageName := ".ebonhold-stage-" + rand.Text()
	if err := unix.Mkdirat(at, stageName, 0700); err != nil {
		return fmt.Errorf("create private staging directory: %w", err)
	}
	defer unix.Unlinkat(at, stageName, unix.AT_REMOVEDIR)
	stage, err := unix.Openat(at, stageName, updateDirectoryFlags|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open private staging directory: %w", err)
	}
	defer unix.Close(stage)
	var info unix.Stat_t
	if err := unix.Fstat(stage, &info); err != nil {
		return fmt.Errorf("check private staging directory: %w", err)
	}
	if info.Uid != uint32(os.Geteuid()) || info.Mode&0022 != 0 {
		return errors.New("staging directory is not private")
	}
	const payload = "payload"
	fd, err := unix.Openat(stage, payload, os.O_CREATE|os.O_EXCL|os.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return fmt.Errorf("create staged file: %w", err)
	}
	defer unix.Unlinkat(stage, payload, 0)
	file := os.NewFile(uintptr(fd), name)
	// Some mounted filesystems supply fixed modes instead of honoring 0700/0600.
	if info.Mode&0011 != 0 {
		if err := unix.Fstat(fd, &info); err != nil {
			file.Close()
			return fmt.Errorf("check staged file: %w", err)
		}
		if info.Mode&0022 != 0 || info.Uid != uint32(os.Geteuid()) {
			file.Close()
			return errors.New("staged file is writable by other users")
		}
	}
	err = write(file)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if beforeCommit != nil {
		if err := beforeCommit(); err != nil {
			return err
		}
	}
	if err := unix.Renameat(stage, payload, at, name); err != nil {
		return fmt.Errorf("replace %s: %w", name, err)
	}
	return nil
}

func readUpdateBytes(dir *os.File, name string) ([]byte, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, &os.PathError{Op: "read", Path: name, Err: err}
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxGameConfig+1))
	if len(data) > maxGameConfig {
		return nil, fmt.Errorf("%s is too large to edit", name)
	}
	return data, err
}

func writeUpdateBytes(dir *os.File, name string, data []byte, beforeWrite func() error) error {
	return commitUpdateFile(dir, name, func(dst io.Writer) error {
		_, err := dst.Write(data)
		return err
	}, beforeWrite)
}
