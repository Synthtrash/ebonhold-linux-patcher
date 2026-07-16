#!/usr/bin/env bash

: "${debug:=false}"
: "${game:=roguelike-prod}"
verify_only=false
quiet=false
dry_run=false
parallel=4

scriptdir="$(dirname "$(realpath "${0}")")"
targetdir="${PWD}"

login_api="https://api.project-ebonhold.com/api/auth/login"
games_api="https://api.project-ebonhold.com/api/launcher/games"
status_api="https://api.project-ebonhold.com/api/server/status"
patch_download_base="https://api.project-ebonhold.com/api/launcher/download?file_ids="
token_file="${targetdir}/.updaterToken"

manage_token() {
  local token=""
  local games_response=""
  local user=""
  local pass=""

  if [[ -f "${token_file}" ]]; then
    token="$(<"${token_file}")"
    if [[ -z "${token}" || "${token}" == "null" ]]; then
      debug "Token file exists but is empty or contains 'null'. Clearing it."
      rm -f "${token_file}"
      token=""
    fi
  fi

  if [[ -n "${token}" ]]; then
    debug "Auth token found, verifying token."
    games_response="$(curl -s \
      -H "Authorization: Bearer ${token}" \
      -H "User-Agent: EbonholdLauncher/1.0" \
      -H "Accept: application/json" \
      -H "X-Client-Id: EbonholdLauncher" \
      -H "Origin: https://project-ebonhold.com" \
      -H "Referer: https://project-ebonhold.com/download" \
      "${games_api}")"
    if [[ -z "${games_response}" || "${games_response}" == "null" ]] || jq -e '.success == false' <<<"${games_response}" >/dev/null 2>&1; then
      debug "Token invalid or expired. Clearing session."
      rm -f "${token_file}"
      token=""
      games_response=""
    else
      debug "Token verified successfully."
      authToken="${token}"
      games_manifest="${games_response}"
      return 0
    fi
  fi

  debug "No valid token found. Please log in."

  user="$(prompt_text "Ebonhold Login" "Enter your username:")" || error 1 "Cannot prompt for username: no terminal and no display available. Run interactively or install zenity."
  pass="$(prompt_password "Ebonhold Login" "Password for ${user}")" || error 1 "Cannot prompt for password: no terminal and no display available."

  debug "Posting credentials to authentication portal..."
  session="$(printf '{"username":"%s","password":"%s","rememberMe":true}' "${user}" "${pass}" |
    curl -s -X POST -w "\n%{http_code}" \
      -H "Content-Type: application/json" \
      -H "User-Agent: EbonholdLauncher/1.0" \
      -d @- \
      "${login_api}")"

  http_code="$(sed -n '$p' <<<"${session}")"
  session="$(sed '$d' <<<"${session}")"

  debug "HTTP return code ${http_code}"
  if [[ "${debug}" == "true" ]]; then
    debug "Login response body:"
    printf '%s\n' "${session}" | jq . 2>/dev/null || printf '%s\n' "${session}" >&2
  fi

  if ! jq -e '.success' <<<"${session}" >/dev/null 2>&1 && [[ "${http_code}" -ne 200 ]]; then
    message="$(jq -r '.message // empty' <<<"${session}")"
    [[ -z "${message}" ]] && message="HTTP Gateway Reject Code: ${http_code}"
    error 1 "Session authorization failed.\n${message}"
  fi

  token="$(jq -r '.token' <<<"${session}")"
  if [[ -z "${token}" || "${token}" == "null" ]]; then
    error 1 "Login succeeded but no valid token was returned."
  fi

  printf '%s' "${token}" >"${token_file}"
  chmod 600 "${token_file}"
  debug "Secure auth token serialized locally (permissions: 600)."

  games_response="$(curl -s \
    -H "Authorization: Bearer ${token}" \
    -H "User-Agent: EbonholdLauncher/1.0" \
    -H "Accept: application/json" \
    -H "X-Client-Id: EbonholdLauncher" \
    -H "Origin: https://project-ebonhold.com" \
    -H "Referer: https://project-ebonhold.com/download" \
    "${games_api}")"
  if [[ -z "${games_response}" || "${games_response}" == "null" ]] || jq -e '.success == false' <<<"${games_response}" >/dev/null 2>&1; then
    error 1 "Failed to fetch games manifest with new token. Please try again."
  fi

  unset user pass
  authToken="${token}"
  games_manifest="${games_response}"
  debug "Authentication successful."
}

