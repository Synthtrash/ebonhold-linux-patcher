package launcher

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
)

func steamShortcuts(ctx context.Context, root string) []GameShortcut {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	flatpak := filepath.Join(home, ".var", "app", "com.valvesoftware.Steam")
	roots := [...]string{
		filepath.Join(userDataHome(home), "Steam"),
		filepath.Join(home, ".steam", "steam"),
		filepath.Join(home, ".steam", "root"),
		filepath.Join(flatpak, ".local", "share", "Steam"),
		filepath.Join(flatpak, "data", "Steam"),
	}
	seen := make(map[string]bool, len(roots))
	var matches []GameShortcut
	for _, steam := range roots {
		userdata, err := filepath.EvalSymlinks(filepath.Join(steam, "userdata"))
		if err != nil || seen[userdata] {
			continue
		}
		seen[userdata] = true
		dir, err := os.OpenFile(userdata, os.O_RDONLY|unix.O_DIRECTORY, 0)
		if err != nil {
			continue
		}
		users, _ := dir.ReadDir(1024)
		dir.Close()
		for _, user := range users {
			if ctx.Err() != nil {
				return matches
			}
			if !user.IsDir() {
				continue
			}
			if _, err := strconv.ParseUint(user.Name(), 10, 32); err != nil {
				continue
			}
			path := filepath.Join(userdata, user.Name(), "config", "shortcuts.vdf")
			matches = append(matches, steamFileShortcuts(root, readManagerFile(path, 8<<20))...)
		}
	}
	return matches
}

// Steam writes a binary KeyValues object containing one child object per shortcut.
// Only shortcut fields are retained; unrelated fields and nested tags are skipped.
func steamFileShortcuts(root string, data []byte) []GameShortcut {
	data, ok := bytes.CutPrefix(data, []byte("\x00shortcuts\x00"))
	if !ok {
		return nil
	}
	var matches []GameShortcut
	var name, exe, startDir []byte
	var appID uint32
	for depth := 1; len(data) > 0; {
		kind := data[0]
		data = data[1:]
		if kind == 8 {
			depth--
			if depth == 1 && appID&0x80000000 != 0 && len(name) != 0 &&
				matchesClientExecutable(root, string(exe), string(startDir)) {
				// Steam's CGameID type 2 puts the saved non-Steam AppID in bits 32–63.
				id := uint64(appID)<<32 | 2<<24
				matches = append(matches, GameShortcut{ID: strconv.FormatUint(id, 10), Name: string(name)})
			}
			if depth == 0 {
				if len(data) == 1 && data[0] == 8 {
					return matches
				}
				return nil
			}
			continue
		}
		key, rest, ok := bytes.Cut(data, []byte{0})
		if !ok {
			return nil
		}
		data = rest
		switch kind {
		case 0:
			depth++
			if depth == 2 {
				name, exe, startDir, appID = nil, nil, nil, 0
			}
		case 1:
			value, rest, ok := bytes.Cut(data, []byte{0})
			if !ok {
				return nil
			}
			data = rest
			if depth == 2 {
				switch {
				case bytes.EqualFold(key, []byte("AppName")):
					name = value
				case bytes.EqualFold(key, []byte("Exe")):
					exe = value
				case bytes.EqualFold(key, []byte("StartDir")):
					startDir = value
				}
			}
		case 2, 3, 4, 6:
			if len(data) < 4 {
				return nil
			}
			if kind == 2 && depth == 2 && bytes.EqualFold(key, []byte("appid")) {
				appID = binary.LittleEndian.Uint32(data)
			}
			data = data[4:]
		case 7:
			if len(data) < 8 {
				return nil
			}
			data = data[8:]
		default:
			return nil
		}
	}
	return nil
}
