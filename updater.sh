#!/usr/bin/env bash

: "${debug:=false}"
: "${game:=roguelike-prod}"

scriptdir="$(dirname "$(readlink -f "${0}")")"
targetdir="${PWD}"

login_api="https://api.project-ebonhold.com/api/auth/login"
manifest_api="https://api.project-ebonhold.com/api/launcher/file-hashes?server_name=roguelike-prod"
patch_download_base="https://api.project-ebonhold.com/api/launcher/download?file_ids="
token_file="${targetdir}/.updaterToken"

declare -A PATCH_IDS=(
  ["patch-4"]="191"
  ["patch-5"]="192"
  ["patch-6"]="193"
)

declare -A EXTRA_FILES=(
  ["AwesomeWotlkLib.dll"]="1"
  ["Wow.exe"]="47"
  ["Data/patch-X.MPQ"]="181"
  ["Data/patch-I.MPQ"]="190"
  ["skia.dll"]="204"
  ["ebonhold.dll"]="205"
)

manage_token() {
  local token=""
  local manifest_response=""
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
    manifest_response="$(curl -s -H "Authorization: Bearer ${token}" -H "User-Agent: EbonholdLauncher/1.0" "${manifest_api}")"
    if [[ -z "${manifest_response}" || "${manifest_response}" == "null" ]] || jq -e '.success == false' <<<"${manifest_response}" >/dev/null 2>&1; then
      debug "Token invalid or expired. Clearing session."
      rm -f "${token_file}"
      token=""
      manifest_response=""
    else
      debug "Token verified successfully."
      authToken="${token}"
      manifest="${manifest_response}"
      return 0
    fi
  fi

  debug "No valid token found. Please log in."

  user="$(prompt_text "Ebonhold Login" "Enter your username:")" || exit 1
  pass="$(prompt_password "Ebonhold Login" "Password for ${user}")" || exit 1

  debug "Posting credentials to authentication portal..."
  session="$(curl -s -X POST -w "\n%{http_code}" \
    -H "Content-Type: application/json" \
    -H "User-Agent: EbonholdLauncher/1.0" \
    -d "{\"username\":\"${user}\",\"password\":\"${pass}\",\"rememberMe\":true}" \
    "${login_api}")"

  http_code="$(tail -n1 <<<"${session}")"
  session="$(head -n-1 <<<"${session}")"

  debug "HTTP return code ${http_code}"
  if [[ "${debug}" == "true" ]]; then
    debug "Login response body:"
    echo "${session}" | jq . 2>/dev/null || echo "${session}" >&2
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

  echo -n "${token}" >"${token_file}"
  chmod 600 "${token_file}"
  debug "Secure auth token serialized locally (permissions: 600)."

  manifest_response="$(curl -s -H "Authorization: Bearer ${token}" -H "User-Agent: EbonholdLauncher/1.0" "${manifest_api}")"
  if [[ -z "${manifest_response}" || "${manifest_response}" == "null" ]] || jq -e '.success == false' <<<"${manifest_response}" >/dev/null 2>&1; then
    error 1 "Failed to fetch manifest with new token. Please try again."
  fi

  unset user pass
  authToken="${token}"
  manifest="${manifest_response}"
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
[[ "${GUI}" == "false" ]] && [[ "${interactiveShell}" == "false" ]] && exit 1

BLUE="\033[0;34m" RED="\033[0;31m" YELLOW="\033[0;33m" GREEN="\033[0;32m" NC="\033[0m"

debug() {
  local msg="${*}"
  if [[ "${debug}" == "true" ]]; then
    echo -e "${BLUE}[DEBUG]:${NC} ${YELLOW}${msg}${NC}" >&2
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
    echo -e "\n\033[2K${RED}[ERROR]:${NC} ${YELLOW}${msg}${NC}" >&2
  fi
  [[ "${exit_code}" -ge "1" ]] && exit "${exit_code}"
}

prompt_text() {
  local title="${1}" text="${2}"
  if [[ "${GUI}" == "true" ]]; then
    zenity --entry --title="${title}" --text="${text}" --width=400 2>/dev/null || return 1
  else
    local input
    echo "${title}" >&2
    read -r -p "${text} > " input
    [[ -z "${input}" ]] && return 1
    echo -n "${input}"
  fi
}

prompt_password() {
  local title="${1}" text="${2}"
  if [[ "${GUI}" == "true" ]]; then
    zenity --password --title="${title}" --text="${text}" --width=400 2>/dev/null
  else
    local input
    echo "${title}" >&2
    read -r -s -p "Password: >" input
    [[ -z "${input}" ]] && return 1
    echo -n "${input}"
  fi
}

format_bytes() {
  local bytes="${1}"
  if ((bytes < 1024)); then
    echo "${bytes} B"
  elif ((bytes < 1048576)); then
    printf "%.2f KB\n" "$(echo "scale=2; ${bytes}/1024" | bc)"
  else
    printf "%.2f MB\n" "$(echo "scale=2; ${bytes}/1048576" | bc)"
  fi
}

download_file_by_id() {
  local file_id="$1"
  local dest_path="$2"
  local description="${3:-$dest_path}"
  local retry=0
  local max_retries=1

  debug "Downloading file ID ${file_id} -> ${dest_path}"

  while [[ $retry -le $max_retries ]]; do
    tmp_out="$(mktemp)"
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

    if [[ "${status}" != "200" ]]; then
      if [[ "${status}" == "401" && $retry -lt $max_retries ]]; then
        debug "Token rejected (401). Re‑authenticating..."
        rm -f "${token_file}"
        manage_token # updates global authToken
        ((retry++))
        debug "Retry ${retry}/${max_retries} with new token."
        continue
      fi
      echo -e "${RED}[ERROR]${NC} Failed to get download URL for ID ${file_id} (HTTP ${status})"
      echo "Server response:"
      echo "${response}" | jq . 2>/dev/null || echo "${response}"
      return 1
    fi

    url="$(jq --raw-output '.files[0].url' <<<"${response}" 2>/dev/null)"
    if [[ -z "${url}" || "${url}" == "null" ]]; then
      url="$(jq --raw-output '.url' <<<"${response}" 2>/dev/null)"
    fi
    if [[ -z "${url}" || "${url}" == "null" ]]; then
      echo -e "${RED}[ERROR]${NC} No download URL found for ID ${file_id}"
      return 1
    fi

    debug "Download URL: ${url}"
    mkdir -p "$(dirname "${dest_path}")"

    echo -e "${BLUE}[DOWNLOADING]${NC} ${description}..."
    if ! curl -fL# "${url}" -o "${dest_path}"; then
      echo -e "${RED}[ERROR]${NC} Failed to download ${description}"
      return 1
    fi

    local size="$(stat -c%s "${dest_path}" 2>/dev/null || echo "0")"
    echo -e "${GREEN}[FINISHED]${NC} ${description} ($(format_bytes ${size}))\n"
    return 0
  done

  echo -e "${RED}[ERROR]${NC} Still getting 401 after re‑authentication. Please try again."
  return 1
}

download_extra_files() {
  local any_failed=0

  for file_path in "${!EXTRA_FILES[@]}"; do
    local id="${EXTRA_FILES[$file_path]}"
    local dest="${targetdir}/${file_path}"
    if [[ -f "${dest}" ]]; then
      debug "Extra file ${file_path} already exists, skipping."
      continue
    fi
    if ! download_file_by_id "$id" "$dest" "$file_path"; then
      any_failed=1
    fi
  done

  return $any_failed
}

update_manifest_patches() {
  local patches_json="$1"

  local patch_name b64_hash expected_md5 local_md5 file_path
  local numeric_id
  local size_before=0 size_after=0 size_downloaded=0 current_size=0

  [[ -z "${patches_json}" || "${patches_json}" == "null" ]] && return

  while read -r patch_name; do
    [[ -z "${patch_name}" ]] && continue
    file_path="Data/${patch_name}.MPQ"
    if [[ -f "${targetdir}/${file_path}" ]]; then
      current_size="$(stat -c%s "${targetdir}/${file_path}")"
      size_before=$((size_before + current_size))
    fi
  done <<<"$(jq -r 'keys[]' <<<"${patches_json}")"

  while read -r patch_name; do
    [[ -z "${patch_name}" ]] && continue
    file_path="Data/${patch_name}.MPQ"

    b64_hash="$(jq -r --arg key "${patch_name}" '.[$key]' <<<"${patches_json}")"
    expected_md5="$(echo -n "${b64_hash}" | base64 --decode | od -An -tx1 | tr -d ' \n')"

    download="false"
    if [[ ! -f "${targetdir}/${file_path}" ]]; then
      echo -e "${YELLOW}[MISSING]${NC} ${patch_name} -> ${file_path} is missing."
      download="true"
    else
      local_md5="$(md5sum "${targetdir}/${file_path}" | cut -d' ' -f1)"
      if [[ "${local_md5}" != "${expected_md5}" ]]; then
        echo -e "${RED}[MISMATCH]${NC} ${patch_name} MD5 verification failed."
        echo -e "  Expected: ${expected_md5}"
        echo -e "  Local:    ${local_md5}"
        download="true"
      else
        echo -e "${GREEN}[OK]${NC} ${patch_name} matches MD5 signature: ${GREEN}${expected_md5}${NC}"
      fi
    fi

    if [[ "${download}" == "true" ]]; then
      numeric_id="${PATCH_IDS[$patch_name]}"
      if [[ -z "${numeric_id}" ]]; then
        error 1 "No numeric ID known for ${patch_name}. Please add to PATCH_IDS."
      fi
      if download_file_by_id "$numeric_id" "${targetdir}/${file_path}" "$patch_name"; then
        current_size="$(stat -c%s "${targetdir}/${file_path}")"
        size_downloaded=$((size_downloaded + current_size))
        [[ -d "${targetdir}/Cache" ]] && touch "${targetdir}/Cache/invalid"
      else
        error 1 "Failed to download patch ${patch_name}"
      fi
    fi
  done <<<"$(jq -r 'keys[]' <<<"${patches_json}")"

  while read -r patch_name; do
    [[ -z "${patch_name}" ]] && continue
    file_path="Data/${patch_name}.MPQ"
    if [[ -f "${targetdir}/${file_path}" ]]; then
      current_size="$(stat -c%s "${targetdir}/${file_path}")"
      size_after=$((size_after + current_size))
    fi
  done <<<"$(jq -r 'keys[]' <<<"${patches_json}")"

  local size_delta=$((size_after - size_before))
  echo -e "\n=========================================="
  echo -e "         PATCH OPERATION SUMMARY          "
  echo -e "=========================================="
  echo -e "Total Network Downloaded:  $(format_bytes ${size_downloaded})"
  echo -e "Local Storage Before:      $(format_bytes ${size_before})"
  echo -e "Local Storage After:       $(format_bytes ${size_after})"
  if ((size_delta >= 0)); then
    echo -e "Storage Growth (Delta):   +$(format_bytes ${size_delta})"
  else
    abs_delta=$((size_delta * -1))
    echo -e "Storage Shrinkage (Delta): -$(format_bytes ${abs_delta})"
  fi
  echo -e "==========================================\n"
}

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
  --verify) debug "Verification mode enabled" ;;
  *) filtered_args+=("${arg}") ;;
  esac
