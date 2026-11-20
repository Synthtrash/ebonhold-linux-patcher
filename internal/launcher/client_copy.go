package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const ptrInstallMarker = ".ebonhold-ptr-needs-update"

// PTRNeedsInstall reports whether a local Production copy still needs its first PTR update.
func PTRNeedsInstall(root string) bool {
	_, err := os.Lstat(filepath.Join(root, ptrInstallMarker))
	return !errors.Is(err, os.ErrNotExist)
}

// CopyClient makes an independent copy of a Production client for PTR.
// The destination must not exist. The copy needs its first PTR update before Play.
func CopyClient(ctx context.Context, source, destination string) error {
	src, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	dst, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	src, dst = filepath.Clean(src), filepath.Clean(dst)
	if src == dst || inside(src, dst) || inside(dst, src) {
		return errors.New("source and destination must be separate folders")
	}
	if err := checkClientRoot(src); err != nil {
		return fmt.Errorf("invalid source client: %w", err)
	}
	if err := checkDestinationPath(dst); err != nil {
		return err
	}
	if _, err := os.Lstat(dst); err == nil {
		return errors.New("destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	hasData, hasWow := false, false
	for _, entry := range entries {
		if entry.Name() == "Data" && entry.IsDir() {
			hasData = true
		}
		if entry.Name() == "Wow.exe" && entry.Type()&os.ModeSymlink == 0 && !entry.IsDir() {
			hasWow = true
		}
	}
	if !hasData || !hasWow {
		return errors.New("source must contain Data and Wow.exe")
	}

	parent := filepath.Dir(dst)
	stage, err := os.MkdirTemp(parent, ".ebonhold-client-copy-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		if name == "Data" && entry.IsDir() {
			if err := copyTree(ctx, filepath.Join(src, name), filepath.Join(stage, name)); err != nil {
				return err
			}
		} else if entry.Type()&os.ModeSymlink == 0 && !entry.IsDir() && isGameBinary(name) {
			if err := copyFile(ctx, filepath.Join(src, name), filepath.Join(stage, name)); err != nil {
				return err
			}
		}
	}
	if err := os.WriteFile(filepath.Join(stage, ptrInstallMarker), nil, 0600); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := os.Lstat(dst); err == nil {
		return errors.New("destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := unix.Renameat2(unix.AT_FDCWD, stage, unix.AT_FDCWD, dst, unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	return nil
}

func inside(parent, path string) bool {
	rel, err := filepath.Rel(parent, path)
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
}

func checkClientRoot(root string) error {
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(root), current), current) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in source path: %s", current)
		}
		if current != filepath.Clean(root) && !info.IsDir() {
			return fmt.Errorf("non-directory in source path: %s", current)
		}
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("must be a directory")
	}
	return nil
}

func checkDestinationPath(path string) error {
	current := filepath.Clean(path)
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("destination path has an unsafe component: %s", current)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

func isGameBinary(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".exe" || ext == ".dll"
}

func copyTree(ctx context.Context, source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe game data directory: %s", source)
	}
	if err := os.Mkdir(destination, info.Mode().Perm()|0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		if excludedClientData(name) {
			continue
		}
		from, to := filepath.Join(source, name), filepath.Join(destination, name)
		child, err := os.Lstat(from)
		if err != nil {
			return err
		}
		if child.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in game data: %s", from)
		}
		if child.IsDir() {
			if err := copyTree(ctx, from, to); err != nil {
				return err
			}
		} else if child.Mode().IsRegular() {
			if err := copyFile(ctx, from, to); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("unsupported game data file: %s", from)
		}
	}
	return nil
}

func excludedClientData(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasPrefix(name, ".") || lower == "cache" || lower == "screenshots" || lower == "interface" || lower == "wtf" || lower == "logs" || lower == "backup" || lower == "backups"
}

func copyFile(ctx context.Context, source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsupported client file: %s", source)
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()|0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, contextReader{ctx: ctx, reader: in})
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
