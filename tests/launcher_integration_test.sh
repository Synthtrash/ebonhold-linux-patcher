#!/usr/bin/env bash
set -euo pipefail

# Keep failure-path tests non-interactive even on desktops with zenity installed.
export GUI=false

repo_root="$(dirname "$(dirname "$(realpath "$0")")")"
test_root="$(mktemp -d)"
mock_bin="${test_root}/bin"
trap 'rm -rf "${test_root}"' EXIT

mkdir -p "${mock_bin}" "${test_root}/Cache/WDB/enUS"
cp "${repo_root}/launcher.sh" "${test_root}/launcher.sh"
chmod +x "${test_root}/launcher.sh"
printf 'cached-token' >"${test_root}/.updaterToken"
printf 'stale cache' >"${test_root}/Cache/WDB/enUS/cache.wdb"
printf 'updated game file' >"${test_root}/expected-file"
expected_hash="$(md5sum "${test_root}/expected-file" | cut -d' ' -f1)"
expected_b64="$(printf '%s' "${expected_hash}" | xxd -r -p | base64 -w0)"
realmlist_hash="$(printf 'set realmlist manifest.realm\n' | md5sum | cut -d' ' -f1)"
realmlist_b64="$(printf '%s' "${realmlist_hash}" | xxd -r -p | base64 -w0)"

cat >"${mock_bin}/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

