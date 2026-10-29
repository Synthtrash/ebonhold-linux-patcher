# Ebonhold Linux Launcher

A Linux launcher and updater for the Project Ebonhold game client. The desktop app helps with first setup; the CLI supports Steam, Lutris, Bottles, and shell-based launches.

## Download

Releases provide a Linux x86_64 archive and SHA-256 checksum:

```sh
sha256sum -c ebonhold-launcher-linux-amd64.tar.gz.sha256
tar -xzf ebonhold-launcher-linux-amd64.tar.gz
./ebonhold-launcher-linux-amd64
```

Run without arguments in a graphical session to open the desktop app. Use `--gui` to open it explicitly. On an X11 or XWayland desktop, the app opens as a compact, dark Catppuccin-themed window with Home, Update, and Setup views. There is no phone layout.

## First setup

1. Open the launcher and choose the folder containing the game client files, including `Data/`.
2. Select Production or PTR.
3. Choose **Update**. The default updates required files; it does not include optional manifest entries.
4. Open **Setup**, select the manager you use, then copy its preset. The launcher only detects whether Steam, Lutris, or Bottles is installed; it cannot infer which one owns your game, bottle, or Wine prefix.
5. Apply the preset in that manager. For Bottles, save the copied text as a shell script, replace the bottle name and `Wow.exe` path, then make it executable. Launch Ebonhold through the manager after setup.

The launcher does not edit manager settings, install packages, change sandbox permissions, choose an app ID/bottle/prefix, or launch the GUI-selected game by guessing. Manager setup is explicit and reversible.

## Desktop features

- **Home:** choose a client folder and game profile; update, verify, or open setup.
- **Update:** run updates or read-only verification. **View files** opens the affected file list. Technical failure details appear in a copyable dialog.
- **Setup:** detect installed managers and show/copy a Steam, Lutris, Bottles-native, or Bottles-Flatpak preset. Presets include the selected client folder and game profile, so the launcher binary can live elsewhere.
- **Add-ons:** load the official catalog, check installed folders, install selected add-ons, and move launcher-managed add-ons to a reversible backup when removing them.

The UI writes preferences to `$XDG_CONFIG_HOME/ebonhold-launcher/config.json` (normally `~/.config/ebonhold-launcher/config.json`). It stores the selected client folder, game profile, and manager choice, not passwords.

## Command line

```sh
./ebonhold-launcher-linux-amd64 --help
./ebonhold-launcher-linux-amd64 --client-dir="$HOME/Games/Ebonhold" --dry-run
./ebonhold-launcher-linux-amd64 --client-dir="$HOME/Games/Ebonhold" --verify
./ebonhold-launcher-linux-amd64 --client-dir="$HOME/Games/Ebonhold" --status
```

| Option | Behavior |
| --- | --- |
| `--gui` | Open the desktop app. Use by itself. |
| `--client-dir=PATH` | Use a game client folder different from the launcher binary's folder. The directory must already exist. |
| `--verify` | Compare selected files with the manifest; do not change game files, cache, realmlist, token, or saved login. |
| `--dry-run` | Print paths that need downloads without changing game or login state. |
| `--quick` | Check required game patch files only. |
| `--full` | Include optional common and game files. |
| `--status` | Print realm status and exit. |
| `--game=SLUG` | Select a manifest game; defaults to `roguelike-prod`. Use `roguelike` for PTR. |
| `--list-addons` | List the official add-on catalog. |
| `--check-addons` | Compare installed add-ons with the current catalog. |
| `--select-addons` | Interactively choose add-ons to install. |
| `--addons=LIST` | Install comma-separated add-on names or IDs. |
| `--remove-addons` | Interactively select launcher-managed add-ons to remove. |
| `--remove-addons=LIST` | Remove comma-separated launcher-managed add-on names or IDs. Removal moves folders to `.ebonhold-removed-addons/`; restore them manually from there. |
| `--quiet` | Suppress routine output; warnings and errors remain. |
| `--debug` | Print non-secret launcher and game diagnostics. |
| `--relogin` | Skip cached and saved sign-in and request fresh credentials. |
| `--forget-login` | Remove this installation's cached token and saved local login without network access. |
| `--help` | Print command help. |

Default mode updates required common and game files. `--quick` narrows that to required game patch files; `--full` includes optional manifest files. `--quick` and `--full` cannot be combined. Arguments after `--` are passed to the manager/game command after updating:

```sh
./ebonhold-launcher-linux-amd64 --client-dir="$HOME/Games/Ebonhold" --quick --quiet -- %command%
```

The launcher refreshes an expired session at most once during a download run. Downloads run four at a time, retry failures, validate manifest MD5 checksums, and replace files atomically. After changed game files, it removes cached `.wdb` files and creates `Cache/invalid`. It also removes stale file names that differ from a manifest path only by case.

## Manager presets

Presets are generated for the selected client folder and game profile. The manual examples below use Production; replace `--game='roguelike-prod'` with `--game='roguelike'` for PTR.

**Steam — non-Steam game Launch Options**

```sh
'/path/to/ebonhold-launcher' --client-dir='/path/to/Ebonhold' --game='roguelike-prod' --quick --quiet -- %command%
```

**Lutris — System options → Command prefix**

```sh
'/path/to/ebonhold-launcher' --client-dir='/path/to/Ebonhold' --game='roguelike-prod' --quick --quiet --
```

Keep Lutris's normal executable and working directory pointed at the game. The launcher passes Lutris's command through after updating.

**Bottles — wrapper script**

```sh
#!/bin/sh
exec '/path/to/ebonhold-launcher' --client-dir='/path/to/Ebonhold' --game='roguelike-prod' --quick --quiet -- \
  flatpak run --command=bottles-cli com.usebottles.bottles run \
  -b 'REPLACE_WITH_BOTTLE_NAME' -e '/path/to/Wow.exe'
```

For native Bottles, replace the `flatpak run --command=bottles-cli com.usebottles.bottles` prefix with `bottles-cli`. Save the wrapper, fill in the bottle and executable values, then run `chmod +x launch-ebonhold.sh`. Bottles is not configured automatically.

## Sign-in and local files

The launcher reuses a valid `.updaterToken` in the client folder. If it expires, an explicitly saved login may be used; otherwise the launcher asks for a username and password. Remember-me is opt-in and unchecked by default. `--verify` and `--dry-run` may use saved credentials but do not persist tokens or credentials.

Remember-me stores unencrypted username/password JSON at `.ebonhold-login/credentials.json` inside the client folder. Its directory is created with mode `0700` and the file with mode `0600`. The token file is also private (`0600`). The launcher rejects unsafe ownership, permissions, symlinks, and hard-linked credential files. **Programs running as your user, root, and backups can still read a remembered password.** Use it only on a trusted computer. No desktop wallet is used or modified.

If upgrading from a version that stored credentials in a desktop wallet, the Go launcher does not import or access those entries. Remove any old Ebonhold updater entry yourself if desired.

## Build from source

Linux builds require Go 1.24 or newer, CGO, `pkg-config`, and the platform's OpenGL and X11 development libraries (Fyne desktop UI). For example, on Debian/Ubuntu install `libgl1-mesa-dev`, `xorg-dev`, and `pkg-config` before building:

```sh
mkdir -p dist
go build -o dist/ebonhold-launcher ./cmd/ebonhold-launcher
go test ./...
```

The release workflow builds a Linux x86_64 desktop binary, runs Go tests and vet, and attaches a SHA-256 checksum.

## Credits

Fork of [sigboe/ebonhold-updater](https://github.com/sigboe/ebonhold-updater) by **Sigboe**.
