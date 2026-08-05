#!/usr/bin/env bash
set -euo pipefail

repo_root="$(dirname "$(dirname "$(realpath "$0")")")"
test_root="$(mktemp -d)"
mock_bin="${test_root}/bin"
trap 'rm -rf "${test_root}"' EXIT

mkdir -p "${mock_bin}" "${test_root}/Cache/WDB/enUS"
cp "${repo_root}/updater.sh" "${test_root}/updater.sh"
chmod +x "${test_root}/updater.sh"
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
for ((i = 1; i <= $#; i++)); do
  if [[ "${!i}" == "-o" ]]; then
    next=$((i + 1))
    output="${!next}"
  fi
  [[ "${!i}" == http* ]] && url="${!i}"
done
if [[ "${url}" == *"/games" ]]; then
  printf '%s' "$(<"${MOCK_MANIFEST_FILE}")"
elif [[ "${url}" == *"/download?file_ids="* ]]; then
  printf '%s' '{"url":"https://download.test/file"}' >"${output}"
  printf '200'
elif [[ "${url}" == *"/addons/download?addon_ids="* ]]; then
  printf '%s' '{"success":true,"files":[{"file_id":209,"filename":"Interface/AddOns/Elvui.zip","url":"https://download.test/addon"}]}'
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

manifest_file="${test_root}/manifest.json"
addons_file="${test_root}/addons.json"
printf '{"success":true,"addons":[{"id":209,"name":"Elvui","description":"Custom interface","file_size_bytes":"100","updated_at":"2026-08-05T15:40:45.000Z"}]}' >"${addons_file}"
printf '{"success":true,"data":{"common":{"files":[]},"games":[{"slug":"roguelike-prod","realmlist":"test.realm","files":[{"id":"1","file_path_from_game_root":"Data/patch-test.MPQ","file_hash":"%s","option_slug":null},{"id":"2","file_path_from_game_root":"Data/enUS/realmlist.wtf","file_hash":"%s","option_slug":null}]}]}}' "${expected_b64}" "${realmlist_b64}" >"${manifest_file}"

symlink_client="${test_root}/symlink-client"
mkdir -p "${symlink_client}/Data"
ln -s "${test_root}/updater.sh" "${symlink_client}/ebonhold-updater.sh"
printf 'cached-token' >"${symlink_client}/.updaterToken"
cp "${test_root}/expected-file" "${symlink_client}/Data/patch-test.MPQ"
symlink_output="$(PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${symlink_client}/ebonhold-updater.sh" --dry-run)"
[[ "${symlink_output}" == *"[OK]"* ]]
[[ "${symlink_output}" == *"Data/patch-test.MPQ"* ]]

status_output="$(PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/updater.sh" --status)"
[[ "${status_output}" == *"Server test is online."* ]]

addons_output="$(PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/updater.sh" --list-addons)"
[[ "${addons_output}" == *"Elvui"* ]]

mkdir -p "${test_root}/Interface/AddOns/Elvui"
printf 'old addon' >"${test_root}/Interface/AddOns/Elvui/Elvui.toc"
touch -d '2010-01-01 00:00:00 UTC' "${test_root}/Interface/AddOns/Elvui/Elvui.toc"
addon_check_output="$(PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/updater.sh" --check-addons)"
[[ "${addon_check_output}" == *"[UPDATE AVAILABLE]"* ]]
[[ "${addon_check_output}" == *"Elvui"* ]]

if PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/updater.sh" --dry-run --addons=unknown >/dev/null 2>&1; then
  printf 'Unknown addon was accepted.\n' >&2
  exit 1
fi

PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/updater.sh" --dry-run --quiet
[[ ! -e "${test_root}/Data/enUS/realmlist.wtf" ]]
[[ -f "${test_root}/Cache/WDB/enUS/cache.wdb" ]]

printf '{"success":true,"data":{"common":{"files":[]},"games":[{"slug":"roguelike-prod","realmlist":"test.realm","files":[{"id":"1","file_path_from_game_root":"../patch-escape","file_hash":"%s","option_slug":null}]}]}}' "${expected_b64}" >"${manifest_file}"
if PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/updater.sh" --dry-run --quiet; then
  printf 'Path traversal manifest was accepted.\n' >&2
  exit 1
fi

printf '{"success":true,"data":{"common":{"files":[]},"games":[{"slug":"roguelike-prod","realmlist":"test.realm","files":[{"id":"1","file_path_from_game_root":"Data/patch-test.MPQ","file_hash":"%s","option_slug":null},{"id":"2","file_path_from_game_root":"Data/enUS/realmlist.wtf","file_hash":"%s","option_slug":null}]}]}}' "${expected_b64}" "${realmlist_b64}" >"${manifest_file}"
PATH="${mock_bin}:${PATH}" MOCK_MANIFEST_FILE="${manifest_file}" MOCK_ADDONS_FILE="${addons_file}" "${test_root}/updater.sh" --quiet --addons=Elvui -- /usr/bin/touch "${test_root}/game-launched"
[[ "$(<"${test_root}/Data/patch-test.MPQ")" == "updated game file" ]]
[[ "$(<"${test_root}/Interface/AddOns/ElvUI/ElvUI.toc")" == "## Title: ElvUI" ]]
[[ -f "${test_root}/game-launched" ]]
[[ ! -e "${test_root}/Cache/WDB/enUS/cache.wdb" ]]
[[ -f "${test_root}/Cache/invalid" ]]
[[ "$(<"${test_root}/Data/enUS/realmlist.wtf")" == "set realmlist test.realm" ]]
