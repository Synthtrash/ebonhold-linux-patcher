#!/usr/bin/env bash

: "${debug:=false}"
: "${game:=roguelike-prod}"
verify_only=false
quiet=false
dry_run=false
parallel=4
files_updated=false
realmlist_updated=false
full=false
status_only=false
addons=""
list_addons_only=false
select_addons=false
check_addons_only=false
addon_catalog=""
addon_ids=()
addon_update_ids=()
addon_update_names=()

script_path="${0}"
if [[ "${script_path}" != */* ]]; then
  resolved_script_path="$(command -v -- "${script_path}" 2>/dev/null || true)"
  if [[ -n "${resolved_script_path}" ]]; then
    script_path="${resolved_script_path}"
  elif [[ -f "${script_path}" ]]; then
    script_path="./${script_path}"
  else
      printf 'Could not determine launcher location.\n' >&2
      exit 1
  fi
fi
scriptdir="$(dirname "${script_path}")"
targetdir="$(realpath -m "${scriptdir}")"
target_root="$(realpath -m "${targetdir}")"

login_api="https://api.project-ebonhold.com/api/auth/login"
games_api="https://api.project-ebonhold.com/api/launcher/games"
status_api="https://api.project-ebonhold.com/api/server/status"
patch_download_base="https://api.project-ebonhold.com/api/launcher/download?file_ids="
addons_api="https://api.project-ebonhold.com/api/launcher/addons"
addon_download_base="https://api.project-ebonhold.com/api/launcher/addons/download?addon_ids="
token_file="${targetdir}/.updaterToken"
addon_state_file="${targetdir}/Interface/AddOns/.ebonhold-launcher-addons.json"

is_read_only_mode() {
  [[ "${verify_only}" == "true" || "${dry_run}" == "true" ]]
}

safe_destination() {
  local path="$1"
  local destination

  if [[ -z "${path}" || "${path}" == /* || "${path}" == "." || "${path}" == ".." ||
    "${path}" == */./* || "${path}" == */. || "${path}" == */.. || "${path}" == */../* || "${path}" == ../* ||
    "${path}" == *"|"* || "${path}" =~ [[:cntrl:]] ]]; then
    return 1
  fi

  destination="$(realpath -m "${target_root}/${path}")"
  [[ "${destination}" == "${target_root}/"* ]] || return 1
  printf '%s' "${destination}"
}

manifest_md5() {
  local encoded="$1"
  local checksum

  checksum="$(printf '%s' "${encoded}" | base64 --decode 2>/dev/null | od -An -tx1 | tr -d ' \n')" || return 1
  [[ "${checksum}" =~ ^[[:xdigit:]]{32}$ ]] || return 1
  printf '%s' "${checksum,,}"
}

manage_token() {
  local token=""
  local games_response=""
  local user=""
  local pass=""

  if [[ -f "${token_file}" ]]; then
    token="$(<"${token_file}")"
    if [[ -z "${token}" || "${token}" == "null" ]]; then
      debug "Token file exists but is empty or contains 'null'. Clearing it."
      is_read_only_mode || rm -f "${token_file}"
      token=""
    fi
  fi

  if [[ -n "${token}" ]]; then
    debug "Auth token found, verifying token."
    games_response="$(curl -s --connect-timeout 10 --max-time 60 \
      -H "Authorization: Bearer ${token}" \
      -H "User-Agent: EbonholdLauncher/1.0" \
      -H "Accept: application/json" \
      -H "X-Client-Id: EbonholdLauncher" \
      -H "Origin: https://project-ebonhold.com" \
      -H "Referer: https://project-ebonhold.com/download" \
      "${games_api}")"
    if [[ -z "${games_response}" || "${games_response}" == "null" ]] || jq -e '.success == false' <<<"${games_response}" >/dev/null 2>&1; then
      debug "Token invalid or expired. Clearing session."
      is_read_only_mode || rm -f "${token_file}"
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
  session="$(jq -n --arg username "${user}" --arg password "${pass}" \
      '{username: $username, password: $password, rememberMe: true}' |
    curl -s --connect-timeout 10 --max-time 60 -X POST -w "\n%{http_code}" \
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

  if [[ ! "${http_code}" =~ ^2[0-9][0-9]$ ]] || ! jq -e '.success == true' <<<"${session}" >/dev/null 2>&1; then
    message="$(jq -r '.message // empty' <<<"${session}")"
    [[ -z "${message}" ]] && message="HTTP Gateway Reject Code: ${http_code}"
    error 1 "Session authorization failed.\n${message}"
  fi

  token="$(jq -r '.token' <<<"${session}")"
  if [[ -z "${token}" || "${token}" == "null" ]]; then
    error 1 "Login succeeded but no valid token was returned."
  fi

  if ! is_read_only_mode; then
    printf '%s' "${token}" >"${token_file}"
    chmod 600 "${token_file}"
    debug "Secure auth token serialized locally (permissions: 600)."
  fi

  games_response="$(curl -s --connect-timeout 10 --max-time 60 \
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

BLUE=$'\033[0;34m' RED=$'\033[0;31m' YELLOW=$'\033[0;33m' GREEN=$'\033[0;32m' NC=$'\033[0m'

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
  local expected_md5="$4"
  local retry=0
  local max_retries=1
  local tmp_out=""
  local tmp_file=""
  local downloaded_md5=""

  debug "Downloading file ID ${file_id} -> ${dest_path}"

  while [[ $retry -le $max_retries ]]; do
    tmp_out="$(mktemp)" || return 1
    status="$(curl -s --connect-timeout 10 --max-time 60 -w "%{http_code}" -o "${tmp_out}" \
      -H "Authorization: Bearer ${authToken}" \
      -H "User-Agent: EbonholdLauncher/1.0" \
      -H "Accept: application/json" \
      -H "X-Client-Id: EbonholdLauncher" \
      -H "Origin: https://project-ebonhold.com" \
      -H "Referer: https://project-ebonhold.com/download" \
      "${patch_download_base}${file_id}" 2>/dev/null)"
    response="$(<"${tmp_out}")"
    rm -f "${tmp_out}"

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

    if [[ "${url}" != https://* ]]; then
      printf '%s[ERROR]%s Refusing non-HTTPS download URL for %s\n' "${RED}" "${NC}" "${description}"
      return 1
    fi

    debug "Download URL: ${url}"
    mkdir -p "$(dirname "${dest_path}")" || return 1
    tmp_file="$(mktemp "${dest_path}.tmp.XXXXXX")" || return 1

    if [[ "${quiet}" == "false" ]]; then
      printf '%s[DOWNLOADING]%s %s...\n' "${BLUE}" "${NC}" "${description}"
    fi
    if ! curl --fail --location --show-error --connect-timeout 10 --max-time 600 --retry 2 --retry-all-errors "${url}" -o "${tmp_file}"; then
      rm -f "${tmp_file}"
      printf '%s[ERROR]%s Failed to download %s\n' "${RED}" "${NC}" "${description}"
      return 1
    fi

    downloaded_md5="$(md5sum "${tmp_file}" | cut -d' ' -f1)"
    if [[ "${downloaded_md5}" != "${expected_md5}" ]]; then
      rm -f "${tmp_file}"
      printf '%s[ERROR]%s Downloaded checksum mismatch for %s\n' "${RED}" "${NC}" "${description}"
      return 1
    fi
    mv -f "${tmp_file}" "${dest_path}" || return 1

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
  local response="$(curl -s --connect-timeout 10 --max-time 30 \
    -H "Authorization: Bearer ${authToken}" \
    -H "User-Agent: EbonholdLauncher/1.0" \
    -H "Accept: application/json" \
    -H "X-Client-Id: EbonholdLauncher" \
    "${status_api}")"
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
      realmlist_updated=true
    else
      debug "realmlist.wtf already correct."
    fi
  else
    printf '%s[REALMLIST]%s Creating %s with %s\n' "${YELLOW}" "${NC}" "${dest}" "${realmlist}"
    printf 'set realmlist %s\n' "${realmlist}" >"${dest}"
    realmlist_updated=true
  fi
}

collect_core_files() {
  local manifest="$1"
  local game_slug="$2"
  local full_mode="$3"
  local files_json

  if [[ "${full_mode}" == "true" ]]; then
    files_json="$(jq -c '.data.common.files[] | select(.file_path_from_game_root != "Data/enUS/realmlist.wtf")' <<<"$manifest")"
    files_json+=$'\n'"$(jq -c --arg slug "$game_slug" '.data.games[] | select(.slug == $slug) | .files[] | select(.file_path_from_game_root != "Data/enUS/realmlist.wtf")' <<<"$manifest")"
  else
    files_json="$(jq -c --arg slug "$game_slug" '.data.games[] | select(.slug == $slug) | .files[] | select(.option_slug == null and (.file_path_from_game_root | test("(^|/)patch"; "i")))' <<<"$manifest")"
  fi

  printf '%s\n' "$files_json" | grep -v '^$'
}

fetch_addon_catalog() {
  addon_catalog="$(curl -s --connect-timeout 10 --max-time 60 \
    -H "Authorization: Bearer ${authToken}" \
    -H "User-Agent: EbonholdLauncher/1.0" \
    -H "Accept: application/json" \
    -H "X-Client-Id: EbonholdLauncher" \
    -H "Origin: https://project-ebonhold.com" \
    -H "Referer: https://project-ebonhold.com/download" \
    "${addons_api}")"

  [[ -n "${addon_catalog}" ]] && jq -e '.success == true and (.addons | type == "array")' <<<"${addon_catalog}" >/dev/null 2>&1 || error 1 "Could not retrieve the addon catalog."
}

print_addon_catalog() {
  jq -r '.addons[] | "  [\(.id)] \(.name)\n      \(.description)\n      \(.file_size_bytes) bytes, updated \(.updated_at)"' <<<"${addon_catalog}"
}

resolve_addons() {
  local requested="$1"
  local addon
  local addon_id

  addon_ids=()
  IFS=',' read -r -a requested_addons <<<"${requested}"
  for addon in "${requested_addons[@]}"; do
    addon="${addon#"${addon%%[![:space:]]*}"}"
    addon="${addon%"${addon##*[![:space:]]}"}"
    [[ -n "${addon}" ]] || error 1 "No addon names or IDs were provided."
    addon_id="$(jq -r --arg addon "${addon}" '
      [.addons[] | select((.id | tostring) == $addon or (.name | ascii_downcase) == ($addon | ascii_downcase)) | .id]
      | if length == 1 then .[0] else empty end
    ' <<<"${addon_catalog}")"
    [[ "${addon_id}" =~ ^[1-9][0-9]*$ ]] || error 1 "Unknown addon '${addon}'. Run --list-addons to see available addons."
    [[ " ${addon_ids[*]} " == *" ${addon_id} "* ]] || addon_ids+=("${addon_id}")
  done

  addons="${requested}"
}

list_addons() {
  fetch_addon_catalog
  if [[ "$(jq '.addons | length' <<<"${addon_catalog}")" -eq 0 ]]; then
    printf 'No addons are currently available.\n'
    return 0
  fi

  printf 'Available addons:\n'
  print_addon_catalog
}

record_addon_install() {
  local addon_id="$1"
  local addon_name="$2"
  local updated_at="$3"
  local folders_json="$4"
  local state='{"addons":{}}'
  local state_dir
  local temporary_state

  state_dir="$(dirname "${addon_state_file}")"
  mkdir -p "${state_dir}" || error 1 "Could not create ${state_dir}."
  if [[ -f "${addon_state_file}" ]] && jq -e '.addons | type == "object"' "${addon_state_file}" >/dev/null 2>&1; then
    state="$(<"${addon_state_file}")"
  fi
  temporary_state="$(mktemp "${state_dir}/.ebonhold-launcher-addons.XXXXXX")" || error 1 "Could not create addon state file."
  if ! jq --arg id "${addon_id}" --arg name "${addon_name}" --arg updated_at "${updated_at}" --argjson folders "${folders_json}" \
    '.addons[$id] = {name: $name, updated_at: $updated_at, folders: $folders}' <<<"${state}" >"${temporary_state}"; then
    rm -f "${temporary_state}"
    error 1 "Could not save addon update state."
  fi
  mv -f "${temporary_state}" "${addon_state_file}" || error 1 "Could not save addon update state."
}

addon_directory() {
  local addon_name="$1"
  local directory

  shopt -s nocasematch
  for directory in "${targetdir}/Interface/AddOns/"*; do
    [[ -d "${directory}" && "${directory##*/}" == "${addon_name}" ]] && {
      printf '%s' "${directory}"
      shopt -u nocasematch
      return 0
    }
  done
  shopt -u nocasematch
  return 1
}