if [[ -t 0 ]]; then interactiveShell="true"; else interactiveShell="false"; fi
if [[ "$XDG_SESSION_TYPE" = "x11" ]] ||
  [[ "$XDG_SESSION_TYPE" = "wayland" ]] ||
  [[ -n "$DISPLAY" ]] ||
  [[ -n "$WAYLAND_DISPLAY" ]]; then
  GUI="${GUI:=true}"
else
  GUI="false"
fi
[[ -x "$(command -v zenity)" ]] || GUI="false"
if [[ "${GUI}" == "false" ]] && [[ "${interactiveShell}" == "false" ]]; then
  error 1 "Cannot run in non-interactive mode without a display. Install zenity or run interactively first."
fi

BLUE="\033[0;34m" RED="\033[0;31m" YELLOW="\033[0;33m" GREEN="\033[0;32m" NC="\033[0m"

debug() {
  local msg="${*}"
  if [[ "${debug}" == "true" ]] && [[ "${quiet}" == "false" ]]; then
    printf '%s[DEBUG]:%s %s%s%s\n' "${BLUE}" "${NC}" "${YELLOW}" "${msg}" "${NC}" >&2
  fi
}

error() {
  local exit_code="0"
  local msg
  if [[ "${1}" =~ ^[0-9]+$ ]]; then
    exit_code="${1}"
    shift
  fi
  msg="${*}"
  if [[ "${GUI}" == "true" ]]; then
    zenity --error --title="Error" --text="${msg}" --width=400 2>/dev/null
  else
    printf '\n\033[2K%s[ERROR]:%s %s%b%s\n' "${RED}" "${NC}" "${YELLOW}" "${msg}" "${NC}" >&2
  fi
  [[ "${exit_code}" -ge "1" ]] && exit "${exit_code}"
}

prompt_text() {
  local title="${1}" text="${2}"
  if [[ "${GUI}" == "true" ]]; then
    zenity --entry --title="${title}" --text="${text}" --width=400 2>/dev/null || return 1
  else
    local input
    printf '%s\n' "${title}" >&2
    read -r -p "${text} > " input
    [[ -z "${input}" ]] && return 1
    printf '%s' "${input}"
  fi
}

prompt_password() {
  local title="${1}" text="${2}"
  if [[ "${GUI}" == "true" ]]; then
    zenity --password --title="${title}" --text="${text}" --width=400 2>/dev/null
  else
    local input
    printf '%s\n' "${title}" >&2
    read -r -s -p "${text} > " input
    [[ -z "${input}" ]] && return 1
    printf '%s' "${input}"
  fi
}

format_bytes() {
  local bytes="${1}"
  if ((bytes < 1024)); then
    printf '%s B\n' "${bytes}"
  elif ((bytes < 1048576)); then
    printf '%.2f KB\n' "$(printf 'scale=2; %s/1024\n' "${bytes}" | bc)"
  else
    printf '%.2f MB\n' "$(printf 'scale=2; %s/1048576\n' "${bytes}" | bc)"
  fi
}

download_file_by_id() {
  local file_id="$1"
  local dest_path="$2"
  local description="${3:-$dest_path}"
  local retry=0
  local max_retries=1
  local tmp_out=""

  debug "Downloading file ID ${file_id} -> ${dest_path}"

  while [[ $retry -le $max_retries ]]; do
    tmp_out="$(mktemp)"
    trap 'rm -f "${tmp_out}"' EXIT INT TERM
    status="$(curl -s -w "%{http_code}" -o "${tmp_out}" \
      -H "Authorization: Bearer ${authToken}" \
      -H "User-Agent: EbonholdLauncher/1.0" \
      -H "Accept: application/json" \
      -H "X-Client-Id: EbonholdLauncher" \
      -H "Origin: https://project-ebonhold.com" \
      -H "Referer: https://project-ebonhold.com/download" \
      "${patch_download_base}${file_id}" 2>/dev/null)"
    response="$(<"${tmp_out}")"
    rm -f "${tmp_out}"
    trap - EXIT INT TERM

    if [[ "${status}" != "200" ]]; then
      if [[ "${status}" == "401" && $retry -lt $max_retries ]]; then
        debug "Token rejected (401). Re‑authenticating..."
        rm -f "${token_file}"
        manage_token
        ((retry++))
        debug "Retry ${retry}/${max_retries} with new token."
        continue
      fi
      printf '%s[ERROR]%s Failed to get download URL for ID %s (HTTP %s)\n' "${RED}" "${NC}" "${file_id}" "${status}"
      printf 'Server response:\n'
      printf '%s\n' "${response}" | jq . 2>/dev/null || printf '%s\n' "${response}"
      return 1
    fi

    url="$(jq --raw-output '.files[0].url' <<<"${response}" 2>/dev/null)"
    if [[ -z "${url}" || "${url}" == "null" ]]; then
      url="$(jq --raw-output '.url' <<<"${response}" 2>/dev/null)"
    fi
    if [[ -z "${url}" || "${url}" == "null" ]]; then
      printf '%s[ERROR]%s No download URL found for ID %s\n' "${RED}" "${NC}" "${file_id}"
      return 1
    fi

    debug "Download URL: ${url}"
    mkdir -p "$(dirname "${dest_path}")"

    if [[ "${quiet}" == "false" ]]; then
      printf '%s[DOWNLOADING]%s %s...\n' "${BLUE}" "${NC}" "${description}"
    fi
    if ! curl -fL# "${url}" -o "${dest_path}"; then
      printf '%s[ERROR]%s Failed to download %s\n' "${RED}" "${NC}" "${description}"
      return 1
    fi

    local size="$(stat -c%s "${dest_path}" 2>/dev/null || printf '0')"
    if [[ "${quiet}" == "false" ]]; then
      printf '%s[FINISHED]%s %s (%s)\n\n' "${GREEN}" "${NC}" "${description}" "$(format_bytes "${size}")"
    fi
    return 0
  done

  printf '%s[ERROR]%s Still getting 401 after re‑authentication. Please try again.\n' "${RED}" "${NC}"
  return 1
}

