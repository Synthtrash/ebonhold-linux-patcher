package launcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

func acquireFileLock(path string) (func() error, error) {
	return lockFile(path, syscall.LOCK_EX)
}

func lockFile(path string, mode int) (func() error, error) {
	parent := filepath.Dir(path)
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("lock directory is missing or unsafe")
	}
	if parentInfo.Mode().Perm()&0022 != 0 {
		return nil, fmt.Errorf("lock directory %q is writable by other users", parent)
	}

	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !ownedByCurrentUser(info) {
			return nil, errors.New("lock file is unsafe")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("could not inspect lock file")
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, errors.New("could not open lock file")
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !ownedByCurrentUser(info) {
		file.Close()
		return nil, errors.New("lock file is unsafe")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		file.Close()
		return nil, errors.New("lock file must not have hard links")
	}
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return nil, errors.New("could not secure lock file permissions")
	}
	if err := syscall.Flock(int(file.Fd()), mode); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.New("another launcher operation is using this game folder")
		}
		return nil, errors.New("could not lock installation")
	}

	var once sync.Once
	unlock := func() error {
		var unlockErr error
		once.Do(func() {
			unlockErr = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
			if err := file.Close(); unlockErr == nil {
				unlockErr = err
			}
		})
		return unlockErr
	}
	return unlock, nil
}

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}
