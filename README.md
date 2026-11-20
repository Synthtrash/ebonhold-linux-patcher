# Ebonhold Linux Launcher

A Linux launcher and updater for the Project Ebonhold game client. The desktop app helps with first setup; the CLI supports Steam, Lutris, Bottles, and shell-based launches.

## Download

Choose an x86_64 archive for your display session. The release builds target Ubuntu 22.04 and need glibc, OpenGL, and the libraries for your display session. A build you compile yourself uses your distro's libraries and may not run on older systems.

| Session                                           | Archive                                        |
| ------------------------------------------------- | ---------------------------------------------- |
| X11 or Wayland with working XWayland              | `ebonhold-launcher-linux-amd64.tar.gz`         |
| Native Wayland, including setups without XWayland | `ebonhold-launcher-linux-amd64-wayland.tar.gz` |

Download the archive and its matching `.sha256` file. For the X11 build:

```sh
sha256sum -c ebonhold-launcher-linux-amd64.tar.gz.sha256
tar -xzf ebonhold-launcher-linux-amd64.tar.gz
./ebonhold-launcher-linux-amd64
```

For native Wayland, add `-wayland` to the binary and archive names in those commands. Run without arguments in a graphical session or use `--gui`. Both binaries support the same CLI commands.

## Upgrade from 1.x

Version 2 replaces `launcher.sh` with the Go launcher. Install the binary for your display session and select your existing game folder in **Setup**. You don't need to reinstall Production.