done
set -- "${filtered_args[@]}"

manage_token
if [[ "${debug}" == "true" ]]; then
  debug "Public manifest:"
  echo "${manifest}" | jq . 2>/dev/null || echo "${manifest}" >&2
fi

if [[ -z "${manifest}" || "${manifest}" == "null" ]] || jq -e '.success == false' <<<"${manifest}" >/dev/null 2>&1; then
  error 1 "Failed to establish context with verification server."
fi

patch_map="$(jq -cM '.patches' <<<"${manifest}" 2>/dev/null)"

if [[ -z "${patch_map}" || "${patch_map}" == "null" ]]; then
  error 1 "Manifest formatting verification failed; no patch objects detected."
fi

if [[ -z "${authToken}" || "${authToken}" == "null" ]]; then
  error 1 "Authentication token is missing. Please log in again."
fi

update_manifest_patches "${patch_map}"
download_extra_files

[[ -f "${targetdir}/Cache/invalid" ]] || [ -d "${targetdir}/Cache" ] && clearCache

if [ ${#} -gt 0 ]; then
  if [[ "${*,,}" == *wow.exe* ]]; then
    unset TZ
    export PROTON_FORCE_LARGE_ADDRESS_AWARE=1 WINE_LARGE_ADDRESS_AWARE=1
  fi
  exec "${@}"
fi
