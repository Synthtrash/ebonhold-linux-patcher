# ebonhold-linux-launcher

Updates and launches the Project Ebonhold game client on Linux.

---

## What it does

- Logs in via the Ebonhold API and caches your session token.
- Checks only game patch files (paths beginning with `patch`) against the manifest using MD5 hashes by default.
- Downloads missing or mismatched files (4 concurrent downloads), verifies them, then replaces files atomically. Use `--full` to include common and optional files.
- Checks server status and updates `Data/enUS/realmlist.wtf`.
- After a successful file update, clears cached `.wdb` files and creates `Cache/invalid` so the game rebuilds its cache.

---

## Prerequisites

- **curl**, **jq**, **md5sum**, **bc**, **unzip** (including `zipinfo`), **flock** (from `util-linux`) – usually pre-installed on most Linux.
- **zenity** (optional) – for GUI prompts; falls back to terminal prompts if missing.

---

## Usage

Copy the launcher to your Ebonhold client directory and make it executable. It always updates the directory containing the invoked script or symlink, regardless of the current working directory:

```bash
chmod +x launcher.sh
./launcher.sh
./launcher.sh --debug
```

### Flags

| Flag | Description |
|------|-------------|
| `--debug` | Enable verbose output |
| `--verify` | Check files against manifest without changing client files, cache, realmlist, or token |
| `--dry-run` | Show downloads that would be needed without changing client files, cache, realmlist, or token |
| `--full` | Check and update all common and game files, including optional files |
| `--status` | Show the authenticated realm status and exit |
| `--list-addons` | List addons available from the official launcher catalog |
| `--check-addons` | Check installed launcher addons and suggest available updates |
| `--select-addons` | Interactively select addons to download |
| `--addons=LIST` | Download comma-separated addon names or IDs, such as `--addons=Elvui,Details` |
| `--quiet` | Suppress non-error output |
| `--help` | Show this message |

When run interactively without game arguments, the launcher checks installed addons after updating and offers to install available updates. Steam and other passthrough launches never prompt.

### Steam (non-Steam game)

1. Add `Wow.exe` as a non-Steam game in Steam.
2. Right-click the game → Properties → Launch Options:

```
/path/to/launcher.sh --quiet %command%
```

The launcher updates the client silently, then launches the game. If files changed, it clears `.wdb` cache files and creates `Cache/invalid` before launch. If your cached token expires, a zenity login prompt will pop up.

---

## Credits

Fork of [sigboe/ebonhold-updater](https://github.com/sigboe/ebonhold-updater) by **Sigboe**.