addon_directories() {
  local addon_id="$1"
  local addon_name="$2"
  local folder
  local directory
  local found=false

  while read -r folder; do
    [[ -z "${folder}" || "${folder}" == *"/"* || "${folder}" == "." || "${folder}" == ".." || "${folder}" =~ [[:cntrl:]] ]] && continue
    directory="${targetdir}/Interface/AddOns/${folder}"
    if [[ -d "${directory}" ]] && find "${directory}" -type f -print -quit | grep -q .; then
      printf '%s\n' "${directory}"
      found=true
    fi
  done < <(jq -r --arg id "${addon_id}" '.addons[$id].folders[]? // empty' "${addon_state_file}" 2>/dev/null)

  if [[ "${found}" == "false" ]] && directory="$(addon_directory "${addon_name}")" && find "${directory}" -type f -print -quit | grep -q .; then
    printf '%s\n' "${directory}"
  fi
}

addon_folder_is_shared() {
  local addon_id="$1"
  local folder="$2"

  jq -e --arg id "${addon_id}" --arg folder "${folder}" \
    'any(.addons | to_entries[] | select(.key != $id) | .value.folders[]?; . == $folder)' "${addon_state_file}" >/dev/null 2>&1
}

addon_state_is_complete() {
  local addon_id="$1"
  local folder
  local directory
  local found=false

  while read -r folder; do
    [[ -z "${folder}" || "${folder}" == *"/"* || "${folder}" == "." || "${folder}" == ".." || "${folder}" =~ [[:cntrl:]] ]] && return 1
    found=true
    directory="${targetdir}/Interface/AddOns/${folder}"
    [[ -d "${directory}" ]] && find "${directory}" -type f -print -quit | grep -q . || return 1
  done < <(jq -r --arg id "${addon_id}" '.addons[$id].folders[]? // empty' "${addon_state_file}" 2>/dev/null)

  [[ "${found}" == "true" ]]
}