If Steam, Lutris, or Bottles calls `launcher.sh`, replace that command with a new [manager preset](#manager-presets). Scripts that fetched `launcher.sh` from the latest GitHub release must download and extract an archive instead. If the binary lives outside your game folder, pass `--client-dir=/path/to/Ebonhold` in CLI commands.

## First setup

1. On first launch or from **Setup**, search common `~/Games`, Steam, and Bottles locations for likely client folders and confirm a match. If none is found, choose the folder containing `Data/` and `Wow.exe`. The search does not scan the whole disk.
2. Select Production or PTR. Choose a separate existing PTR folder, or switch to PTR and use **Setup → Create PTR copy**. The copy needs room for `Data` and the game binaries; it leaves sign-in, add-ons, cache, and `WTF` settings behind. Run **Install PTR** once before playing a new copy. After that, both profiles use **Update files**. Switching profiles never swaps files.
3. In **Setup → How to launch**, select the app you already use to start Ebonhold. In Bottles, use **Choose a bottle…** to pick your existing Windows setup. In Steam or Lutris, use **Choose a game shortcut…** to pick an entry that points to this folder's `Wow.exe`. If the entry isn't listed, you can enter its shortcut ID manually. Or choose **Start the game myself** to use the launcher only for updates.
4. Choose **Play** on Home to start the game without checking for updates. Choose **Update files** separately when a patch is available. Updates and verification require a launcher sign-in; plain Play does not.

Discovery reads local manager data and does not change manager settings or grant Flatpak filesystem access. Steam needs a non-Steam shortcut. Lutris discovery reads the Wine entry's executable from its game configuration and does not suggest IDs if native and Flatpak databases could conflict. Reconfirm the launch choice after changing a profile's game folder.

If Play opens **Could not launch game**, use **Copy details** to get the manager's output. An X11 display error from Wine can occur even when the launcher itself opens on Wayland. Save your work, sign out of the desktop and back in to restart XWayland, then try Play again.

## Desktop features

- **Home:** Play starts the configured game. **Update files** and **Verify files** don't launch it.
- **Profiles:** Production and PTR store separate folders and launch targets. Updates and add-ons use the selected folder only.
- **Header:** the shared server light checks on startup and every 30 seconds. Green means online, red means the server reports offline, and amber covers checks, missing sign-in, and unavailable status.
- **Update:** run updates or read-only verification. The scrollable file pane uses green for files written and red for removals. Select a row for its full path, failure reason, and next step. **Technical details** lists all failed files and their causes, plus the number of downloads that succeeded. Use **Copy details** to share the report.
- **Setup:** **Next steps** tells you what's missing or how to play. The optional launch command is under **Advanced** once setup is ready. Use a permanent launcher path before copying that command.
- **Add-ons:** the tab checks the official catalog and shows available updates for the selected client. Use **Refresh catalog** when signed in, or **Sign in** when needed. Removal moves only launcher-managed add-ons to a reversible backup. The client root, `Interface`, and `AddOns` directories must not be group/other writable.
- **News:** the bottom-left card on Home previews the two latest posts from the [official public news feed](https://api.project-ebonhold.com/api/news). **All news** opens the scrollable News tab. Select a headline to open the full article. Titles and descriptions appear as published. No sign-in required.
- **Settings:** **Launch** holds launch options. **Check for updates before Play** is off by default for each profile. With it on, a failed update stops the launch. Enter Bottles arguments one per line, without quotes; set Steam or Lutris arguments in those apps. **Game** holds language and HD settings, which apply on the next update. Use the [Ebonhold Installation Guide](https://project-ebonhold.com/support/installation-guide) for missing language packs. Updates preserve video and audio settings in `WTF/Config.wtf`. HD management can install available patches or move disabled ones to `.ebonhold-disabled-hd-*`. The **Launcher language** picker sits next to the tabs; **Save** changes the interface immediately for both profiles.

If WoW opens at the wrong size, has black bars, or clicks land in the wrong place, close it and open **Settings → Launch → Game CVars**. **CVar guide…** explains the recommended settings without changing anything. **Load windowed preset** replaces your draft with `gxWindow=1` and `gxMaximize=1`; it does not write to the game until you select **Apply**. Add a line like `gxResolution=1920x1080` after loading the preset if you need a different resolution. You can enter other CVars as `name=value`; realm, language, and sign-in values are managed separately. This works without launcher sign-in. An existing config is backed up to `WTF/Config.wtf.before-launcher-cvars`; to undo the last edit, close WoW and restore that file as `WTF/Config.wtf`. This changes how WoW draws its window, not how Wine connects to X11.

Preferences are stored in `$XDG_CONFIG_HOME/ebonhold-launcher/config.json` (normally `~/.config/ebonhold-launcher/config.json`). They don't contain credentials. Legacy shared folders and manager choices migrate to Production only; confirm a target before using Play.

The launcher ships with the ELL wallpaper. To use your own, save a JPEG named `background.jpg` next to `config.json` and restart the launcher. Move that file aside to restore the built-in wallpaper.

## Command line

```sh
./ebonhold-launcher-linux-amd64 --help
./ebonhold-launcher-linux-amd64 --client-dir="$HOME/Games/Ebonhold" --dry-run
./ebonhold-launcher-linux-amd64 --client-dir="$HOME/Games/Ebonhold" --verify
./ebonhold-launcher-linux-amd64 --client-dir="$HOME/Games/Ebonhold" --status
```

| Option                               | Behavior                                                                                                                                             |
| ------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--gui`                              | Open the desktop app. Use by itself.                                                                                                                 |
| `--client-dir=PATH`                  | Use a game client folder different from the launcher binary's folder. The directory must already exist.                                              |
| `--verify`                           | Compare selected files with the manifest; do not change game files, cache, realmlist, token, or saved login.                                         |
| `--dry-run`                          | Print paths that need downloads without changing game or login state.                                                                                |
| `--quick`                            | Check required game patch files only when no game command is supplied.                                                                               |
| `--full`                             | Include optional common and game files.                                                                                                              |
| `--status`                           | Print realm status and exit.                                                                                                                         |
| `--game=SLUG`                        | Select a manifest game; defaults to `roguelike-prod`. Use `roguelike` for PTR.                                                                       |
| `--language=LOCALE`                  | Select `enUS`, `frFR`, `deDE`, or `esES`.                                                                                                            |
| `--hd-characters` / `--hd-creatures` | Include the corresponding HD option if available in the selected game manifest.                                                                      |
| `--option=SLUG`                      | Include an exact optional group from the manifest. Repeat to combine options.                                                                        |
| `--list-addons`                      | List the official add-on catalog.                                                                                                                    |
| `--check-addons`                     | Compare installed add-ons with the current catalog.                                                                                                  |
| `--select-addons`                    | Interactively choose add-ons to install.                                                                                                             |
| `--addons=LIST`                      | Install comma-separated add-on names or IDs.                                                                                                         |
| `--remove-addons`                    | Interactively select launcher-managed add-ons to remove.                                                                                             |
| `--remove-addons=LIST`               | Remove comma-separated launcher-managed add-on names or IDs. Removal moves folders to `.ebonhold-removed-addons/`; restore them manually from there. |
| `--quiet`                            | Suppress routine output; warnings and errors remain.                                                                                                 |
| `--debug`                            | Print non-secret launcher and game diagnostics.                                                                                                      |
| `--relogin`                          | Skip cached and saved sign-in and request fresh credentials.                                                                                         |
| `--forget-login`                     | Remove this installation's cached token and saved local login without network access.                                                                |
| `--help`                             | Print command help.                                                                                                                                  |

Default mode updates required common and game files. On a configured profile's exact client folder, it also uses that profile's settings unless you supply `--full` or explicit `--option` values. `--quick` narrows standalone checks only. When a game command is supplied, even old `--quick` wrappers check every required file before starting it. `--full` includes all optional manifest files and cannot be combined with `--quick`.

Production and PTR need separate client folders. CLI updates and verification refuse a folder that overlaps the other profile's saved folder. Use `--game=roguelike --client-dir=/path/to/Ebonhold-PTR` for the PTR copy; omitting `--game` selects Production.

```sh
./ebonhold-launcher-linux-amd64 --client-dir="$HOME/Games/Ebonhold" --quiet -- %command%
```

The launcher refreshes an expired session at most once during a download run. Downloads run four at a time, retry failures, validate manifest MD5 checksums, and replace files atomically. After changed game files, it removes cached `.wdb` files and creates `Cache/invalid`. It also removes stale file names that differ from a manifest path only by case.

## Manager presets

These optional commands update before launching, regardless of the desktop Play setting. Presets use the selected client folder and game profile. The manual examples use Production; replace `--game='roguelike-prod'` with `--game='roguelike'` for PTR.

**Steam — non-Steam game Launch Options**

```sh
'/path/to/ebonhold-launcher' --client-dir='/path/to/Ebonhold' --game='roguelike-prod' --quiet -- %command%
```

**Lutris — System options → Command prefix**

```sh
'/path/to/ebonhold-launcher' --client-dir='/path/to/Ebonhold' --game='roguelike-prod' --quiet --
```

Keep Lutris's normal executable and working directory pointed at the game. The launcher passes Lutris's command through after updating.

**Bottles — wrapper script**

```sh
#!/bin/sh
exec '/path/to/ebonhold-launcher' --client-dir='/path/to/Ebonhold' --game='roguelike-prod' --quiet -- \
  flatpak run --command=bottles-cli com.usebottles.bottles run \
  --bottle='Ebonhold' -e '/path/to/Ebonhold/Wow.exe'
```

For native Bottles, use `bottles-cli` in place of `flatpak run --command=bottles-cli com.usebottles.bottles`. The generated preset includes your bottle, the profile's `Wow.exe`, and any Bottles game arguments you set. If using a wrapper instead of the GUI, save the preset and run `chmod +x launch-ebonhold.sh`.

## Sign-in and local files

The launcher reuses a valid `.updaterToken` in the client folder. If it expires, an explicitly saved login may be used; otherwise the launcher asks for a username and password. Remember-me is opt-in and unchecked by default. `--verify` and `--dry-run` may use saved credentials but do not persist tokens or credentials.

Remember-me stores unencrypted username/password JSON at `.ebonhold-login/credentials.json` inside the client folder. Its directory is created with mode `0700` and the file with mode `0600`. The token file is also private (`0600`). The launcher rejects unsafe ownership, permissions, symlinks, and hard-linked credential files. **Programs running as your user, root, and backups can still read a remembered password.** Use it only on a trusted computer. No desktop wallet is used or modified.

If upgrading from a version that stored credentials in a desktop wallet, the Go launcher does not import or access those entries. Remove any old Ebonhold updater entry yourself if desired.

## Build from source

Linux builds require Go 1.24 or newer, CGO, and OpenGL development libraries. On Debian/Ubuntu, install `libgl1-mesa-dev`, `xorg-dev`, `pkg-config`, `libwayland-dev`, and `libxkbcommon-dev` to build both variants.

```sh
go test ./...
go test -tags wayland ./...
./scripts/package-linux.sh dist
```

The tests cover direct Play without a manifest request, opt-in updates before launch, shortcut matching by profile, first-time PTR installation, server status refresh, and scrolling long verification failures. They use temporary client folders and don't sign in to Ebonhold.

The release workflow builds both archives and checksums on Ubuntu 22.04. Run a packaged binary under its matching display server, and verify Production and PTR with a current sign-in and separate game folders before publishing.
