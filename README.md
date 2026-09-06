# ebonhold-linux-launcher

Updates and launches the Project Ebonhold game client on Linux.

---

## What it does

- Logs in via the Ebonhold API and caches your session token. Login remains interactive by default; an optional, explicit **Remember me on this computer** choice stores the updater password in the Linux desktop keyring through `secret-tool` (never in a plaintext fallback).
- Checks all required common and game files against the manifest using MD5 hashes by default.
- Downloads missing or mismatched files (4 concurrent downloads), verifies them, then replaces files atomically. Use `--full` to include optional files.
- Removes stale files that differ only by case from the manifest path (e.g. a leftover `patch-4.mpq` next to `patch-4.MPQ`), which would otherwise shadow the updated file on case-sensitive filesystems.
- Checks server status and updates `Data/enUS/realmlist.wtf`.
- After a successful file update, clears cached `.wdb` files and creates `Cache/invalid` so the game rebuilds its cache.
- Removing addons (`--remove-addons`) moves their folders to `.ebonhold-removed-addons/` instead of deleting them, so a removal can be undone by moving the folders back.

---

## Prerequisites

- **curl**, **jq**, **md5sum**, **sha256sum**, **bc**, **unzip** (including `zipinfo`), **flock** (from `util-linux`) – usually pre-installed on most Linux systems.
- Standard GNU command-line tools such as `realpath`, `find`, `sed`, `grep`, `sort`, `date`, `stat`, and `mktemp`.
- **zenity** (optional) – for GUI prompts; falls back to terminal prompts if missing. Once a token is cached, non-interactive launches can run without either one; the first login still needs a terminal or display.
- **secret-tool** (optional, from libsecret) – enables the opt-in desktop-keyring login. If it is missing, locked, or unavailable, normal manual login still works and the launcher never falls back to a plaintext password file.

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
| ------ | ------------- |
| `--debug` | Enable verbose output |
| `--verify` | Check files against the manifest without changing client files, cache, realmlist, token, or addons |
| `--dry-run` | Show downloads that would be needed without changing client files, cache, realmlist, or token |
| `--quick` | Check only required game patch files, matching the previous default behavior |
| `--full` | Check and update all common and game files, including optional files |
| `--status` | Show the authenticated realm status and exit |
| `--game=SLUG` | Select a game from the launcher manifest (default: `roguelike-prod`) |
| `--list-addons` | List addons available from the official launcher catalog |
| `--check-addons` | Check installed launcher addons and suggest available updates |
| `--select-addons` | Interactively select addons to download |
| `--remove-addons` | Interactively select installed launcher addons to remove |
| `--remove-addons=LIST` | Remove comma-separated installed addon names or IDs |
| `--addons=LIST` | Download comma-separated addon names or IDs, such as `--addons=Elvui,Details` |
| `--quiet` | Suppress routine output; warnings and errors remain |
| `--relogin` | Ignore cached/saved login and request a fresh manual login; choose Remember me again if desired |
| `--forget-login` | Remove this installation's cached token and desktop-keyring login without contacting the network |
| `--help` | Show this message |

When run interactively without game arguments, the launcher checks installed addons after updating and offers to install available updates. Steam and other passthrough launches never prompt for addons. Arguments after the launcher options are passed to the game; use `--` when you want to clearly separate them from launcher options. Use `--game=roguelike` for the PTR manifest.

### Login and keyring behavior

A valid `.updaterToken` is always reused without looking up a password. If it is missing or expired, an opted-in desktop-keyring login is tried once; rejected saved credentials are never retried in a loop. Otherwise the launcher asks for a normal username and password. In a GUI, the Remember me choice is an unchecked checklist; in a terminal the prompt defaults to **No**. The warning explains that the password is stored in the desktop keyring and should only be enabled on a trusted computer. Credentials are written only after authentication succeeds and consent is explicit.

Use `--relogin` to force a new manual login and choose the setting again. Use `--forget-login` to clear the current installation's cached token and all Ebonhold updater keyring entries for that installation; it reports a failure if the keyring cannot be cleared. `--verify` and `--dry-run` do not write tokens, keyring credentials, or client files.

The launcher never logs the password or raw login response, does not pass bearer tokens as curl command-line arguments, and does not pass updater credentials to the game process. A headless first login fails with an actionable message unless a previously saved desktop-keyring login can be used.

### Steam (non-Steam game)

1. Add `Wow.exe` as a non-Steam game in Steam.
2. Right-click the game → Properties → Launch Options:

```bash
/path/to/launcher.sh --quick --quiet -- %command%
```

The launcher quickly checks game patch files, then launches the game. Omit `--quick` to check every required file. If files changed, it clears `.wdb` cache files and creates `Cache/invalid` before launch. If your cached token expires, a zenity login prompt will pop up; after an explicit opt-in, the desktop keyring can renew the updater login without prompting.

### Lutris

Lutris can prepend a command to its normal Wine launch command:

1. Open the game's **Configure** window.
2. Open **System options**.
3. Set **Command prefix** to the launcher command, using an absolute path:

```bash
/path/to/launcher.sh --quick --quiet --
```

Leave the Lutris executable and working directory set to the game client. Lutris will run the launcher first; the launcher updates the client and then passes Lutris's normal Wine command through unchanged. Remove `--quiet` while testing if you want to see the update messages.

### Bottles

Bottles does not use Steam's `%command%` placeholder. The simplest setup is a small wrapper that runs the launcher and then Bottles' CLI:

```bash
#!/usr/bin/env bash
exec /path/to/launcher.sh --quick --quiet -- \
  flatpak run --command=bottles-cli com.usebottles.bottles run \
  -b "MyBottle" -e "/path/to/Wow.exe"
```

Save it as `launch-ebonhold.sh`, replace `MyBottle` and the executable path, then run:

```bash
chmod +x launch-ebonhold.sh
./launch-ebonhold.sh
```

For a non-Flatpak Bottles installation, replace the `flatpak run --command=bottles-cli com.usebottles.bottles` portion with `bottles-cli`. The launcher updates the client directory before invoking Bottles. Do not add the shell script as a Windows executable inside the bottle; it is a Linux launcher command.

### Independent optional password-prefill modification

The launcher does not implement game autologin, GlueXML/savepass installation, password downloading, or saved-game-password import. If you independently choose to investigate password prefilling, [wow-wotlk_savepass](https://github.com/s0h2x/wow-wotlk_savepass) is an **OPTIONAL, independent modification** and is untested on Ebonhold. It may store credentials recoverably on disk; review its code and storage behavior yourself. No compatibility claim is made here.

---

## Credits

Fork of [sigboe/ebonhold-updater](https://github.com/sigboe/ebonhold-updater) by **Sigboe**.