check_addon_updates() {
  local addon_id addon_name updated_at directories local_mtime remote_mtime local_display state_updated_at

  fetch_addon_catalog
  addon_update_ids=()
  addon_update_names=()
  while IFS=$'\t' read -r addon_id addon_name updated_at; do
    state_updated_at="$(jq -r --arg id "${addon_id}" '.addons[$id].updated_at // empty' "${addon_state_file}" 2>/dev/null)"
    if [[ -n "${state_updated_at}" ]] && ! addon_state_is_complete "${addon_id}"; then
      printf '%s[UPDATE AVAILABLE]%s %s (repair required)\n' "${GREEN}" "${NC}" "${addon_name}"
      addon_update_ids+=("${addon_id}")
      addon_update_names+=("${addon_name}")
      continue
    fi

    directories="$(addon_directories "${addon_id}" "${addon_name}")"
    if [[ -z "${directories}" ]]; then
      printf '%s[NOT INSTALLED]%s %s\n' "${YELLOW}" "${NC}" "${addon_name}"
      continue
    fi

    remote_mtime="$(date -d "${updated_at}" +%s 2>/dev/null)" || {
      printf '%s[UNKNOWN]%s %s has an invalid catalog timestamp.\n' "${YELLOW}" "${NC}" "${addon_name}"
      continue
    }
    if [[ "${state_updated_at}" == "${updated_at}" ]]; then
      printf '%s[CURRENT]%s %s\n' "${GREEN}" "${NC}" "${addon_name}"
      continue
    fi

    local_mtime="$(while read -r directory; do find "${directory}" -type f -printf '%T@\n'; done <<<"${directories}" | sort -nr | { IFS= read -r first; printf '%s' "${first}"; })"
    if [[ -n "${state_updated_at}" ]]; then
      local_mtime="$(date -d "${state_updated_at}" +%s 2>/dev/null)"
    fi
    if [[ -z "${local_mtime}" ]]; then
      printf '%s[UNKNOWN]%s %s has no files to compare.\n' "${YELLOW}" "${NC}" "${addon_name}"
    elif ((remote_mtime > ${local_mtime%.*})); then
      local_display="$(date -d "@${local_mtime%.*}" '+%Y-%m-%d %H:%M:%S UTC')"
      printf '%s[UPDATE AVAILABLE]%s %s\n' "${GREEN}" "${NC}" "${addon_name}"
      printf '  Remote: %s\n  Local:  %s\n' "${updated_at}" "${local_display}"
      addon_update_ids+=("${addon_id}")
      addon_update_names+=("${addon_name}")
    else
      printf '%s[CURRENT]%s %s\n' "${GREEN}" "${NC}" "${addon_name}"
    fi
  done < <(jq -r '.addons[] | [.id, .name, .updated_at] | @tsv' <<<"${addon_catalog}")
}

