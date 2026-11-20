package launcher

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const legacyPasswordDir = ".ebonhold-login"
const legacyPasswordFile = "credentials.json"

const tokenFile = ".updaterToken"

func (s *CredentialStore) Token() (string, error) {
	install, err := openInstallation(s.Root)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer install.Close()
	dir, err := openUpdateDir(install, ".", unix.O_PATH, false)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	return readToken(dir)
}

func readToken(dir *os.File) (string, error) {
	fd, err := unix.Openat(int(dir.Fd()), tokenFile, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	f := os.NewFile(uintptr(fd), tokenFile)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || !ownedByCurrentUser(info) || info.Mode().Perm() != 0600 {
		return "", errors.New("cached token file is unsafe")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		return "", errors.New("cached token file has hard links")
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	t := strings.TrimSpace(string(b))
	if t == "null" {
		return "", nil
	}
	return t, nil
}

func requirePrivateInstallation(install *os.Root) error {
	info, err := install.Stat(".")
	if err != nil {
		return err
	}
	if !ownedByCurrentUser(info) || info.Mode().Perm()&0022 != 0 {
		return errors.New("installation directory is unsafe")
	}
	return nil
}

func saveToken(install *os.Root, token string) error {
	if err := requirePrivateInstallation(install); err != nil {
		return err
	}
	if info, err := install.Lstat(tokenFile); err == nil {
		st, ok := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !ownedByCurrentUser(info) || !ok || st.Nlink != 1 {
			return errors.New("credential destination is unsafe")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("could not inspect credential destination")
	}
	tmp := ".ebonhold-token-" + rand.Text()
	f, err := install.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("could not create private credential")
	}
	defer install.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.WriteString(token)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return errors.New("could not write private credential")
	}
	if err = install.Rename(tmp, tokenFile); err != nil {
		return errors.New("could not replace private credential")
	}
	return nil
}

// RemoveLegacyPassword removes an old password from a valid game folder without reading it.
func (s *CredentialStore) RemoveLegacyPassword() error {
	install, err := openInstallation(s.Root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not inspect saved game folder: %w", err)
	}
	defer install.Close()
	var file unix.Stat_t
	legacy, err := inspectLegacyPassword(install, &file)
	if legacy == nil {
		return err
	}
	legacy.Close()
	locked, err := lockAuthentication(install)
	if err != nil {
		return fmt.Errorf("could not check for an old saved password: %w", err)
	}
	defer locked.Close()
	return removeLegacyPassword(install, true)
}

func inspectLegacyPassword(install *os.Root, file *unix.Stat_t) (*os.File, error) {
	dir, err := openUpdateDir(install, legacyPasswordDir, unix.O_PATH, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not inspect %s: %w", legacyPasswordDir, err)
	}
	if err := unix.Fstatat(int(dir.Fd()), legacyPasswordFile, file, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		dir.Close()
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("could not inspect %s/%s: %w", legacyPasswordDir, legacyPasswordFile, err)
	}
	return dir, nil
}

func removeLegacyPassword(install *os.Root, requireGame bool) error {
	var file unix.Stat_t
	dir, err := inspectLegacyPassword(install, &file)
	if dir == nil {
		return err
	}
	defer dir.Close()
	const confirmed = legacyPasswordDir + "/" + legacyPasswordFile + " exists"
	info, err := dir.Stat()
	if err != nil {
		return fmt.Errorf("%s but its directory could not be inspected: %w", confirmed, err)
	}
	if !ownedByCurrentUser(info) || info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("%s but its directory is unsafe", confirmed)
	}
	if file.Mode&unix.S_IFMT != unix.S_IFREG || file.Uid != uint32(os.Geteuid()) || file.Nlink != 1 {
		return fmt.Errorf("%s but the file is unsafe", confirmed)
	}
	if err := requirePrivateInstallation(install); err != nil {
		return fmt.Errorf("%s but cannot be removed: %w", confirmed, err)
	}
	if requireGame {
		data, dataErr := install.Lstat("Data")
		exe, exeErr := install.Lstat("Wow.exe")
		if dataErr != nil || !data.IsDir() || exeErr != nil || !exe.Mode().IsRegular() {
			return fmt.Errorf("%s but the folder no longer contains Data/ and a regular Wow.exe", confirmed)
		}
	}
	if err := unix.Unlinkat(int(dir.Fd()), legacyPasswordFile, 0); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s but could not be removed: %w", confirmed, err)
	}
	return nil
}

