package launcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

var errUnsafeInstallDirectory = errors.New("installation directory is unsafe")

func (s *CredentialStore) tokenPath() string     { return filepath.Join(s.Root, ".updaterToken") }
func (s *CredentialStore) credentialDir() string { return filepath.Join(s.Root, ".ebonhold-login") }
func (s *CredentialStore) credentialPath() string {
	return filepath.Join(s.credentialDir(), "credentials.json")
}

func (s *CredentialStore) readPrivate(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !ownedByCurrentUser(info) || info.Mode().Perm() != 0600 {
		return nil, errors.New("private credential file is unsafe")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		return nil, errors.New("private credential file has hard links")
	}
	return os.ReadFile(path)
}

func (s *CredentialStore) Token() (string, error) {
	b, err := s.readPrivate(s.tokenPath())
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	t := strings.TrimSpace(string(b))
	if t == "null" {
		return "", nil
	}
	return t, nil
}

func (s *CredentialStore) Credentials() (Credentials, bool) {
	var c Credentials
	dir, err := os.Lstat(s.credentialDir())
	if err != nil || !dir.IsDir() || dir.Mode()&os.ModeSymlink != 0 || !ownedByCurrentUser(dir) || dir.Mode().Perm() != 0700 {
		return c, false
	}
	b, err := s.readPrivate(s.credentialPath())
	if err != nil {
		return c, false
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	if dec.Decode(&c) != nil || c.Username == "" || c.Password == "" || strings.ContainsRune(c.Username, 0) || strings.ContainsRune(c.Password, 0) {
		return Credentials{}, false
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return Credentials{}, false
	}
	return c, true
}

func (s *CredentialStore) writePrivate(path string, data []byte) error {
	root, err := os.Lstat(s.Root)
	if err != nil || !root.IsDir() || root.Mode()&os.ModeSymlink != 0 || !ownedByCurrentUser(root) || root.Mode().Perm()&0022 != 0 {
		return errUnsafeInstallDirectory
	}
	if filepath.Base(path) == "credentials.json" {
		if err := os.Mkdir(s.credentialDir(), 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return errors.New("could not create credentials directory")
		}
		d, err := os.Lstat(s.credentialDir())
		if err != nil || !d.IsDir() || d.Mode()&os.ModeSymlink != 0 || !ownedByCurrentUser(d) || d.Mode().Perm() != 0700 {
			return errors.New("credentials directory is unsafe")
		}
	}
	if info, err := os.Lstat(path); err == nil {
		st, ok := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !ownedByCurrentUser(info) || !ok || st.Nlink != 1 {
			return errors.New("credential destination is unsafe")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("could not inspect credential destination")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".credential-*")
	if err != nil {
		return errors.New("could not create private credential")
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
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
	if err = os.Rename(tmp, path); err != nil {
		return errors.New("could not replace private credential")
	}
	return nil
}

func (s *CredentialStore) saveToken(token string) error {
	return s.writePrivate(s.tokenPath(), []byte(token))
}
func (s *CredentialStore) saveCredentials(c Credentials) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.writePrivate(s.credentialPath(), b)
}
func (s *CredentialStore) clearCredentials() error {
	if _, err := os.Lstat(s.credentialDir()); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	d, err := os.Lstat(s.credentialDir())
	if err != nil || !d.IsDir() || d.Mode()&os.ModeSymlink != 0 || !ownedByCurrentUser(d) || d.Mode().Perm() != 0700 {
		return errors.New("credentials directory is unsafe")
	}
	if _, err := os.Lstat(s.credentialPath()); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := s.readPrivate(s.credentialPath()); err != nil {
		return err
	}
	return os.Remove(s.credentialPath())
}

func authLockPath(root string) (string, error) {
	install, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	install, err = filepath.EvalSymlinks(install)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256([]byte(install))
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	var base string
	if runtime == "" {
		base = os.Getenv("TMPDIR")
		if base == "" {
			base = "/tmp"
		}
		base = filepath.Join(base, fmt.Sprintf("ebonhold-updater-%d", os.Geteuid()))
	} else {
		base = filepath.Join(runtime, "ebonhold-updater")
	}
	parent := filepath.Dir(base)
	if info, err := os.Lstat(parent); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("authentication lock parent is unsafe")
	}
	if err := os.Mkdir(base, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", errors.New("could not prepare authentication lock directory")
	}
	info, err := os.Lstat(base)
	if err != nil {
		return "", errors.New("could not inspect authentication lock directory")
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !ownedByCurrentUser(info) || info.Mode().Perm()&0022 != 0 {
		return "", errors.New("authentication lock directory is unsafe")
	}
	return filepath.Join(base, "auth-"+hex.EncodeToString(h[:])+".lock"), nil
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
	path, err := authLockPath(a.Store.Root)
	if err != nil {
		return "", Manifest{}, err
	}
	unlock, err := acquireFileLock(path)
	if err != nil {
		return "", Manifest{}, err
	}
	defer unlock()
	old, err := a.Store.Token()
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
	if !relogin {
		if c, ok := a.Store.Credentials(); ok {
			token, e := a.API.Login(ctx, c.Username, c.Password)
			if e != nil && !unauthorized(e) {
				return "", Manifest{}, errors.New("saved sign-in failed temporarily")
			}
			if e == nil {
				m, e := a.API.Games(ctx, token)
				if e != nil {
					return "", Manifest{}, fmt.Errorf("could not load manifest after saved sign-in: %w", e)
				}
				if !a.ReadOnly {
					if e = a.Store.saveToken(token); e != nil {
						return "", Manifest{}, errors.New("could not save authentication token")
					}
				}
				return token, m, nil
			}
		}
	}
	if a.Prompt == nil {
		return "", Manifest{}, errors.New("interactive sign-in is unavailable")
	}
	user, pass, remember, err := a.Prompt.PromptLogin(ctx, "", !a.ReadOnly)
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
		if err := a.Store.saveToken(token); err != nil {
			if remember && errors.Is(err, errUnsafeInstallDirectory) {
				a.Warning = "Remember me was not saved because the client folder is not safely owned or permissioned."
				return token, m, nil
			}
			return "", Manifest{}, errors.New("could not save authentication token")
		}
		if remember {
			if err := a.Store.saveCredentials(Credentials{Username: user, Password: pass}); err != nil {
				a.Warning = "Remember me was not saved; check the client folder's ownership and permissions."
			}
		} else if err := a.Store.clearCredentials(); err != nil {
			return "", Manifest{}, errors.New("could not clear saved local credentials")
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
	path, err := authLockPath(a.Store.Root)
	if err != nil {
		return err
	}
	unlock, err := acquireFileLock(path)
	if err != nil {
		return err
	}
	defer unlock()
	var failures []error
	if err := os.Remove(a.Store.tokenPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		failures = append(failures, err)
	}
	if err := a.Store.clearCredentials(); err != nil {
		failures = append(failures, fmt.Errorf("Could not clear saved local credentials: %w", err))
	}
	return errors.Join(failures...)
}