prompt_addon_updates() {
  local answer

  [[ ${#addon_update_ids[@]} -gt 0 ]] || return 0
  printf '\nUpdates are available for: %s\n' "$(IFS=', '; printf '%s' "${addon_update_names[*]}")"
  read -r -p 'Install updates now? [y/N] ' answer
  case "${answer}" in
  [Yy] | [Yy][Ee][Ss])
    addon_ids=("${addon_update_ids[@]}")
    download_addons
    ;;
  *)
    printf 'Skipped addon updates.\n'
    ;;
  esac
}

select_addons_interactively() {
  local selection
  local -a zenity_args

  fetch_addon_catalog
  [[ "$(jq '.addons | length' <<<"${addon_catalog}")" -gt 0 ]] || error 1 "No addons are currently available."

  if [[ "${GUI}" == "true" ]]; then
    zenity_args=(--list --checklist --title="Ebonhold Addons" --text="Select addons to install" --column="Install" --column="Name" --column="Description" --separator=",")
    while IFS=$'\t' read -r addon_id addon_name addon_description; do
      zenity_args+=(FALSE "${addon_id}" "${addon_name}: ${addon_description}")
    done < <(jq -r '.addons[] | [.id, .name, .description] | @tsv' <<<"${addon_catalog}")
    selection="$(zenity "${zenity_args[@]}")" || exit 0
  else
    printf 'Available addons:\n'
    print_addon_catalog
    selection="$(prompt_text "Ebonhold Addons" "Enter comma-separated addon names or IDs to install")" || exit 0
  fi

  resolve_addons "${selection}"
}

install_addon_archive() {
  local addon_id="$1"
  local archive="$2"
  local addon_name="$3"
  local addons_dir="${targetdir}/Interface/AddOns"
  local staging
  local path
  local top_level
  local updated_at
  local folders_json
  local previous_folder
  local zip_metadata
  local -a top_levels=()

  command -v unzip >/dev/null 2>&1 || error 1 "Installing addons requires unzip."
  command -v zipinfo >/dev/null 2>&1 || error 1 "Installing addons requires zipinfo."
  mkdir -p "${addons_dir}" || error 1 "Could not create ${addons_dir}."
  [[ ! -L "${addons_dir}" ]] || error 1 "Refusing symlinked addon directory: ${addons_dir}."
  zip_metadata="$(zipinfo -l "${archive}")" || error 1 "Could not inspect addon archive for ${addon_name}."
  while read -r path; do
    [[ "${path}" =~ ^l[-rwxSsTt]{9}[[:space:]] ]] && error 1 "Addon archive for ${addon_name} contains a symbolic link."
  done <<<"${zip_metadata}"

  while read -r path; do
    [[ -z "${path}" ]] && continue
    if [[ "${path}" == /* || "${path}" == *\\* || "${path}" == "." || "${path}" == ".." ||
      "${path}" == ./* || "${path}" == ../* || "${path}" == */./* || "${path}" == */. ||
      "${path}" == */../* || "${path}" == */.. || "${path}" =~ [[:cntrl:]] ]]; then
      error 1 "Addon archive for ${addon_name} contains an unsafe path."
    fi
    top_level="${path%%/*}"
    [[ -n "${top_level}" ]] || error 1 "Addon archive for ${addon_name} has an invalid layout."
    [[ " ${top_levels[*]} " == *" ${top_level} "* ]] || top_levels+=("${top_level}")
  done < <(unzip -Z1 "${archive}")

  [[ ${#top_levels[@]} -gt 0 ]] || error 1 "Addon archive for ${addon_name} is empty."
  for top_level in "${top_levels[@]}"; do
    [[ ! -L "${addons_dir}/${top_level}" ]] || error 1 "Refusing symlinked addon folder: ${top_level}."
    addon_folder_is_shared "${addon_id}" "${top_level}" && error 1 "Cannot safely replace shared addon folder: ${top_level}."
  done
  staging="$(mktemp -d "${addons_dir}/.updater-addon.XXXXXX")" || error 1 "Could not create addon staging directory."
  if ! unzip -qq "${archive}" -d "${staging}"; then
    rm -rf "${staging}"
    error 1 "Could not extract addon ${addon_name}."
  fi

  for top_level in "${top_levels[@]}"; do
    rm -rf "${addons_dir:?}/${top_level}"
    mv "${staging}/${top_level}" "${addons_dir}/${top_level}" || {
      rm -rf "${staging}"
      error 1 "Could not install addon ${addon_name}."
    }
  done
  rm -rf "${staging}"
  while read -r previous_folder; do
    [[ -z "${previous_folder}" || "${previous_folder}" == *"/"* || "${previous_folder}" == "." || "${previous_folder}" == ".." || "${previous_folder}" =~ [[:cntrl:]] ]] && continue
    [[ " ${top_levels[*]} " == *" ${previous_folder} "* ]] && continue
    addon_folder_is_shared "${addon_id}" "${previous_folder}" && continue
    rm -rf "${addons_dir:?}/${previous_folder}"
  done < <(jq -r --arg id "${addon_id}" '.addons[$id].folders[]? // empty' "${addon_state_file}" 2>/dev/null)
  updated_at="$(jq -r --argjson id "${addon_id}" '.addons[] | select(.id == $id) | .updated_at' <<<"${addon_catalog}")"
  [[ -n "${updated_at}" && "${updated_at}" != "null" ]] || error 1 "Could not determine installed addon version."
  folders_json="$(printf '%s\n' "${top_levels[@]}" | jq -Rsc 'split("\n") | map(select(length > 0))')"
  record_addon_install "${addon_id}" "${addon_name}" "${updated_at}" "${folders_json}"
}

download_addons() {
  local ids
  local response
  local file_id filename url archive addon_name
  local addon_lock_fd
  local addons_dir="${targetdir}/Interface/AddOns"
  local -a response_ids=()

  [[ ${#addon_ids[@]} -gt 0 ]] || return 0
  command -v flock >/dev/null 2>&1 || error 1 "Installing addons requires flock."
  mkdir -p "${addons_dir}" || error 1 "Could not create ${addons_dir}."
  [[ ! -L "${addons_dir}" ]] || error 1 "Refusing symlinked addon directory: ${addons_dir}."
  exec {addon_lock_fd}>"${addons_dir}/.ebonhold-launcher.lock"
  flock "${addon_lock_fd}" || error 1 "Could not lock addon installation."
  ids="$(IFS=,; printf '%s' "${addon_ids[*]}")"
  response="$(curl -s --connect-timeout 10 --max-time 60 \
    -H "Authorization: Bearer ${authToken}" \
    -H "User-Agent: EbonholdLauncher/1.0" \
    -H "Accept: application/json" \
    -H "X-Client-Id: EbonholdLauncher" \
    -H "Origin: https://project-ebonhold.com" \
    -H "Referer: https://project-ebonhold.com/download" \
    "${addon_download_base}${ids}")"
  [[ -n "${response}" ]] && jq -e '.success == true and (.files | type == "array")' <<<"${response}" >/dev/null 2>&1 || error 1 "Could not retrieve addon download URLs."

  while read -r file_id; do
    [[ "${file_id}" =~ ^[1-9][0-9]*$ ]] || error 1 "Received an invalid addon download response."
    [[ " ${addon_ids[*]} " == *" ${file_id} "* ]] || error 1 "Received an unrequested addon download."
    [[ " ${response_ids[*]} " != *" ${file_id} "* ]] || error 1 "Received a duplicate addon download."
    jq -e --argjson id "${file_id}" '.addons[] | select(.id == $id)' <<<"${addon_catalog}" >/dev/null || error 1 "Received an unknown addon download."
    response_ids+=("${file_id}")
  done < <(jq -r '.files[] | .file_id' <<<"${response}")
  for file_id in "${addon_ids[@]}"; do
    [[ " ${response_ids[*]} " == *" ${file_id} "* ]] || error 1 "No download was returned for requested addon ID ${file_id}."
  done

  while IFS=$'\t' read -r file_id filename url; do
    [[ "${file_id}" =~ ^[1-9][0-9]*$ && "${filename}" == Interface/AddOns/*.zip && "${url}" == https://* ]] || error 1 "Received an invalid addon download response."
    addon_name="$(jq -r --argjson id "${file_id}" '.addons[] | select(.id == $id) | .name' <<<"${addon_catalog}")"
    archive="$(mktemp)" || error 1 "Could not create addon download file."
    if ! curl --fail --location --show-error --connect-timeout 10 --max-time 600 --retry 2 --retry-all-errors "${url}" -o "${archive}"; then
      rm -f "${archive}"
      error 1 "Failed to download addon ${addon_name}."
    fi
    install_addon_archive "${file_id}" "${archive}" "${addon_name}"
    rm -f "${archive}"
    [[ "${quiet}" == "true" ]] || printf '%s[ADDON]%s Installed %s\n' "${GREEN}" "${NC}" "${addon_name}"
  done < <(jq -r '.files[] | [.file_id, .filename, .url] | @tsv' <<<"${response}")
  flock -u "${addon_lock_fd}"
  exec {addon_lock_fd}>&-
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
    if ! dest="$(safe_destination "${path}")" || ! expected_md5="$(manifest_md5 "${expected_b64}")"; then
      printf '%s[INVALID]%s Manifest entry for %s is unsafe or has an invalid checksum.\n' "${RED}" "${NC}" "${path}"
      mismatches=$((mismatches + 1))
      continue
    fi
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
      dest="$(safe_destination "${path}")" || error 1 "Unsafe manifest path: ${path}"
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
    dest="$(safe_destination "${path}")" || error 1 "Unsafe manifest path: ${path}"
    expected_md5="$(manifest_md5 "${expected_b64}")" || error 1 "Invalid manifest checksum for: ${path}"
    [[ -n "${id}" && "${id}" != "null" && "${id}" != *"|"* ]] || error 1 "Invalid manifest file ID for: ${path}"

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
      download_tasks+=("${id}|${dest}|${path}|${expected_md5}")
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
      rest="${rest#*|}"
      path="${rest%%|*}"
      printf '  - %s (ID: %s)\n' "${path}" "${id}"
    done
    printf 'Total: %s files\n' "${#download_tasks[@]}"
    return 0
  fi

  if [[ "${quiet}" == "false" ]]; then
    printf '%sStarting %s downloads with %s concurrent jobs...%s\n\n' "${BLUE}" "${#download_tasks[@]}" "${parallel}" "${NC}"
  fi

  local failed=0
  declare -a pids=()

  for task in "${download_tasks[@]}"; do
    id="${task%%|*}"
    rest="${task#*|}"
    dest="${rest%%|*}"
    rest="${rest#*|}"
    path="${rest%%|*}"
    expected_md5="${rest#*|}"

    (
      download_file_by_id "$id" "$dest" "$path" "$expected_md5"
    ) &
    pids+=("$!")

    if [[ ${#pids[@]} -ge ${parallel} ]]; then
      for p in "${pids[@]}"; do
        wait "$p" || failed=$((failed + 1))
      done
      pids=()
    fi
  done

  for p in "${pids[@]}"; do
    wait "$p" || failed=$((failed + 1))
  done

  if [[ $failed -gt 0 ]]; then
    error 1 "One or more downloads failed."
  fi
  files_updated=true

  while read -r file; do
    [[ -z "${file}" ]] && continue
    path="$(jq -r '.file_path_from_game_root' <<<"$file")"
    dest="$(safe_destination "${path}")" || error 1 "Unsafe manifest path: ${path}"
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
  local cache_dir="${targetdir}/Cache"
  local deleted

  mkdir -p "${cache_dir}" || return 1
  deleted="$(find "${cache_dir}" -iname '*.wdb' -type f -print -delete)" || return 1
  touch "${cache_dir}/invalid" || return 1
  [[ -n "${deleted}" ]] && debug "Update completed, cleared local caches:\n${deleted}"
}

filtered_args=()
passthrough_args=false
for arg in "${@}"; do
  if [[ "${passthrough_args}" == "true" ]]; then
    filtered_args+=("${arg}")
    continue
  fi

  case "${arg}" in
  --)
    passthrough_args=true
    ;;
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
  --full)
    full=true
    ;;
  --status)
    status_only=true
    ;;
  --addons=*)
    addons="${arg#--addons=}"
    ;;
  --list-addons)
    list_addons_only=true
    ;;
  --check-addons)
    check_addons_only=true
    ;;
  --select-addons)
    select_addons=true
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
  --full            Update all common and game files, including optional files
  --status          Show realm status and exit
  --list-addons     Show addons available for the selected game and exit
  --check-addons    Show installed addon update recommendations and exit
  --select-addons   Interactively select addons to download
  --addons=LIST     Download comma-separated addon names or IDs
  --quiet           Suppress non-error output
  --help            Show this help message

Default mode updates only game patch files. For Steam, use: ./launcher.sh --quiet %command%
EOF
    exit 0
    ;;
  *) filtered_args+=("${arg}") ;;
  esac
done
set -- "${filtered_args[@]}"

if [[ ! "${parallel}" =~ ^[1-9][0-9]*$ ]]; then
  error 1 "parallel must be a positive integer."
fi

manage_token

check_server_status

if [[ "${status_only}" == "true" ]]; then
  exit 0
fi

if ! jq -e --arg slug "${game}" '.data.games[] | select(.slug == $slug)' <<<"${games_manifest}" >/dev/null 2>&1; then
  error 1 "Game slug '${game}' not found in manifest. Available: $(jq -r '.data.games[].slug' <<<"${games_manifest}" | tr '\n' ' ')"
fi

if [[ "${list_addons_only}" == "true" ]]; then
  list_addons
  exit 0
fi

if [[ "${check_addons_only}" == "true" ]]; then
  check_addon_updates
  exit 0
fi

if [[ "${select_addons}" == "true" ]]; then
  select_addons_interactively
elif [[ -n "${addons}" ]]; then
  fetch_addon_catalog
  resolve_addons "${addons}"
fi

realmlist="$(jq -r --arg slug "${game}" '.data.games[] | select(.slug == $slug) | .realmlist' <<<"${games_manifest}")"
if ! is_read_only_mode && [[ -n "${realmlist}" && "${realmlist}" != "null" ]]; then
  update_realmlist "${realmlist}"
fi

files_to_process="$(collect_core_files "${games_manifest}" "${game}" "${full}")"

if [[ -z "${files_to_process}" ]]; then
  error 1 "No files found in manifest for game slug '${game}'."
fi

if ! update_files "${files_to_process}"; then
  exit 1
fi

if [[ ${#addon_ids[@]} -gt 0 ]]; then
  if [[ "${dry_run}" == "true" ]]; then
    printf '%s[DRY RUN]%s Would download selected launcher addons: %s\n' "${YELLOW}" "${NC}" "${addons}"
  elif [[ "${verify_only}" == "false" ]]; then
    download_addons
  fi
fi

if [[ "${files_updated}" == "true" || "${realmlist_updated}" == "true" ]]; then
  clearCache || error 1 "Updated files, but failed to clear and invalidate the cache."
fi

if [[ ${#} -eq 0 && "${interactiveShell}" == "true" && "${quiet}" == "false" && "${dry_run}" == "false" && "${verify_only}" == "false" && -z "${addons}" ]]; then
  check_addon_updates
  prompt_addon_updates
fi

if [[ "${verify_only}" == "true" ]]; then
  exit 0
fi

if [[ ${#} -gt 0 ]]; then
  if [[ "${*,,}" == *wow.exe* ]]; then
    unset TZ
    export PROTON_FORCE_LARGE_ADDRESS_AWARE=1 WINE_LARGE_ADDRESS_AWARE=1
  fi
  exec "${@}"
fi