check_server_status() {
  debug "Checking server status..."
  local response="$(curl -s "${status_api}")"
  if [[ -z "${response}" ]] || ! jq -e '.success' <<<"${response}" >/dev/null 2>&1; then
    printf '%s[WARN]%s Could not retrieve server status.\n' "${YELLOW}" "${NC}"
    return 0
  fi
  local online="$(jq -r '.data.online' <<<"${response}")"
  local realm_name="$(jq -r '.data.serverName // "unknown"' <<<"${response}")"
  if [[ "${online}" == "true" ]]; then
    printf '%s[INFO]%s Server %s is online.\n' "${GREEN}" "${NC}" "${realm_name}"
  else
    printf '%s[WARN]%s Server %s appears offline.\n' "${YELLOW}" "${NC}" "${realm_name}"
    printf '  You can still update files, but the game may not work until the server is back.\n'
  fi
  printf '\n'
}

update_realmlist() {
  local realmlist="$1"
  local dest="${targetdir}/Data/enUS/realmlist.wtf"
  mkdir -p "$(dirname "${dest}")"
  if [[ -f "${dest}" ]]; then
    local current="$(<"${dest}")"
    if [[ "${current}" != "set realmlist ${realmlist}" ]]; then
      printf '%s[REALMLIST]%s Updating %s to %s\n' "${YELLOW}" "${NC}" "${dest}" "${realmlist}"
      printf 'set realmlist %s\n' "${realmlist}" >"${dest}"
    else
      debug "realmlist.wtf already correct."
    fi
  else
    printf '%s[REALMLIST]%s Creating %s with %s\n' "${YELLOW}" "${NC}" "${dest}" "${realmlist}"
    printf 'set realmlist %s\n' "${realmlist}" >"${dest}"
  fi
}

collect_core_files() {
  local manifest="$1"
  local game_slug="$2"
  local files_json

  files_json="$(jq -c '.data.common.files[] | select(.option_slug == null)' <<<"$manifest")"
  files_json+=$'\n'"$(jq -c --arg slug "$game_slug" '.data.games[] | select(.slug == $slug) | .files[] | select(.option_slug == null)' <<<"$manifest")"

  printf '%s\n' "$files_json" | grep -v '^$'
}

