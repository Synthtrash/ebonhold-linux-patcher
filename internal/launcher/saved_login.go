package launcher

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

const (
	savedLoginFile     = ".ebonhold-saved-login"
	savedLoginMagic    = "EL-login-v1\x00"
	maxSavedLoginBytes = 64 << 10
	savedLoginStateDir = "ebonhold-launcher"
	savedLoginKeyFile  = ".ebonhold-credential-key"
	savedLoginKeySize  = 32
)

func readSavedLogin(install *os.Root, dir *os.File) (LoginCredentials, error) {
	f, err := openPrivateCredential(dir, savedLoginFile)
	if errors.Is(err, os.ErrNotExist) {
		return LoginCredentials{}, nil
	}
	if err != nil {
		return LoginCredentials{}, err
	}
	defer f.Close()
	if err := requirePrivateInstallation(install); err != nil {
		return LoginCredentials{}, err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxSavedLoginBytes+1))
	if err != nil {
		return LoginCredentials{}, err
	}
	sealed, ok := bytes.CutPrefix(data, []byte(savedLoginMagic))
	if !ok || len(data) > maxSavedLoginBytes {
		return LoginCredentials{}, errors.New("invalid saved launcher sign-in")
	}
	aead, err := savedLoginCipher(install, false)
	if err != nil {
		return LoginCredentials{}, err
	}
	data, err = aead.Open(sealed[:0], nil, sealed, []byte(savedLoginMagic))
	var login LoginCredentials
	if err != nil || json.Unmarshal(data, &login) != nil || login.Username == "" || login.Password == "" {
		return LoginCredentials{}, errors.New("invalid saved launcher sign-in")
	}
	return login, nil
}

func saveLogin(install *os.Root, login LoginCredentials) error {
	data, err := json.Marshal(login)
	if err != nil || len(data) > maxSavedLoginBytes || login.Username == "" || login.Password == "" {
		return errors.New("invalid launcher sign-in")
	}
	return writePrivateCredential(install, savedLoginFile, func(w io.Writer) error {
		aead, err := savedLoginCipher(install, true)
		if err != nil {
			return err
		}
		size := len(savedLoginMagic) + len(data) + aead.Overhead()
		if size > maxSavedLoginBytes {
			return errors.New("invalid launcher sign-in")
		}
		header := make([]byte, len(savedLoginMagic), size)
		copy(header, savedLoginMagic)
		_, err = w.Write(aead.Seal(header, nil, data, []byte(savedLoginMagic)))
		return err
	})
}

func savedLoginCipher(install *os.Root, create bool) (cipher.AEAD, error) {
	state, err := openSavedLoginState(install, create)
	if err != nil {
		return nil, err
	}
	defer state.Close()
	info, err := state.Stat(".")
	if err != nil {
		return nil, err
	}
	game, err := install.Stat(".")
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm() != 0700 || !ownedByCurrentUser(info) || os.SameFile(info, game) {
		return nil, errors.New("launcher key directory is unsafe")
	}
	// Different game folders share this key; serialize first-time creation.
	dir, err := lockAuthentication(state)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	var key [savedLoginKeySize + 1]byte
	f, err := openPrivateCredential(dir, savedLoginKeyFile)
	switch {
	case create && errors.Is(err, os.ErrNotExist):
		rand.Read(key[:savedLoginKeySize])
		if err := writePrivateCredential(state, savedLoginKeyFile, func(w io.Writer) error {
			_, err := w.Write(key[:savedLoginKeySize])
			return err
		}); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		defer f.Close()
		if n, err := io.ReadFull(f, key[:]); n != savedLoginKeySize || err != io.ErrUnexpectedEOF {
			return nil, errors.New("invalid launcher key")
		}
	}
	block, err := aes.NewCipher(key[:savedLoginKeySize])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func openSavedLoginState(install *os.Root, create bool) (*os.Root, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(base) {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		if !filepath.IsAbs(home) {
			return nil, errors.New("home directory must be absolute")
		}
		base = filepath.Join(home, ".local", "state")
	}
	game, err := filepath.EvalSymlinks(install.Name())
	if err != nil {
		return nil, err
	}
	game, err = filepath.Abs(game)
	if err != nil {
		return nil, err
	}
	if create {
		if err := os.MkdirAll(base, 0700); err != nil {
			return nil, err
		}
	}
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(base, savedLoginStateDir)
	if inside(game, path) || inside(path, game) {
		return nil, errors.New("keep launcher state separate from the game folder")
	}
	if create {
		if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	return openInstallation(path)
}