output=""
url=""
config=""
for ((i = 1; i <= $#; i++)); do
  if [[ "${!i}" == "-o" ]]; then
    next=$((i + 1))
    output="${!next}"
  fi
  if [[ "${!i}" == "--config" ]]; then
    next=$((i + 1))
    config="${!next}"
  fi
  [[ "${!i}" == http* ]] && url="${!i}"
done
if [[ -n "${config}" && -f "${config}" ]]; then
  while IFS= read -r config_line; do
    if [[ "${config_line}" == 'url = "'* ]]; then
      url="${config_line#url = \"}"
      url="${url%\"}"
    fi
  done <"${config}"
fi
if [[ "${url}" == *"/games" ]]; then
  if [[ -n "${MOCK_GAMES_RESPONSE:-}" ]]; then
    printf '%s' "${MOCK_GAMES_RESPONSE}"
  else
    printf '%s' "$(<"${MOCK_MANIFEST_FILE}")"
  fi
  printf '\n%s' "${MOCK_GAMES_HTTP_CODE:-200}"
elif [[ "${url}" == *"/download?file_ids="* ]]; then
  printf '%s' '{"url":"https://download.test/file"}' >"${output}"
  printf '200'
elif [[ "${url}" == *"/addons/download?addon_ids="* ]]; then
  if [[ -n "${MOCK_ADDON_DOWNLOAD_RESPONSE:-}" ]]; then
    printf '%s' "${MOCK_ADDON_DOWNLOAD_RESPONSE}"
  else
    printf '%s' '{"success":true,"files":[{"file_id":209,"filename":"Interface/AddOns/Elvui.zip","url":"https://download.test/addon"}]}'
  fi
elif [[ "${url}" == *"/addons" ]]; then
  printf '%s' "$(<"${MOCK_ADDONS_FILE}")"
elif [[ "${url}" == "https://download.test/file" ]]; then
  printf 'updated game file' >"${output}"
elif [[ "${url}" == "https://download.test/addon" ]]; then
  printf 'mock addon archive' >"${output}"
elif [[ "${url}" == *"/status" ]]; then
  printf '%s' '{"success":true,"data":{"online":true,"serverName":"test"}}'
else
  exit 1
fi
EOF
chmod +x "${mock_bin}/curl"

cat >"${mock_bin}/unzip" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [[ "${1}" == "-Z1" ]]; then
  printf 'ElvUI/\nElvUI/ElvUI.toc\n'
else
  destination="${4}"
  mkdir -p "${destination}/ElvUI"
  printf '## Title: ElvUI\n' >"${destination}/ElvUI/ElvUI.toc"
fi
EOF
chmod +x "${mock_bin}/unzip"

cat >"${mock_bin}/zipinfo" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [[ "${MOCK_ARCHIVE_HAS_SYMLINK:-false}" == "true" ]]; then
  printf 'lrwxrwxrwx  3.0 unx        0 bx        0 stor 00:00 2026-08-05 ElvUI/link\n'
else
  printf '%s\n' '-rw-r--r--  3.0 unx        1 bx        1 stor 00:00 2026-08-05 ElvUI/ElvUI.toc'
fi
EOF
chmod +x "${mock_bin}/zipinfo"

manifest_file="${test_root}/manifest.json"
addons_file="${test_root}/addons.json"
printf '{"success":true,"addons":[{"id":209,"name":"Elvui","description":"Custom interface","file_size_bytes":"100","updated_at":"2026-08-05T15:40:45.000Z"},{"id":212,"name":"Quest Helper","description":"Quest guidance","file_size_bytes":"100","updated_at":"2026-08-05T15:40:45.000Z"}]}' >"${addons_file}"
printf '{"success":true,"data":{"common":{"files":[{"id":"3","file_path_from_game_root":"Ebonhold/common-required.dat","file_hash":"%s","option_slug":null},{"id":"6","file_path_from_game_root":"Ebonhold/common-optional.dat","file_hash":"%s","option_slug":"optional"}]},"games":[{"slug":"roguelike-prod","realmlist":"test.realm","files":[{"id":"1","file_path_from_game_root":"Data/patch-test.MPQ","file_hash":"%s","option_slug":null},{"id":"2","file_path_from_game_root":"Data/enUS/realmlist.wtf","file_hash":"%s","option_slug":null},{"id":"4","file_path_from_game_root":"Ebonhold/new-required.dat","file_hash":"%s","option_slug":null},{"id":"5","file_path_from_game_root":"Ebonhold/optional.dat","file_hash":"%s","option_slug":"optional"}]}]}}' "${expected_b64}" "${expected_b64}" "${expected_b64}" "${realmlist_b64}" "${expected_b64}" "${expected_b64}" >"${manifest_file}"

symlink_client="${test_root}/symlink-client"
mkdir -p "${symlink_client}/Data"
ln -s "${test_root}/launcher.sh" "${symlink_client}/ebonhold-launcher.sh"
printf 'cached-token' >"${symlink_client}/.updaterToken"
cp "${test_root}/expected-file" "${symlink_client}/Data/patch-test.MPQ"
symlink_output="$(PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${symlink_client}/ebonhold-launcher.sh" --dry-run)"
[[ "${symlink_output}" == *"[OK]"* ]]
[[ "${symlink_output}" == *"Data/patch-test.MPQ"* ]]
[[ "${symlink_output}" == *"Ebonhold/common-required.dat"* ]]
[[ "${symlink_output}" == *"Ebonhold/new-required.dat"* ]]
[[ "${symlink_output}" != *"realmlist.wtf"* ]]
[[ "${symlink_output}" != *"Ebonhold/common-optional.dat"* ]]
[[ "${symlink_output}" != *"Ebonhold/optional.dat"* ]]
full_output="$(PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --dry-run --full)"
[[ "${full_output}" != *"realmlist.wtf"* ]]
[[ "${full_output}" == *"Ebonhold/common-optional.dat"* ]]
[[ "${full_output}" == *"Ebonhold/optional.dat"* ]]
quick_output="$(PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --dry-run --quick)"
[[ "${quick_output}" == *"Data/patch-test.MPQ"* ]]
[[ "${quick_output}" != *"Ebonhold/common-required.dat"* ]]
[[ "${quick_output}" != *"Ebonhold/new-required.dat"* ]]
if PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --dry-run --quick --full >/dev/null 2>&1; then
  printf '%s\n' '--quick and --full were accepted together.' >&2
  exit 1
fi
if PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --verify --dry-run >/dev/null 2>&1; then
  printf '%s\n' '--verify and --dry-run were accepted together.' >&2
  exit 1
fi
if PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --select-addons --addons=Elvui >/dev/null 2>&1; then
  printf '%s\n' '--select-addons and --addons were accepted together.' >&2
  exit 1
fi
if PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --status --quick >/dev/null 2>&1; then
  printf '%s\n' '--status and --quick were accepted together.' >&2
  exit 1
fi
path_output="$(PATH="${symlink_client}:${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" TEST_CWD="${test_root}" bash -c 'cd "${TEST_CWD}" && ebonhold-launcher.sh --dry-run')"
[[ "${path_output}" == *"[OK]"* ]]
interpreter_output="$(PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" TEST_CWD="${test_root}" bash -c 'cd "${TEST_CWD}" && bash launcher.sh --dry-run')"
[[ "${interpreter_output}" == *"Ebonhold/common-required.dat"* ]]
[[ "${interpreter_output}" == *"Ebonhold/new-required.dat"* ]]

renamed_manifest_file="${test_root}/renamed-manifest.json"
sed 's/roguelike-prod/renamed-game/g' "${manifest_file}" >"${renamed_manifest_file}"
renamed_output="$(PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${renamed_manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --dry-run --game=renamed-game)"
[[ "${renamed_output}" == *"Ebonhold/common-required.dat"* ]]

bad_manifest_output="$(PATH="${mock_bin}:${PATH}" GUI=false MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" MOCK_GAMES_RESPONSE='{"error":"manifest service unavailable"}' MOCK_GAMES_HTTP_CODE=503 script -q -e -c "\"${test_root}/launcher.sh\" --dry-run" /dev/null 2>&1 || true)"
[[ "${bad_manifest_output}" == *"manifest service unavailable"* ]]
[[ "${bad_manifest_output}" != *"Game slug"* ]]