// Lock the opened directory so GUI and CLI share the lock even in different
// runtime environments. This also leaves read-only game folders unchanged.
func lockAuthentication(install *os.Root) (*os.File, error) {
	dir, err := openUpdateDir(install, ".", os.O_RDONLY, false)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(dir.Fd()), unix.LOCK_EX); err != nil {
		dir.Close()
		return nil, fmt.Errorf("could not lock game folder for authentication: %w", err)
	}
	return dir, nil
}

func (a *Authenticator) Authenticate(ctx context.Context, relogin bool) (string, Manifest, error) {
	return a.authenticate(ctx, relogin, false)
}
func (a *Authenticator) Refresh(ctx context.Context) (string, Manifest, error) {
	return a.authenticate(ctx, false, true)
}

func (a *Authenticator) authenticate(ctx context.Context, relogin, force bool) (string, Manifest, error) {
	a.Warning = ""
	if a.API == nil || a.Store == nil {
		return "", Manifest{}, errors.New("authentication is not configured")
	}
	install, err := openInstallation(a.Store.Root)
	if err != nil {
		return "", Manifest{}, err
	}
	defer install.Close()
	dir, err := lockAuthentication(install)
	if err != nil {
		return "", Manifest{}, err
	}
	defer dir.Close()
	if !a.ReadOnly {
		if err := removeLegacyPassword(install, false); err != nil {
			a.Warning = fmt.Sprintf("Could not remove the old saved password at %s: %v", filepath.Join(a.Store.Root, legacyPasswordDir, legacyPasswordFile), err)
		}
	}
	old, err := readToken(dir)
	if err != nil {
		return "", Manifest{}, errors.New("cached authentication token is unsafe")
	}
	if !force && !relogin && old != "" {
		m, e := a.API.Games(ctx, old)
		if e == nil {
			return old, m, nil
		}
		if !unauthorized(e) {
			return "", Manifest{}, errors.New("could not verify cached authentication")
		}
	}
	if a.Prompt == nil {
		return "", Manifest{}, errors.New("interactive sign-in is unavailable")
	}
	user, pass, err := a.Prompt.PromptLogin(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return "", Manifest{}, err
		}
		return "", Manifest{}, errors.New("could not prompt for sign-in")
	}
	token, err := a.API.Login(ctx, user, pass)
	if err != nil {
		return "", Manifest{}, errors.New("sign-in failed")
	}
	m, err := a.API.Games(ctx, token)
	if err != nil {
		return "", Manifest{}, errors.New("could not load manifest after sign-in")
	}
	if !a.ReadOnly {
		if err := saveToken(install, token); err != nil {
			return "", Manifest{}, fmt.Errorf("could not save authentication token: %w", err)
		}
	}
	return token, m, nil
}

func unauthorized(err error) bool {
	var h *HTTPError
	return errors.As(err, &h) && (h.StatusCode == 401 || h.StatusCode == 403)
}

func (a *Authenticator) Forget() error {
	if a.Store == nil {
		return errors.New("credential store is not configured")
	}
	install, err := openInstallation(a.Store.Root)
	if err != nil {
		return err
	}
	defer install.Close()
	dir, err := lockAuthentication(install)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := requirePrivateInstallation(install); err != nil {
		return err
	}
	var failures []error
	if err := unix.Unlinkat(int(dir.Fd()), tokenFile, 0); err != nil && !errors.Is(err, os.ErrNotExist) {
		failures = append(failures, fmt.Errorf("remove cached token: %w", err))
	}
	if err := removeLegacyPassword(install, false); err != nil {
		failures = append(failures, fmt.Errorf("could not remove the old saved password: %w", err))
	}
	return errors.Join(failures...)
}
