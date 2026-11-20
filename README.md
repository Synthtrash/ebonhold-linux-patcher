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

1. Open **Setup**. If you need the game files, choose **I need to install it**. If you already have Ebonhold, let the launcher look for it, or choose `Wow.exe` or the folder containing it and `Data/`. Use a separate folder for each profile.
2. Under **How to launch**, choose the app you already use. Pick your existing Bottles bottle or a matching Steam or Lutris shortcut. If there is one matching shortcut, the launcher suggests it. Use **Advanced** only if you need to enter an ID. If you only want the updater, choose **Start the game myself**.
3. On **Next steps**, use **Test launch** to see whether the game opens without a launcher update. Then use **Play** on Home. If you chose **Start the game myself**, update here and start the game from your usual app.

Drag the title bar to move the folder or file picker inside the launcher. On small displays, the launcher uses smaller text without changing your desktop settings. If Home is narrow, scroll past the controls to see the news. Setup keeps its buttons visible while you scroll.

Steam needs a non-Steam shortcut pointing to that folder's `Wow.exe`. The launcher won't edit your Steam, Lutris, or Bottles settings. If you move the game, choose its launch target again in **Setup**.

If you create a separate copy in Setup, **Check space** shows the destination, estimated copy size, and free space before copying. An activity bar stays visible while the launcher checks space or copies files. Keep the launcher open until the copy is ready, then use the install button on Home before playing it.

If you move or delete a test copy, reopen **Setup** on that profile. **Create PTR copy** offers the previous path if it is free, or you can choose an existing folder.

## Using the launcher

- **Play** skips the desktop launcher's update check by default. Use a Steam or Lutris shortcut without an updater command if you want Play to start the game without checking. **Update files** downloads changes. **Verify files** checks the game files without changing them. Both buttons need your Ebonhold sign-in.
- If an update fails, select a file in **Update** to see why. **Technical details** lists all failures in one place. Use **Copy details** if you need to share them.
- Run `--version` to identify the build when reporting a problem. **Copy details** includes the same version.
- **Add-ons** shows what you can install or update. Removal only affects add-ons installed by this launcher and moves them to a backup.
- **News** shows the latest posts from Project Ebonhold. No sign-in needed.
- **Player Guide** under **Latest news** on Home opens the gameplay guide in your browser.
- In **Settings**, you can make **Play** check for updates first, choose the launcher language, or pick game language and HD patches for the next update. Enter Bottles launch arguments one per line, without quotes. Set Steam or Lutris launch arguments in those apps.
- **Settings → Launch → Add to Applications** puts Ebonhold Linux Launcher in your app menu without `sudo`. If you move the executable, run it from its new location and add the shortcut again.

## When the game won't open

Open **Troubleshoot** on Home or in Setup. Start by trying the same `Wow.exe` in your usual game app. The launcher can point you to Steam compatibility settings, Flatpak folder access, Microsoft Visual C++ downloads, or display fixes. It won't install a runtime or change Flatpak permissions.

If a launch fails, **Copy details** shares the launcher version, Linux distribution, display session, game app, and detected issue when available. It excludes paths, credentials, and raw launch output. Use **View local output** to inspect the full captured message and check it for private information before sharing.

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

Launcher sign-in is for updates and add-ons. By default, only the session token is saved in `.updaterToken`. If it expires, sign in again.

**Remember launcher password** is off by default. If enabled, the launcher encrypts your username and password in `.ebonhold-saved-login` and uses them to renew expired sessions. The key is stored separately in `${XDG_STATE_HOME:-$HOME/.local/state}/ebonhold-launcher/.ebonhold-credential-key`. Both files are owner-only (`0600`). No keyring is required.

A copy of the game folder alone can't reveal your saved password, but it can contain a still-valid `.updaterToken`. Apps running under your Linux account or anyone who gets both the encrypted login and its key can still read the password. Leave this off on shared computers. Don't share the token or saved login file.

Choose **Sign out…** on **Home** to clear the launcher session and any saved password for the selected profile. This doesn't sign you out of WoW. From a terminal, use `--client-dir=/path/to/Ebonhold --forget-login`. To start remembering your password, sign out, sign back in, and tick **Remember launcher password**.

`--relogin` asks for credentials without using the previous token or password. Declining to remember a successful new sign-in removes the previous saved password. Verify and dry-run can use a saved password but do not change saved credentials.

To save only your account name, select **Remember Account Name** in WoW. Anyone who can read the game folder can see the name, so leave it off on shared computers. To clear the name, uncheck it and sign in to WoW again.

Older `.ebonhold-login/credentials.json` files are checked for safe removal, never imported. The launcher does not read or remove entries in your desktop password wallet. If legacy cleanup fails, check that both the directory and file are inside the intended game folder and neither is a symlink before removing the file manually.

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

### Check the profile isolation proof

[Lean 4](https://lean-lang.org/install/manual/) checks a model of the game folders: switching profiles leaves both folders intact, and folder edits cannot make them overlap. The Go test checks the launcher's folder selection and validation against cases generated by the model. It does not prove the Go code or model filesystem aliases such as symlinks.

Use Lean on `PATH` with the version pinned in `internal/launcher/testdata/proofs/lean-toolchain`. If you use Elan, install that toolchain first. Then run:

```sh
go test -tags leanproof ./internal/launcher -run '^TestProfileIsolationProof$' -count=1 -v
```

Go CI runs this check on pull requests and pushes to `main`. Lean is not needed to build or run the launcher.
