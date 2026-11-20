package launcher

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

type lutrisGame struct {
	ID         int64
	Name       string
	Directory  string
	ConfigPath string
}

// lutrisShortcuts suggests installed Lutris entries whose configured executable is the selected client.
func lutrisShortcuts(ctx context.Context, root string) []GameShortcut {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	nativeConfig, _ := os.UserConfigDir()
	flatpak := filepath.Join(home, ".var", "app", "net.lutris.Lutris")
	installations := [...]struct{ data, config string }{
		{filepath.Join(userDataHome(home), "lutris"), filepath.Join(nativeConfig, "lutris")},
		{filepath.Join(flatpak, "data", "lutris"), filepath.Join(flatpak, "config", "lutris")},
	}
	present := func(path string) bool {
		info, err := os.Stat(path)
		return err == nil && info.Mode().IsRegular()
	}
	active := -1
	for i, install := range installations {
		if info, err := os.Stat(install.config); !filepath.IsAbs(install.config) || err != nil || !info.IsDir() {
			install.config = install.data
			installations[i] = install
		}
		if present(filepath.Join(install.data, "pga.db")) || present(filepath.Join(install.config, "lutris.conf")) {
			if active >= 0 {
				// The desktop URI cannot distinguish colliding IDs in two installations.
				return nil
			}
			active = i
		}
	}
	if active < 0 {
		return nil
	}
	install := installations[active]
	args := []string{"-I", "-c", lutrisSQLiteQuery, install.data, install.config}
	var query *exec.Cmd
	if active == 0 {
		query = exec.CommandContext(ctx, "python3", args...)
	} else {
		query = exec.CommandContext(ctx, "flatpak", append([]string{"run", "--command=python3", "net.lutris.Lutris"}, args...)...)
	}
	output, err := query.Output()
	if err != nil {
		return nil
	}
	var games []lutrisGame
	if json.Unmarshal(output, &games) != nil {
		return nil
	}
	return lutrisGameShortcuts(ctx, root, install.config, games)
}

func lutrisGameShortcuts(ctx context.Context, root, configDir string, games []lutrisGame) []GameShortcut {
	var matches []GameShortcut
	for _, game := range games {
		if ctx.Err() != nil {
			return matches
		}
		if game.ID <= 0 || game.Name == "" || game.ConfigPath == "" || filepath.Base(game.ConfigPath) != game.ConfigPath ||
			game.ConfigPath == "." || game.ConfigPath == ".." {
			continue
		}
		data := readManagerFile(filepath.Join(configDir, "games", game.ConfigPath+".yml"), 1<<20)
		if len(data) == 0 {
			continue
		}
		var config struct {
			Game struct {
				Exe string `yaml:"exe"`
			} `yaml:"game"`
		}
		if yaml.Unmarshal(data, &config) == nil && matchesClientExecutable(root, config.Game.Exe, game.Directory) {
			matches = append(matches, GameShortcut{ID: strconv.FormatInt(game.ID, 10), Name: game.Name})
		}
	}
	return matches
}

const lutrisSQLiteQuery = `import configparser, json, pathlib, sqlite3, sys
data, config = map(pathlib.Path, sys.argv[1:])
settings = configparser.ConfigParser()
settings.read(config / "lutris.conf")
db = pathlib.Path(settings.get("lutris", "pga_path", fallback="") or data / "pga.db").expanduser()
if not db.is_absolute() or not db.is_file():
    sys.exit(1)
with sqlite3.connect(db.as_uri() + "?mode=ro", uri=True, timeout=1) as conn:
    conn.row_factory = sqlite3.Row
    rows = conn.execute("SELECT id,name,directory,configpath FROM games WHERE installed=1 AND runner='wine' LIMIT 10000")
    print(json.dumps([dict(row) for row in rows]))
`
