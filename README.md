# Ebonhold Linux Launcher

Update and play Project Ebonhold on Linux. You need an existing copy of the game. If you don't have one yet, start with the [Ebonhold Installation Guide](https://project-ebonhold.com/support/installation-guide).

## Download

Get a [v2 release](https://github.com/synthtrash/ebonhold-linux-patcher/releases) for a 64-bit Intel or AMD PC:

| If you use | Download |
| --- | --- |
| X11, or Wayland with XWayland | `ebonhold-launcher-linux-amd64.tar.gz` |
| Wayland without XWayland | `ebonhold-launcher-linux-amd64-wayland.tar.gz` |

Both downloads are built on Ubuntu 22.04. They need glibc, OpenGL, and the libraries for your display system.

Extract the archive somewhere you plan to keep it. Open the launcher inside, or run it from a terminal. If the first download won't open on Wayland, try the `-wayland` version.

Optional: check the archive with `sha256sum -c` followed by the name of its matching `.sha256` file. Keep both files in the same folder.

## Upgrade from 1.x

[Download and extract the new launcher](#download). Keep your game folder, bottle, and shortcuts. You don't need to reinstall the game.

- **Play from the new launcher.** If your existing Steam or Lutris shortcut uses `launcher.sh`, remove the old updater command from the shortcut. Keep it pointing at `Wow.exe`, then follow [first setup](#first-setup).
- **Keep starting from Steam, Lutris, or Bottles.** If your shortcut runs `launcher.sh`, [replace the old launch command](#manager-presets). You can skip desktop setup.
- **Update a script.** Download and extract a release archive instead of downloading `launcher.sh`, then use the [update-only command](#command-line).

## First setup

1. Open **Setup** and choose the game profile that matches your existing files. Use **Search common locations**, or choose the folder containing `Wow.exe` and `Data/`. Use a separate folder for each profile.
2. Under **How to launch**, pick the app you already use for Ebonhold. Choose your existing bottle in Bottles, or your game shortcut in Steam or Lutris. If you only want the updater, choose **Start the game myself**.
3. On **Home**, use the install button first if you created a separate copy in Setup. Choose **Play** if you set up a launch app. If you chose **Start the game myself**, use **Update files** when a patch is out, then start the game from your usual app.

Steam needs a non-Steam shortcut pointing to that folder's `Wow.exe`. The launcher won't edit your Steam, Lutris, or Bottles settings. If you move the game, choose its launch target again in **Setup**.

## Using the launcher

- **Play** skips the desktop launcher's update check by default. Use a Steam or Lutris shortcut without an updater command if you want Play to start the game without checking. **Update files** downloads changes. **Verify files** checks the game files without changing them. Both buttons need your Ebonhold sign-in.
- If an update fails, select a file in **Update** to see why. **Technical details** lists all failures in one place. Use **Copy details** if you need to share them.
- Run `--version` to identify the build when reporting a problem. **Copy details** includes the same version.
- **Add-ons** shows what you can install or update. Removal only affects add-ons installed by this launcher and moves them to a backup.
- **News** shows the latest posts from Project Ebonhold. No sign-in needed.
- In **Settings**, you can make **Play** check for updates first, choose the launcher language, or pick game language and HD patches for the next update. Enter Bottles launch arguments one per line, without quotes. Set Steam or Lutris launch arguments in those apps.

## When the game won't open

If you see **Could not launch game**, use **Copy details** for the launch error. If Wine says it can't open the X11 display, save your work and try signing out of your Linux desktop and back in before retrying. The game may need XWayland even when the launcher itself opens on Wayland.

If the game opens but has black bars or clicks land in the wrong place, close WoW. Open **Settings → Launch → Game CVars**, select **Load windowed preset**, and choose **Apply**. This changes the game's display settings, not how Wine connects to your desktop. To undo it, close WoW and restore `WTF/Config.wtf.before-launcher-cvars` as `WTF/Config.wtf`.

To use your own wallpaper, put `background.jpg` in `~/.config/ebonhold-launcher/` next to `config.json` and restart. If you set `XDG_CONFIG_HOME`, use the `ebonhold-launcher` folder there instead. Move the JPEG aside to restore the built-in wallpaper.

## Command line

To update without opening the launcher window, pass `--client-dir`, even if the executable is already in your game folder. Use the `-wayland` executable name if that's the version you downloaded.

```sh
./ebonhold-launcher-linux-amd64 --client-dir="$HOME/Games/Ebonhold"
./ebonhold-launcher-linux-amd64 --client-dir="$HOME/Games/Ebonhold" --dry-run
./ebonhold-launcher-linux-amd64 --client-dir="$HOME/Games/Ebonhold" --verify
./ebonhold-launcher-linux-amd64 --client-dir="$HOME/Games/Ebonhold" --status
```

Run `--help` to see all options. `--dry-run` lists what an update would download. `--verify` checks the files you already have. Neither changes your game files or saves a login.

Without `--game`, the updater uses the default profile, regardless of the game folder. Use `--game=roguelike` for a folder set up under the other profile. If your old command used `--game=NAME` or your old script set `game=NAME`, pass the same `--game=NAME` to the new launcher.

## Manager presets

Use these commands to update automatically when starting the game from your usual app. If you selected a launch target in Setup, copy your command from **Setup → Next steps → Advanced**. Otherwise, use the examples below. For a shortcut that uses a different game profile, add [`--game=NAME`](#command-line) after `--client-dir=…` and before the first `--`.

If a shortcut needs you to sign in again, the updater uses Zenity to show the sign-in prompt. Without Zenity, run an update in a terminal first with the same `--client-dir` and `--game` settings, then try the shortcut again.

**Steam:** Replace the old updater command in **Launch Options**. Keep your game arguments after `%command%`.

```sh
'/path/to/ebonhold-launcher' --client-dir='/path/to/Ebonhold' --quiet -- %command%
```

With Flatpak Steam, put the launcher somewhere Steam can read. Steam also needs read and write access to the game folder to run the updater, even when no update is available. If Steam has read-only access, keep the original shortcut without the updater command and run updates outside Steam.

**Lutris:** Paste into **System options → Command prefix**.

```sh
'/path/to/ebonhold-launcher' --client-dir='/path/to/Ebonhold' --quiet --
```

Leave the executable, working directory, and game arguments in the game's settings.

**Bottles:** Save this as `launch-ebonhold.sh`. Change `Ebonhold` after `--bottle` to your bottle's name.

```sh
#!/bin/sh
exec '/path/to/ebonhold-launcher' --client-dir='/path/to/Ebonhold' --quiet -- \
  flatpak run --command=bottles-cli com.usebottles.bottles run \
  --bottle='Ebonhold' -e '/path/to/Ebonhold/Wow.exe'
```

Keep any game arguments after a second `--`, following the `Wow.exe` path. If you installed Bottles outside Flatpak, use `bottles-cli` instead of `flatpak run --command=bottles-cli com.usebottles.bottles`. Run `chmod +x launch-ebonhold.sh` before using the script.

## Sign-in and local files

The launcher saves a session token in `.updaterToken`, not your password. If the token expires, sign in again.

The desktop launcher removes old `.ebonhold-login/credentials.json` files from valid saved game folders and when you confirm a folder in Setup. It warns if it can't check a saved folder or safely remove an old password. The command line checks for the file before reusing your token, including when reporting status or add-ons. Verify and dry-run leave it alone.

To sign out locally, run the launcher with `--client-dir` and `--forget-login`. This removes the cached session and any old saved password file without contacting the server. The launcher does not read or remove entries in your desktop password wallet.

If cleanup fails, check that `.ebonhold-login` and `credentials.json` are inside the intended game folder and neither is a symlink before removing the old file manually. `--forget-login` reports paths it can't safely clean up.

## Build from source

Builds need Go 1.26.11 or newer, CGO, and OpenGL development libraries. On Debian or Ubuntu, install `libgl1-mesa-dev`, `xorg-dev`, `pkg-config`, `libwayland-dev`, and `libxkbcommon-dev` to build both versions.

```sh
go test -tags no_emoji ./...
go test -tags no_emoji,wayland ./...
./scripts/package-linux.sh dist
```

Pass a version as the second argument to label both archives, for example `./scripts/package-linux.sh dist v2.0.0-rc.1`.

The release workflow packages both versions on Ubuntu 22.04. Building on a newer distro can produce executables that won't run on older systems.

The launcher is [MIT licensed](LICENSE). The release archives include third-party notices.