verify_files() {
  local files_json="$1"
  local mismatches=0 missing=0 ok=0 total=0
  local path expected_b64 expected_md5 dest local_md5

  printf '\nVerifying files...\n\n'

  while read -r file; do
    [[ -z "${file}" ]] && continue
    path="$(jq -r '.file_path_from_game_root' <<<"$file")"
    expected_b64="$(jq -r '.file_hash' <<<"$file")"
    expected_md5="$(printf '%s' "${expected_b64}" | base64 --decode | od -An -tx1 | tr -d ' \n')"
    dest="${targetdir}/${path}"
    total=$((total + 1))

    if [[ ! -f "${dest}" ]]; then
      printf '%s[MISSING]%s %s\n' "${RED}" "${NC}" "${path}"
      missing=$((missing + 1))
    else
      local_md5="$(md5sum "${dest}" | cut -d' ' -f1)"
      if [[ "${local_md5}" != "${expected_md5}" ]]; then
        printf '%s[MISMATCH]%s %s\n' "${RED}" "${NC}" "${path}"
        printf '  Expected: %s\n' "${expected_md5}"
        printf '  Local:    %s\n' "${local_md5}"
        mismatches=$((mismatches + 1))
      else
        printf '%s[OK]%s %s\n' "${GREEN}" "${NC}" "${path}"
        ok=$((ok + 1))
      fi
    fi
  done <<<"$files_json"

  printf '\n==========================================\n'
  printf '         VERIFICATION SUMMARY            \n'
  printf '==========================================\n'
  printf 'Total files checked: %s\n' "${total}"
  printf '%s OK: %s%s\n' "${GREEN}" "${ok}" "${NC}"
  printf '%s Mismatch: %s%s\n' "${RED}" "${mismatches}" "${NC}"
  printf '%s Missing: %s%s\n' "${YELLOW}" "${missing}" "${NC}"
  printf '==========================================\n\n'

  [[ ${mismatches} -eq 0 && ${missing} -eq 0 ]] && return 0 || return 1
}

update_files() {
  local files_json="$1"
  local size_before=0 size_after=0 size_downloaded=0 current_size=0

  if [[ "${verify_only}" == "true" ]]; then
    verify_files "$files_json"
    return $?
  fi

  if [[ "${dry_run}" != "true" ]]; then
    while read -r file; do
      [[ -z "${file}" ]] && continue
      path="$(jq -r '.file_path_from_game_root' <<<"$file")"
      dest="${targetdir}/${path}"
      if [[ -f "${dest}" ]]; then
        current_size="$(stat -c%s "${dest}" 2>/dev/null || printf '0')"
        size_before=$((size_before + current_size))
      fi
    done <<<"$files_json"
  fi

  declare -a download_tasks=()
  while read -r file; do
    [[ -z "${file}" ]] && continue
    path="$(jq -r '.file_path_from_game_root' <<<"$file")"
    id="$(jq -r '.id' <<<"$file")"
    expected_b64="$(jq -r '.file_hash' <<<"$file")"
    expected_md5="$(printf '%s' "${expected_b64}" | base64 --decode | od -An -tx1 | tr -d ' \n')"
    dest="${targetdir}/${path}"

    download_needed=false
    if [[ ! -f "${dest}" ]]; then
      printf '%s[MISSING]%s %s is missing.\n' "${YELLOW}" "${NC}" "${path}"
      download_needed=true
    else
      local_md5="$(md5sum "${dest}" | cut -d' ' -f1)"
      if [[ "${local_md5}" != "${expected_md5}" ]]; then
        printf '%s[MISMATCH]%s %s MD5 verification failed.\n' "${RED}" "${NC}" "${path}"
        printf '  Expected: %s\n' "${expected_md5}"
        printf '  Local:    %s\n' "${local_md5}"
        download_needed=true
      else
        printf '%s[OK]%s %s matches MD5 signature: %s%s%s\n' "${GREEN}" "${NC}" "${path}" "${GREEN}" "${expected_md5}" "${NC}"
      fi
    fi

    if [[ "${download_needed}" == "true" ]]; then
      download_tasks+=("${id}|${dest}|${path}")
    fi
  done <<<"$files_json"

  if [[ ${#download_tasks[@]} -eq 0 ]]; then
    if [[ "${quiet}" == "false" ]]; then
      printf '%sAll files are up to date.%s\n' "${GREEN}" "${NC}"
    fi
    return 0
  fi

  if [[ "${dry_run}" == "true" ]]; then
    printf '%s[DRY RUN]%s The following files would be downloaded:\n' "${YELLOW}" "${NC}"
    for task in "${download_tasks[@]}"; do
      id="${task%%|*}"
      rest="${task#*|}"
      dest="${rest%%|*}"
      path="${rest#*|}"
      printf '  - %s (ID: %s)\n' "${path}" "${id}"
    done
    printf 'Total: %s files\n' "${#download_tasks[@]}"
    return 0
  fi

  if [[ "${quiet}" == "false" ]]; then
    printf '%sStarting %s downloads with %s concurrent jobs...%s\n\n' "${BLUE}" "${#download_tasks[@]}" "${parallel}" "${NC}"
  fi

  local max_jobs="${parallel}"
  local running=0
  local failed=0
  declare -a pids=()

  for task in "${download_tasks[@]}"; do
    id="${task%%|*}"
    rest="${task#*|}"
    dest="${rest%%|*}"
    path="${rest#*|}"

    (
      download_file_by_id "$id" "$dest" "$path"
    ) &
    pids+=($!)
    running=$((running + 1))

    if [[ $running -ge $max_jobs ]]; then
      wait -n
      new_pids=()
      for p in "${pids[@]}"; do
        if kill -0 "$p" 2>/dev/null; then
          new_pids+=("$p")
        fi
      done
      pids=("${new_pids[@]}")
      running=${#pids[@]}
    fi
  done

  for p in "${pids[@]}"; do
    wait "$p" || failed=$((failed + 1))
  done

  if [[ $failed -gt 0 ]]; then
    error 1 "One or more downloads failed."
  fi

  while read -r file; do
    [[ -z "${file}" ]] && continue
    path="$(jq -r '.file_path_from_game_root' <<<"$file")"
    dest="${targetdir}/${path}"
    if [[ -f "${dest}" ]]; then
      current_size="$(stat -c%s "${dest}" 2>/dev/null || printf '0')"
      size_after=$((size_after + current_size))
    fi
  done <<<"$files_json"

  if [[ "${quiet}" == "false" ]]; then
    local size_delta=$((size_after - size_before))
    printf '\n==========================================\n'
    printf '         UPDATE OPERATION SUMMARY          \n'
    printf '==========================================\n'
    printf 'Local Storage Before:      %s\n' "$(format_bytes "${size_before}")"
    printf 'Local Storage After:       %s\n' "$(format_bytes "${size_after}")"
    if ((size_delta >= 0)); then
      printf 'Storage Growth (Delta):   +%s\n' "$(format_bytes "${size_delta}")"
    else
      abs_delta=$((size_delta * -1))
      printf 'Storage Shrinkage (Delta): -%s\n' "$(format_bytes "${abs_delta}")"
    fi
    printf '==========================================\n\n'
  fi

  return 0
}

# ============================================================
# Clear cache
# ============================================================
clearCache() {
  local deleted
  if [[ -d "${targetdir}/Cache" ]] && deleted="$(find "${targetdir}/Cache" -iname '*.wdb' -type f -print -delete)"; then
    rm -f "${targetdir}/Cache/invalid"
    [[ -n "${deleted}" ]] && debug "Update completed, cleared local caches:\n${deleted}"
  fi
}

filtered_args=()
for arg in "${@}"; do
  case "${arg}" in
  --debug)
    debug="true"
    debug "Debug messages enabled"
    ;;
  --verify)
    verify_only=true
    debug "Verification mode enabled"
    ;;
  --quiet)
    quiet=true
    ;;
  --dry-run)
    dry_run=true
    debug "Dry-run mode enabled"
    ;;
  --help)
    cat <<EOF
