package launcher

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const desktopEntryID = "com.project-ebonhold.launcher.desktop"

// InstallApplicationShortcut installs or replaces the current user's launcher entry.
func InstallApplicationShortcut(executable string, icon []byte) error {
	if !filepath.IsAbs(executable) || !utf8.ValidString(executable) || strings.ContainsRune(executable, '=') {
		return fmt.Errorf("invalid launcher executable path")
	}
	exec, err := desktopExecArg(executable)
	if err != nil {
		return err
	}
	// GIO rejects an escaped percent in the program token. Pass that path
	// as an argument to env instead so field expansion happens first.
	if strings.ContainsRune(executable, '%') {
		exec = "env " + exec
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dataHome := userDataHome(home)
	iconPath := filepath.Join(dataHome, "icons", "hicolor", "256x256", "apps", "com.project-ebonhold.launcher.png")
	entryPath := filepath.Join(dataHome, "applications", desktopEntryID)
	entry := "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=Ebonhold Linux Launcher\n" +
		"Exec=" + exec + " --gui\n" +
		"Icon=com.project-ebonhold.launcher\n" +
		"Terminal=false\n" +
		"Categories=Game;\n"

	if err := atomicUserFile(iconPath, icon); err != nil {
		return err
	}
	return atomicUserFile(entryPath, []byte(entry))
}

// desktopExecArg quotes one argv element, escaping both Exec syntax and the
// desktop-entry string layer. Field-code markers are doubled as required.
func desktopExecArg(arg string) (string, error) {
	var quoted strings.Builder
	quoted.Grow(len(arg) + 2)
	quoted.WriteByte('"')
	for _, r := range arg {
		switch r {
		case '\\', '"', '$', '`':
			quoted.WriteByte('\\')
			quoted.WriteRune(r)
		case '%':
			quoted.WriteString("%%")
		default:
			if r < 0x20 || r == 0x7f {
				return "", fmt.Errorf("launcher executable path contains a control character")
			}
			quoted.WriteRune(r)
		}
	}
	quoted.WriteByte('"')

	var value strings.Builder
	value.Grow(quoted.Len())
	for _, r := range quoted.String() {
		if r == '\\' {
			value.WriteString("\\\\")
		} else {
			value.WriteRune(r)
		}
	}
	return value.String(), nil
}

func atomicUserFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".shortcut-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if err := file.Chmod(0644); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
