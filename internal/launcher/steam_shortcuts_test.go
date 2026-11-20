package launcher

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSteamShortcutsMatchSelectedExecutable(t *testing.T) {
	home, dataHome := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", dataHome)
	client, other := filepath.Join(home, "Production client"), filepath.Join(home, "PTR client")
	for _, folder := range []string{client, other} {
		if err := os.Mkdir(folder, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, "Wow.exe"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	data := []byte("\x00shortcuts\x00")
	for i, folder := range []string{client, other} {
		data = fmt.Appendf(data, "\x00%d\x00\x01AppName\x00Ebonhold\x00\x01Exe\x00\"%s\"\x00\x02appid\x00", i, filepath.Join(folder, "Wow.exe"))
		data = binary.LittleEndian.AppendUint32(data, 0x80001234+uint32(i))
		data = append(data, "\x00tags\x00\x010\x00RPG\x00\x08\x08"...)
	}
	data = append(data, 8, 8)
	config := filepath.Join(dataHome, "Steam", "userdata", "1", "config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config, "shortcuts.vdf")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	want := []GameShortcut{{Name: "Ebonhold", ID: "9223392051435929600"}}
	if got := ListGameShortcuts(context.Background(), "steam", client); !slices.Equal(got, want) {
		t.Fatalf("Steam choices = %#v, want %#v", got, want)
	}
	if err := os.WriteFile(path, data[:len(data)-1], 0600); err != nil {
		t.Fatal(err)
	}
	if got := ListGameShortcuts(context.Background(), "steam", client); len(got) != 0 {
		t.Fatalf("a truncated catalog gave choices: %#v", got)
	}
}