status_output="$(PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --status)"
[[ "${status_output}" == *"Server test is online."* ]]

addons_output="$(PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --list-addons)"
[[ "${addons_output}" == *"Elvui"* ]]

mkdir -p "${test_root}/Interface/AddOns/Elvui"
printf 'old addon' >"${test_root}/Interface/AddOns/Elvui/Elvui.toc"
touch -d '2010-01-01 00:00:00 UTC' "${test_root}/Interface/AddOns/Elvui/Elvui.toc"
addon_check_output="$(PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --check-addons)"
[[ "${addon_check_output}" == *"[UPDATE AVAILABLE]"* ]]
[[ "${addon_check_output}" == *"Elvui"* ]]

if PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --dry-run --addons=unknown >/dev/null 2>&1; then
  printf 'Unknown addon was accepted.\n' >&2
  exit 1
fi

PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --dry-run --addons='Quest Helper' >/dev/null
quiet_addon_output="$(PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --dry-run --quiet --addons='Quest Helper')"
[[ -z "${quiet_addon_output}" ]]
if PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --verify --addons='Quest Helper' >/dev/null 2>&1; then
  printf 'Verify mode accepted addon changes.\n' >&2
  exit 1
fi

if PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" MOCK_ADDON_DOWNLOAD_RESPONSE='{"success":true,"files":[]}' "${test_root}/launcher.sh" --addons=Elvui >/dev/null 2>&1; then
  printf 'Partial addon download response was accepted.\n' >&2
  exit 1
fi
if PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" MOCK_ARCHIVE_HAS_SYMLINK=true "${test_root}/launcher.sh" --addons=Elvui >/dev/null 2>&1; then
  printf 'Symbolic link addon archive was accepted.\n' >&2
  exit 1
fi
rm -f "${test_root}/Data/enUS/realmlist.wtf"

quiet_output="$(PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --dry-run --quiet)"
[[ -z "${quiet_output}" ]]
[[ ! -e "${test_root}/Data/enUS/realmlist.wtf" ]]
[[ -f "${test_root}/Cache/WDB/enUS/cache.wdb" ]]

mkdir -p "${test_root}/Data"
cp "${test_root}/expected-file" "${test_root}/Data/patch-test.MPQ"
printf 'stale lowercase copy' >"${test_root}/Data/patch-test.mpq"
verify_output="$(PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --verify 2>&1 || true)"
[[ "${verify_output}" == *"[STALE]"* ]]
[[ "${verify_output}" == *"patch-test.mpq"* ]]
[[ -f "${test_root}/Data/patch-test.mpq" ]]
[[ -f "${test_root}/Data/patch-test.MPQ" ]]

printf '{"success":true,"data":{"common":{"files":[]},"games":[{"slug":"roguelike-prod","realmlist":"test.realm","files":[{"id":"1&evil","file_path_from_game_root":"Data/patch-test.MPQ","file_hash":"%s","option_slug":null}]}]}}' "${expected_b64}" >"${manifest_file}"
if PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --dry-run --quiet; then
  printf 'Malformed manifest file ID was accepted.\n' >&2
  exit 1
fi

printf '{"success":true,"data":{"common":{"files":[]},"games":[{"slug":"roguelike-prod","realmlist":"test.realm","files":[{"id":"1","file_path_from_game_root":"../patch-escape","file_hash":"%s","option_slug":null}]}]}}' "${expected_b64}" >"${manifest_file}"
if PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --dry-run --quiet; then
  printf 'Path traversal manifest was accepted.\n' >&2
  exit 1
fi

