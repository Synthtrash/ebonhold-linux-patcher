# ebonhold-linux-patcher

A bash script that downloads and updates the Project Ebonhold game client on Linux.

---

## What it does

- Logs in via the Ebonhold API and caches your session token.
- Checks the manifest for patches (`patch-4`, `patch-5`, `patch-6`) using MD5 hashes.
- Downloads missing or mismatched patches.
- Also downloads required extra files:
  - `AwesomeWotlkLib.dll`, `Wow.exe`, `skia.dll`, `ebonhold.dll`
  - `Data/patch-X.MPQ`, `Data/patch-I.MPQ`
- Touches `Cache/invalid` when a patch changes (so the game rebuilds its cache).

---

## Prerequisites

- **curl**, **jq**, **md5sum**, **bc** – usually pre‑installed on most Linux systems.
- **zenity** (optional) – for GUI prompts; the script falls back to terminal prompts if missing.

---

## Usage

### Make it executable
```bash
chmod +x updater.sh
./updater.sh
or
./updater.sh --debug
```

## Credits

This script is a **simplified fork** of the original community‑maintained updater for Project Ebonhold.
**Original script:** ([sigboe/ebonhold-updater](https://github.com/sigboe/ebonhold-updater)) by **Sigboe**
Special thanks to the developers and community who reverse‑engineered the API and made the Linux port possible.
