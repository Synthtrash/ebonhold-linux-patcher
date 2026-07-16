# ebonhold-linux-patcher

Downloads and updates the Project Ebonhold game client on Linux.

---

## What it does

- Logs in via the Ebonhold API and caches your session token.
- Checks all game files against the manifest using MD5 hashes.
- Downloads missing or mismatched files (4 concurrent downloads).
- Checks server status and updates `Data/enUS/realmlist.wtf`.
- Touches `Cache/invalid` when files change (so the game rebuilds its cache).

---

## Prerequisites

- **curl**, **jq**, **md5sum**, **bc** – usually pre‑installed on most Linux.
- **zenity** (optional) – for GUI prompts; falls back to terminal prompts if missing.

---

## Usage

Copy the script to your Ebonhold client directory and make it executable:

```bash
chmod +x updater.sh
./updater.sh
./updater.sh --debug
```

### Flags

| Flag | Description |
|------|-------------|
| `--debug` | Enable verbose output |
| `--verify` | Check files against manifest (no downloads) |
| `--dry-run` | Show what would be done without downloading |
| `--quiet` | Suppress non-error output |
| `--help` | Show this message |

### Steam (non-Steam game)

1. Add `Wow.exe` as a non-Steam game in Steam.
2. Right-click the game → Properties → Launch Options:

```
/path/to/updater.sh --quiet %command%
```

The script will update the client silently, then launch the game. If your cached token expires, a zenity login prompt will pop up.

---

## Credits

Fork of [sigboe/ebonhold-updater](https://github.com/sigboe/ebonhold-updater) by **Sigboe**.