printf '{"success":true,"data":{"common":{"files":[]},"games":[{"slug":"roguelike-prod","realmlist":"test.realm","files":[{"id":"1","file_path_from_game_root":"Data/patch-test.MPQ","file_hash":"%s","option_slug":null},{"id":"2","file_path_from_game_root":"Data/enUS/realmlist.wtf","file_hash":"%s","option_slug":null}]}]}}' "${expected_b64}" "${realmlist_b64}" >"${manifest_file}"
rm -f "${test_root}/Data/patch-test.MPQ"
quiet_verify_output="$(PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --verify --quiet 2>&1 || true)"
[[ "${quiet_verify_output}" == *"[MISSING]"* ]]
cp "${test_root}/expected-file" "${test_root}/Data/patch-test.MPQ"
mkdir -p "${test_root}/Interface/AddOns/LegacyElvUI" "${test_root}/Interface/AddOns/SharedLibrary"
printf 'legacy' >"${test_root}/Interface/AddOns/LegacyElvUI/legacy.toc"
printf 'shared' >"${test_root}/Interface/AddOns/SharedLibrary/shared.lua"
printf '{"addons":{"209":{"name":"Elvui","updated_at":"2026-01-01T00:00:00.000Z","folders":["LegacyElvUI","SharedLibrary"]},"212":{"name":"Quest Helper","updated_at":"2026-01-01T00:00:00.000Z","folders":["SharedLibrary"]}}}' >"${test_root}/Interface/AddOns/.ebonhold-launcher-addons.json"
PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --quiet >/dev/null
[[ ! -e "${test_root}/Data/patch-test.mpq" ]]
[[ -f "${test_root}/Data/patch-test.MPQ" ]]
printf 'stale lowercase copy' >"${test_root}/Data/patch-test.mpq"
rm -f "${test_root}/Data/patch-test.MPQ"
quiet_update_output="$(PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --quiet --addons=Elvui -- /usr/bin/touch "${test_root}/game-launched")"
[[ -z "${quiet_update_output}" ]]
[[ "$(<"${test_root}/Data/patch-test.MPQ")" == "updated game file" ]]
[[ ! -e "${test_root}/Data/patch-test.mpq" ]]
[[ "$(<"${test_root}/Interface/AddOns/ElvUI/ElvUI.toc")" == "## Title: ElvUI" ]]
[[ ! -e "${test_root}/Interface/AddOns/LegacyElvUI" ]]
[[ -f "${test_root}/Interface/AddOns/SharedLibrary/shared.lua" ]]
MOCK_ARGS_FILE="${test_root}/passthrough.args" PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --quiet -- /bin/sh -c 'printf "%s\n" "$@" >"$MOCK_ARGS_FILE"' sh --quiet --game=forwarded
mapfile -t passthrough_args <"${test_root}/passthrough.args"
[[ "${passthrough_args[0]}" == "--quiet" ]]
[[ "${passthrough_args[1]}" == "--game=forwarded" ]]
jq '.addons["209"].folders += ["SharedLibrary"]' "${test_root}/Interface/AddOns/.ebonhold-launcher-addons.json" >"${test_root}/Interface/AddOns/.ebonhold-launcher-addons.tmp" && mv "${test_root}/Interface/AddOns/.ebonhold-launcher-addons.tmp" "${test_root}/Interface/AddOns/.ebonhold-launcher-addons.json"
remove_shared_output="$(PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --remove-addons='Quest Helper')"
[[ "${remove_shared_output}" == *"[SKIP]"* ]]
[[ -f "${test_root}/Interface/AddOns/SharedLibrary/shared.lua" ]]
[[ -z "$(jq -r '.addons["212"] // empty' "${test_root}/Interface/AddOns/.ebonhold-launcher-addons.json")" ]]
[[ -n "$(jq -r '.addons["209"] // empty' "${test_root}/Interface/AddOns/.ebonhold-launcher-addons.json")" ]]
addon_check_after_install="$(PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --check-addons)"
[[ "${addon_check_after_install}" == *"[CURRENT]"* ]]
rm -f "${test_root}/Interface/AddOns/ElvUI/ElvUI.toc"
addon_check_after_removal="$(PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --check-addons)"
[[ "${addon_check_after_removal}" == *"[UPDATE AVAILABLE]"* ]]
remove_output="$(PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/launcher.sh" --remove-addons=Elvui)"
[[ "${remove_output}" == *"[REMOVED]"* ]]
[[ ! -e "${test_root}/Interface/AddOns/ElvUI" ]]
backup_found=false
for backup in "${test_root}"/.ebonhold-removed-addons/Elvui-*; do
  [[ -d "${backup}/ElvUI" ]] && backup_found=true
done
[[ "${backup_found}" == "true" ]]
[[ -z "$(jq -r '.addons["209"] // empty' "${test_root}/Interface/AddOns/.ebonhold-launcher-addons.json")" ]]
[[ -f "${test_root}/game-launched" ]]
[[ ! -e "${test_root}/Cache/WDB/enUS/cache.wdb" ]]
[[ -f "${test_root}/Cache/invalid" ]]
[[ "$(<"${test_root}/Data/enUS/realmlist.wtf")" == "set realmlist test.realm" ]]