Usage: $0 [OPTIONS] [--] [game arguments]

Options:
  --debug           Enable verbose output
  --verify          Check files against manifest (no downloads)
  --dry-run         Show what would be done without downloading
  --quiet           Suppress non-error output
  --help            Show this help message

For Steam, use as launch option: ./updater.sh --quiet %command%
EOF
    exit 0
    ;;
  *) filtered_args+=("${arg}") ;;
  esac
done
set -- "${filtered_args[@]}"

manage_token

check_server_status

if ! jq -e --arg slug "${game}" '.data.games[] | select(.slug == $slug)' <<<"${games_manifest}" >/dev/null 2>&1; then
  error 1 "Game slug '${game}' not found in manifest. Available: $(jq -r '.data.games[].slug' <<<"${games_manifest}" | tr '\n' ' ')"
fi

realmlist="$(jq -r --arg slug "${game}" '.data.games[] | select(.slug == $slug) | .realmlist' <<<"${games_manifest}")"
if [[ -n "${realmlist}" && "${realmlist}" != "null" ]]; then
  update_realmlist "${realmlist}"
fi

files_to_process="$(collect_core_files "${games_manifest}" "${game}")"

if [[ -z "${files_to_process}" ]]; then
  error 1 "No files found in manifest for game slug '${game}'."
fi

update_files "${files_to_process}"
update_status=$?

if [[ -f "${targetdir}/Cache/invalid" ]] || [[ -d "${targetdir}/Cache" ]]; then
  clearCache
fi

if [[ "${verify_only}" == "true" ]]; then
  exit "${update_status}"
fi

if [[ ${#} -gt 0 ]]; then
  if [[ "${*,,}" == *wow.exe* ]]; then
    unset TZ
    export PROTON_FORCE_LARGE_ADDRESS_AWARE=1 WINE_LARGE_ADDRESS_AWARE=1
  fi
  exec "${@}"
fi
