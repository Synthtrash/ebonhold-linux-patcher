package launcher

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

const (
	installationLock = ".launcher-update.lock"
	cvarBackupSuffix = ".before-launcher-cvars"
	maxGameConfig    = 1 << 20
)

var cvarName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

type gameCVar struct{ name, value string }

// ApplyGameCVars sets only the supplied name=value pairs in a client's Config.wtf.
// Close the game first so it does not overwrite these settings on exit.
func ApplyGameCVars(root, input string) (bool, error) {
	if !filepath.IsAbs(root) {
		return false, errors.New("choose a game folder in Setup first")
	}
	if len(input) > 16<<10 {
		return false, errors.New("CVar input is too long")
	}
	var values []gameCVar
	seen := make(map[string]bool)
	lineNumber := 0
	for line := range strings.SplitSeq(input, "\n") {
		lineNumber++
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || !cvarName.MatchString(name) {
			return false, fmt.Errorf("line %d: use name=value", lineNumber)
		}
		if !utf8.ValidString(value) || strings.ContainsAny(value, `"\`) || strings.ContainsFunc(value, unicode.IsControl) {
			return false, fmt.Errorf("line %d: values cannot contain quotes, backslashes, or control characters", lineNumber)
		}
		key := strings.ToLower(name)
		switch key {
		case "locale", "realmlist", "realmlistbn", "patchlist", "realmname", "portal", "accountname", "accountlist", "accounttype":
			return false, fmt.Errorf("line %d: do not set realm, language, or sign-in values here", lineNumber)
		}
		if seen[key] {
			return false, fmt.Errorf("line %d: enter each CVar once", lineNumber)
		}
		seen[key] = true
		values = append(values, gameCVar{name, value})
	}
	if len(values) == 0 {
		return false, errors.New("enter at least one CVar")
	}
	unlock, err := lockFile(filepath.Join(root, installationLock), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		return false, err
	}
	defer unlock()
	return (&Updater{root: root}).writeGameCVars(values, true)
}

// The caller holds the installation lock. Preserve bytes outside the named SET lines.
func (u *Updater) writeGameCVars(values []gameCVar, backup bool) (bool, error) {
	path, err := u.destination(filepath.Join("WTF", "Config.wtf"), true)
	if err != nil {
		return false, err
	}
	info, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode().Perm()&0022 != 0 {
		return false, errors.New("WTF folder is unsafe or writable by other users")
	}
	old, err := readGameConfig(path)
	existed := err == nil
	if !existed && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	body := bytes.TrimPrefix(old, []byte("\xef\xbb\xbf"))
	eol := "\n"
	if i := bytes.IndexByte(body, '\n'); i > 0 && body[i-1] == '\r' {
		eol = "\r\n"
	}
	prefix := len(old) - len(body)
	out := append(make([]byte, 0, len(old)+48*len(values)), old[:prefix]...)
	written := make([]bool, len(values))
	for line := range bytes.Lines(body) {
		i := cvarLineIndex(line, values)
		if i < 0 {
			out = append(out, line...)
		} else if !written[i] {
			out = appendCVar(out, values[i])
			switch {
			case bytes.HasSuffix(line, []byte("\r\n")):
				out = append(out, "\r\n"...)
			case bytes.HasSuffix(line, []byte("\n")):
				out = append(out, '\n')
			}
			written[i] = true
		}
	}
	for i, v := range values {
		if written[i] {
			continue
		}
		if len(out) > prefix && out[len(out)-1] != '\n' {
			out = append(out, eol...)
		}
		out = append(appendCVar(out, v), eol...)
	}
	if bytes.Equal(old, out) {
		return false, nil
	}
	if len(out) > maxGameConfig {
		return false, errors.New("Config.wtf would be too large to edit")
	}
	if backup && existed {
		if err := writeAtomic(path+cvarBackupSuffix, old); err != nil {
			return false, fmt.Errorf("back up Config.wtf: %w", err)
		}
	}
	if err := writeAtomic(path, out); err != nil {
		return false, err
	}
	return true, nil
}

func readGameConfig(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("Config.wtf is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxGameConfig+1))
	if len(data) > maxGameConfig {
		return nil, errors.New("Config.wtf is too large to edit")
	}
	return data, err
}

func cvarLineIndex(line []byte, values []gameCVar) int {
	first := true
	for field := range bytes.FieldsSeq(line) {
		if !first {
			return slices.IndexFunc(values, func(v gameCVar) bool { return bytes.EqualFold(field, []byte(v.name)) })
		}
		if !bytes.EqualFold(field, []byte("SET")) {
			break
		}
		first = false
	}
	return -1
}

func appendCVar(out []byte, v gameCVar) []byte {
	out = append(out, "SET "...)
	out = append(out, v.name...)
	out = append(out, ` "`...)
	out = append(out, v.value...)
	return append(out, '"')
}
