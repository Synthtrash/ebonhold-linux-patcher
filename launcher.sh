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
quick=false
status_only=false
addons=""
list_addons_only=false
select_addons=false
check_addons_only=false
addon_catalog=""
addon_ids=()
addon_update_ids=()
addon_update_names=()
declare -A manifest_dest_paths=()
remove_addons=""
remove_addons_only=false
relogin=false
forget_login=false
renew_auth=false

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

login_api="https://api.project-ebonhold.com/api/auth/login"
games_api="https://api.project-ebonhold.com/api/launcher/games"
status_api="https://api.project-ebonhold.com/api/server/status"
patch_download_base="https://api.project-ebonhold.com/api/launcher/download?file_ids="
addons_api="https://api.project-ebonhold.com/api/launcher/addons"
addon_download_base="https://api.project-ebonhold.com/api/launcher/addons/download?addon_ids="
token_file="${targetdir}/.updaterToken"
addon_state_file="${targetdir}/Interface/AddOns/.ebonhold-launcher-addons.json"
account_hint_key="username"
keyring_service="com.project-ebonhold.updater"
installation_identity="$(realpath -e "${targetdir}" 2>/dev/null || realpath -m "${targetdir}")"
installation_key="$(printf '%s' "${installation_identity}" | sha256sum | cut -d' ' -f1)"
keyring_tool="$(command -v secret-tool 2>/dev/null || true)"
if [[ -n "${XDG_RUNTIME_DIR:-}" ]]; then
  auth_lock_dir="${XDG_RUNTIME_DIR}/ebonhold-updater"
else
  auth_lock_dir="${TMPDIR:-/tmp}/ebonhold-updater-${EUID}"
fi
auth_lock_file="${auth_lock_dir}/auth-${installation_key}.lock"
auth_lock_fd=""

is_read_only_mode() {
  [[ "${verify_only}" == "true" || "${dry_run}" == "true" ]]
}

credential_path_is_safe() {
  local destination="$1"
  local directory

  [[ -n "${destination}" && ! -L "${destination}" ]] || return 1
  [[ ! -e "${destination}" || -f "${destination}" ]] || return 1
  directory="$(dirname "${destination}")"
  [[ -d "${directory}" && ! -L "${directory}" ]]
}

write_private_credential() {
  local destination="$1"
  local value="$2"
  local directory basename temporary previous_umask result

  credential_path_is_safe "${destination}" || return 1
  directory="$(dirname "${destination}")"
  basename="$(basename "${destination}")"
  previous_umask="$(umask)"
  umask 077
  temporary="$(mktemp "${directory}/.${basename}.XXXXXX")" || {
    umask "${previous_umask}"
    return 1
  }
  if ! chmod 600 "${temporary}" || ! printf '%s' "${value}" >"${temporary}"; then
    rm -f -- "${temporary}"
    umask "${previous_umask}"
    return 1
  fi
  if [[ -L "${destination}" ]] || ! mv -f -- "${temporary}" "${destination}"; then
    rm -f -- "${temporary}"
    umask "${previous_umask}"
    return 1
  fi
  [[ "$(stat -c '%a' "${destination}" 2>/dev/null)" == "600" ]]
  result=$?
  umask "${previous_umask}"
  return "${result}"
}

curl_config_escape() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  value="${value//$'\n'/\\n}"
  value="${value//$'\r'/\\r}"
  printf '%s' "${value}"
}

curl_authenticated() {
  local token="$1"
  shift
  local config escaped status previous_umask

  [[ -n "${token}" && ! "${token}" =~ [[:cntrl:]] ]] || return 1
  previous_umask="$(umask)"
  umask 077
  config="$(mktemp)" || {
    umask "${previous_umask}"
    return 1
  }
  if ! chmod 600 "${config}" ||
    ! escaped="$(curl_config_escape "${token}")" ||
    ! printf 'header = "Authorization: Bearer %s"\n' "${escaped}" >"${config}"; then
    rm -f -- "${config}"
    umask "${previous_umask}"
    return 1
  fi
  curl --config "${config}" "$@"
  status=$?
  rm -f -- "${config}"
  umask "${previous_umask}"
  return "${status}"
}

curl_url() {
  local url="$1"
  shift
  local config escaped status previous_umask

  [[ "${url}" == https://* && ! "${url}" =~ [[:cntrl:]] ]] || return 1
  previous_umask="$(umask)"
  umask 077
  config="$(mktemp)" || {
    umask "${previous_umask}"
    return 1
  }
  if ! chmod 600 "${config}" ||
    ! escaped="$(curl_config_escape "${url}")" ||
    ! printf 'url = "%s"\n' "${escaped}" >"${config}"; then
    rm -f -- "${config}"
    umask "${previous_umask}"
    return 1
  fi
  curl --config "${config}" "$@"
  status=$?
  rm -f -- "${config}"
  umask "${previous_umask}"
  return "${status}"
}

private_runtime_directory() {
  local directory="$1"
  local mode

  [[ -d "${directory}" && ! -L "${directory}" && -O "${directory}" ]] || return 1
  mode="$(stat -c '%a' "${directory}" 2>/dev/null)" || return 1
  [[ "${mode}" == *00 ]]
}

lock_parent_is_safe() {
  local directory="$1"
  local mode owner

  [[ -d "${directory}" && ! -L "${directory}" ]] || return 1
  mode="$(stat -c '%a' "${directory}" 2>/dev/null)" || return 1
  owner="$(stat -c '%u' "${directory}" 2>/dev/null)" || return 1
  if (((8#${mode} & 0022) != 0)); then
    (((8#${mode} & 01000) != 0 && owner == 0)) || return 1
  fi
}

prepare_auth_lock_dir() {
  local parent

  if [[ -L "${auth_lock_dir}" ]]; then
    return 1
  fi
  parent="$(dirname "${auth_lock_dir}")"
  lock_parent_is_safe "${parent}" || return 1
  if [[ ! -e "${auth_lock_dir}" ]]; then
    (
      umask 077
      mkdir -- "${auth_lock_dir}"
    ) 2>/dev/null || {
      [[ -d "${auth_lock_dir}" ]] || return 1
    }
  fi
  private_runtime_directory "${auth_lock_dir}"
}

auth_lock_file_is_safe() {
  [[ ! -L "${auth_lock_file}" ]] || return 1
  if [[ -e "${auth_lock_file}" ]]; then
    [[ -f "${auth_lock_file}" && -O "${auth_lock_file}" ]] || return 1
  fi
}

acquire_auth_lock() {
  local previous_umask

  command -v flock >/dev/null 2>&1 || return 1
  prepare_auth_lock_dir || return 1
  auth_lock_file_is_safe || return 1
  previous_umask="$(umask)"
  umask 077
  if ! exec {auth_lock_fd}>>"${auth_lock_file}"; then
    umask "${previous_umask}"
    return 1
  fi
  umask "${previous_umask}"
  if ! auth_lock_file_is_safe || ! flock -x "${auth_lock_fd}"; then
    exec {auth_lock_fd}>&-
    auth_lock_fd=""
    return 1
  fi
}

release_auth_lock() {
  [[ -n "${auth_lock_fd}" ]] || return 0
  flock -u "${auth_lock_fd}" 2>/dev/null || true
  exec {auth_lock_fd}>&-
  auth_lock_fd=""
}

account_key_for_user() {
  local username="$1"
  [[ -n "${username}" ]] || return 1
  printf '%s' "${username}" | sha256sum | cut -d' ' -f1
}

keyring_lookup_secret() {
  local kind="$1"
  local account="${2:-}"
  [[ -n "${keyring_tool}" ]] || return 3
  if [[ -n "${account}" ]]; then
    "${keyring_tool}" lookup service "${keyring_service}" installation "${installation_identity}" \
      kind "${kind}" account "${account}" 2>/dev/null
  else
    "${keyring_tool}" lookup service "${keyring_service}" installation "${installation_identity}" \
      kind "${kind}" 2>/dev/null
  fi
}

keyring_store_secret() {
  local kind="$1"
  local secret="$2"
  local account="${3:-}"
  is_read_only_mode && return 1
  [[ -n "${keyring_tool}" ]] || return 1
  if [[ -n "${account}" ]]; then
    printf '%s' "${secret}" |
      "${keyring_tool}" store --label="Ebonhold Updater login" \
        service "${keyring_service}" installation "${installation_identity}" kind "${kind}" account "${account}" \
        >/dev/null 2>&1
  else
    printf '%s' "${secret}" |
      "${keyring_tool}" store --label="Ebonhold Updater account" \
        service "${keyring_service}" installation "${installation_identity}" kind "${kind}" \
        >/dev/null 2>&1
  fi
}

keyring_clear_secret() {
  local kind="$1"
  local account="${2:-}"
  [[ -n "${keyring_tool}" ]] || return 1
  if [[ -n "${account}" ]]; then
    "${keyring_tool}" clear service "${keyring_service}" installation "${installation_identity}" \
      kind "${kind}" account "${account}" >/dev/null 2>&1
  else
    "${keyring_tool}" clear service "${keyring_service}" installation "${installation_identity}" \
      kind "${kind}" >/dev/null 2>&1
  fi
}

keyring_clear_installation() {
  [[ -n "${keyring_tool}" ]] || return 1
  "${keyring_tool}" clear service "${keyring_service}" installation "${installation_identity}" >/dev/null 2>&1
}

load_saved_credentials() {
  local saved_account=""

  saved_username=""
  saved_password=""
  [[ -n "${keyring_tool}" ]] || return 3
  if ! saved_username="$(keyring_lookup_secret "${account_hint_key}")" || [[ -z "${saved_username}" ]]; then
    saved_username=""
    return 1
  fi
  saved_account="$(account_key_for_user "${saved_username}")" || {
    saved_username=""
    return 1
  }
  if ! saved_password="$(keyring_lookup_secret password "${saved_account}")" || [[ -z "${saved_password}" ]]; then
    saved_username=""
    saved_password=""
    return 1
  fi
  return 0
}

save_credentials_to_keyring() {
  local username="$1"
  local password="$2"
  local account

  account="$(account_key_for_user "${username}")" || return 1
  keyring_store_secret "${account_hint_key}" "${username}" || return 1
  if ! keyring_store_secret password "${password}" "${account}"; then
    keyring_clear_secret "${account_hint_key}" || true
    return 1
  fi
  return 0
}

safe_destination() {
  local path="$1"
  local destination

  if [[ -z "${path}" || "${path}" == /* || "${path}" == "." || "${path}" == ".." ||
    "${path}" == */./* || "${path}" == */. || "${path}" == */.. || "${path}" == */../* || "${path}" == ../* ||
    "${path}" == *"|"* || "${path}" =~ [[:cntrl:]] ]]; then
    return 1
  fi

  destination="$(realpath -m "${targetdir}/${path}")"
  [[ "${destination}" == "${targetdir}/"* ]] || return 1
  printf '%s' "${destination}"
}

manifest_md5() {
  local encoded="$1"
  local checksum

  checksum="$(printf '%s' "${encoded}" | base64 --decode 2>/dev/null | od -An -tx1 | tr -d ' \n')" || return 1
  [[ "${checksum}" =~ ^[[:xdigit:]]{32}$ ]] || return 1
  printf '%s' "${checksum,,}"
}

fetch_games_manifest() {
  local token="$1"
  local response=""

  games_http_code=""
  games_error=""
  if ! response="$(curl_authenticated "${token}" -sS --connect-timeout 10 --max-time 60 -w $'\n%{http_code}' \
    -H "User-Agent: EbonholdLauncher/1.0" \
    -H "Accept: application/json" \
    -H "X-Client-Id: EbonholdLauncher" \
    -H "Origin: https://project-ebonhold.com" \
    -H "Referer: https://project-ebonhold.com/download" \
    "${games_api}")"; then
    games_error="Network request failed."
    return 1
  fi

  games_http_code="${response##*$'\n'}"
  response="${response%$'\n'*}"
  if [[ ! "${games_http_code}" =~ ^2[0-9][0-9]$ ]] ||
    ! jq -e '.success == true and (.data.games | type == "array")' <<<"${response}" >/dev/null 2>&1; then
    games_error="$(jq -r '.error // .message // empty' <<<"${response}" 2>/dev/null)"
    [[ -n "${games_error}" ]] || games_error="HTTP ${games_http_code:-unknown}"
    games_error="${games_error//"${token}"/[redacted]}"
    [[ "${games_http_code}" == "401" || "${games_http_code}" == "403" ]] && return 2
    return 1
  fi

  games_manifest="${response}"
  return 0
}

authenticate_with_credentials() {
  local username="$1"
  local password="$2"
  local session=""
  local curl_status=0

  login_token=""
  login_http_code=""
  login_error=""
  curl_status=0
  session="$(printf '%s' "${password}" |
    jq -n --arg username "${username}" --rawfile password /dev/stdin \
      '{username: $username, password: $password, rememberMe: true}' |
    curl -sS --connect-timeout 10 --max-time 60 -X POST -w $'\n%{http_code}' \
      -H "Content-Type: application/json" \
      -H "User-Agent: EbonholdLauncher/1.0" \
      -d @- \
      "${login_api}")" || curl_status=$?

  login_http_code="${session##*$'\n'}"
  session="${session%$'\n'*}"
  if [[ ${curl_status} -ne 0 ]]; then
    login_error="Authentication request failed."
    return 1
  fi

  if [[ "${login_http_code}" == "401" || "${login_http_code}" == "403" ]]; then
    login_error="Ebonhold rejected the supplied credentials."
    return 2
  fi
  if [[ ! "${login_http_code}" =~ ^2[0-9][0-9]$ ]]; then
    login_error="Authentication service returned HTTP ${login_http_code:-unknown}."
    return 1
  fi
  if ! jq -e '.success == true' <<<"${session}" >/dev/null 2>&1; then
    login_error="Authentication service rejected the login request (HTTP ${login_http_code:-unknown})."
    return 1
  fi

  login_token="$(jq -r '.token // empty' <<<"${session}")"
  if [[ -z "${login_token}" || "${login_token}" == "null" || "${login_token}" =~ [[:space:]] ]]; then
    login_token=""
    login_error="Login succeeded but no valid token was returned."
    return 1
  fi
  return 0
}

prompt_remember_login() {
  local answer=""
  local warning_text="Remember me on this computer? Your password will be stored in your desktop keyring. Only enable this on a computer you trust."

  remember_login=false
  if [[ "${GUI}" == "true" ]]; then
    answer="$(zenity --list --checklist --title="Ebonhold Login" --text="${warning_text}" \
      --column="Remember" --column="Choice" --separator="|" FALSE "Remember me on this computer" 2>/dev/null)" || answer=""
    [[ -n "${answer}" ]] && remember_login=true
    return 0
  fi

  printf '%s\n' "${warning_text}" >&2
  read -r -p 'Remember me on this computer? [y/N] ' answer || answer=""
  case "${answer}" in
  [Yy] | [Yy][Ee][Ss]) remember_login=true ;;
  esac
}

persist_authenticated_token() {
  local token="$1"

  is_read_only_mode && return 0
  write_private_credential "${token_file}" "${token}" || return 1
  debug "Secure auth token serialized locally (permissions: 600)."
}

save_manual_login_if_requested() {
  local username="$1"
  local password="$2"

  is_read_only_mode && return 0
  [[ "${remember_login:-false}" == "true" ]] || return 0
  if [[ -z "${keyring_tool}" ]]; then
    warn "Remember me was not saved: the optional secret-tool desktop-keyring helper is unavailable. The launcher will continue without storing your password."
    return 0
  fi
  if ! save_credentials_to_keyring "${username}" "${password}"; then
    warn "Remember me was not saved because the desktop keyring is unavailable or locked. The launcher will continue without storing your password."
    return 0
  fi
  debug "Remembered login stored in the desktop keyring for this installation."
}

manage_token() {
  local token=""
  local user=""
  local pass=""
  local manifest_result=0
  local login_result=0
  local had_cached_token=false
  local force_renew="${renew_auth}"
  local remember_login=false

  if [[ -L "${token_file}" ]]; then
    error 1 "Refusing symlinked authentication token destination: ${token_file}"
  fi
  if [[ -f "${token_file}" ]]; then
    if ! token="$(<"${token_file}")"; then
      error 1 "Could not read the cached authentication token."
    fi
    if [[ -z "${token}" || "${token}" == "null" ]]; then
      token=""
    fi
  fi
  [[ -n "${token}" ]] && had_cached_token=true

  if [[ "${relogin}" != "true" && "${force_renew}" != "true" && -n "${token}" ]]; then
    debug "Auth token found, verifying token."
    if fetch_games_manifest "${token}"; then
      debug "Token verified successfully."
      authToken="${token}"
      return 0
    else
      manifest_result=$?
      if [[ ${manifest_result} -eq 2 ]]; then
        debug "Token invalid or expired; cached token will be retained until replacement succeeds."
        token=""
      else
        error 1 "Failed to fetch games manifest (HTTP ${games_http_code:-unknown}).\n${games_error}"
      fi
    fi
  elif [[ "${relogin}" == "true" ]]; then
    debug "Manual re-login requested; cached credentials will not be used."
  elif [[ "${force_renew}" == "true" ]]; then
    debug "Forced authentication renewal requested; the cached token will not be reused."
  fi

  if [[ "${relogin}" != "true" && -n "${keyring_tool}" ]]; then
    if load_saved_credentials; then
      debug "Trying the opted-in desktop-keyring login once."
      if authenticate_with_credentials "${saved_username}" "${saved_password}"; then
        login_result=0
      else
        login_result=$?
      fi
      if [[ ${login_result} -eq 0 ]]; then
        if ! fetch_games_manifest "${login_token}"; then
          error 1 "Failed to fetch games manifest after saved login (HTTP ${games_http_code:-unknown}).\n${games_error}"
        fi
        persist_authenticated_token "${login_token}" || error 1 "Authenticated successfully, but could not securely write ${token_file}."
        authToken="${login_token}"
        unset saved_username saved_password
        debug "Saved desktop-keyring login succeeded."
        return 0
      elif [[ ${login_result} -eq 1 ]]; then
        error 1 "Saved desktop-keyring login could not reach the authentication service.\n${login_error}"
      else
        warn "The saved desktop-keyring login was rejected. Please sign in manually; it will not be retried automatically."
      fi
      unset saved_username saved_password
    elif [[ ${had_cached_token} == true ]]; then
      warn "The saved desktop-keyring login is unavailable or locked. Continuing with one manual login attempt."
    fi
  elif [[ ${had_cached_token} == true && -z "${keyring_tool}" ]]; then
    warn "The optional secret-tool desktop-keyring helper is unavailable. Continuing with one manual login attempt."
  fi

  debug "No valid token found. Please log in."
  if [[ "${GUI}" == "false" && "${interactiveShell}" == "false" ]]; then
    error 1 "Cannot authenticate without a terminal or display. Run the launcher interactively once, or provide a graphical display."
  fi

  user="$(prompt_text "Ebonhold Login" "Enter your username:")" || error 1 "Cannot prompt for username: no terminal and no display available. Run interactively or install zenity."
  pass="$(prompt_password "Ebonhold Login" "Password for ${user}")" || error 1 "Cannot prompt for password: no terminal and no display available."

  debug "Posting credentials to authentication portal..."
  if authenticate_with_credentials "${user}" "${pass}"; then
    login_result=0
  else
    login_result=$?
  fi
  if [[ ${login_result} -eq 2 ]]; then
    error 1 "Session authorization failed.\n${login_error}"
  elif [[ ${login_result} -ne 0 ]]; then
    error 1 "Session authorization failed.\n${login_error}"
  fi
  debug "HTTP return code ${login_http_code}"

  if ! fetch_games_manifest "${login_token}"; then
    error 1 "Failed to fetch games manifest (HTTP ${games_http_code:-unknown}).\n${games_error}"
  fi

  if ! is_read_only_mode; then
    prompt_remember_login
  fi
  persist_authenticated_token "${login_token}" || error 1 "Authenticated successfully, but could not securely write ${token_file}."
  if ! is_read_only_mode && [[ "${remember_login}" == "false" && -n "${keyring_tool}" ]]; then
    if ! keyring_clear_installation; then
      warn "The previous desktop-keyring login could not be cleared. Use --forget-login and try again."
    fi
  fi
  save_manual_login_if_requested "${user}" "${pass}"

  unset user pass
  authToken="${login_token}"
  debug "Authentication successful."
}

forget_login_state() {
  local failed=false

  if ! acquire_auth_lock; then
    warn "Could not lock the updater credentials for clearing."
    return 1
  fi

  if [[ -e "${token_file}" || -L "${token_file}" ]]; then
    if ! rm -f -- "${token_file}"; then
      warn "Could not remove the cached updater token: ${token_file}"
      failed=true
    fi
  fi

  if [[ -z "${keyring_tool}" ]]; then
    warn "Could not clear desktop-keyring credentials: secret-tool is unavailable."
    failed=true
  elif ! keyring_clear_installation; then
    warn "Could not clear desktop-keyring credentials for this installation. The keyring may be locked; retry --forget-login after unlocking it."
    failed=true
  fi
  release_auth_lock

  if [[ "${failed}" == "true" ]]; then
    return 1
  fi
  [[ "${quiet}" == "true" ]] || printf 'Forgot the cached updater token and desktop-keyring login for this installation.\n'
  return 0
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

warn() {
  local msg="${*}"
  if [[ "${GUI}" == "true" ]]; then
    zenity --warning --title="Ebonhold Login" --text="${msg}" --width=460 2>/dev/null || true
  fi
  printf '%s[WARN]%s %s\n' "${YELLOW}" "${NC}" "${msg}" >&2
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
  local unit

  if ((bytes < 1024)); then
    printf '%s B\n' "${bytes}"
    return 0
  fi

  local bytes_formatted
  local decimal_point

  if ((bytes < 1048576)); then
    bytes_formatted="$(printf 'scale=2; %s/1024\n' "${bytes}" | LC_ALL=C bc)"
    unit="KB"
  else
    bytes_formatted="$(printf 'scale=2; %s/1048576\n' "${bytes}" | LC_ALL=C bc)"
    unit="MB"
  fi

  decimal_point="$(locale decimal_point 2>/dev/null)"
  [[ -z "${decimal_point}" ]] && decimal_point='.'
  if [[ "${decimal_point}" != '.' ]]; then
    bytes_formatted="${bytes_formatted/./${decimal_point}}"
  fi

  printf '%s %s\n' "${bytes_formatted}" "${unit}"
}

download_file_by_id() {
  local file_id="$1"
  local dest_path="$2"
  local description="${3:-$dest_path}"
  local expected_md5="$4"
  local tmp_out=""
  local tmp_file=""
  local downloaded_md5=""
  local status=""
  local curl_status=0
  local response=""
  local url=""
  local size=""

  debug "Downloading file ID ${file_id} -> ${dest_path}"

  tmp_out="$(mktemp)" || return 1
  status="$(curl_authenticated "${authToken}" -sS --connect-timeout 10 --max-time 60 -w "%{http_code}" -o "${tmp_out}" \
    -H "User-Agent: EbonholdLauncher/1.0" \
    -H "Accept: application/json" \
    -H "X-Client-Id: EbonholdLauncher" \
    -H "Origin: https://project-ebonhold.com" \
    -H "Referer: https://project-ebonhold.com/download" \
    "${patch_download_base}${file_id}" 2>/dev/null)"
  curl_status=$?
  response="$(<"${tmp_out}")"
  rm -f -- "${tmp_out}"

  [[ ${curl_status} -eq 0 ]] || status="${status:-000}"
  if [[ "${status}" == "401" ]]; then
    return 2
  fi
  if [[ "${status}" != "200" ]]; then
    printf '%s[ERROR]%s Failed to get download URL for ID %s (HTTP %s)\n' "${RED}" "${NC}" "${file_id}" "${status:-unknown}"
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

  mkdir -p "$(dirname "${dest_path}")" || return 1
  tmp_file="$(mktemp "${dest_path}.tmp.XXXXXX")" || return 1

  if [[ "${quiet}" == "false" ]]; then
    printf '%s[DOWNLOADING]%s %s...\n' "${BLUE}" "${NC}" "${description}"
  fi
  if ! curl_url "${url}" --fail --location --show-error --connect-timeout 10 --max-time 600 --retry 2 --retry-all-errors -o "${tmp_file}"; then
    rm -f -- "${tmp_file}"
    printf '%s[ERROR]%s Failed to download %s\n' "${RED}" "${NC}" "${description}"
    return 1
  fi

  downloaded_md5="$(md5sum "${tmp_file}" | cut -d' ' -f1)"
  if [[ "${downloaded_md5}" != "${expected_md5}" ]]; then
    rm -f -- "${tmp_file}"
    printf '%s[ERROR]%s Downloaded checksum mismatch for %s\n' "${RED}" "${NC}" "${description}"
    return 1
  fi
  mv -f -- "${tmp_file}" "${dest_path}" || return 1

  size="$(stat -c%s "${dest_path}" 2>/dev/null || printf '0')"
  if [[ "${quiet}" == "false" ]]; then
    printf '%s[FINISHED]%s %s (%s)\n\n' "${GREEN}" "${NC}" "${description}" "$(format_bytes "${size}")"
  fi
  return 0
}

check_server_status() {
  debug "Checking server status..."
  local response=""
  if ! response="$(curl_authenticated "${authToken}" -sS --connect-timeout 10 --max-time 30 \
    -H "User-Agent: EbonholdLauncher/1.0" \
    -H "Accept: application/json" \
    -H "X-Client-Id: EbonholdLauncher" \
    "${status_api}")"; then
    printf '%s[WARN]%s Could not retrieve server status.\n' "${YELLOW}" "${NC}"
    return 0
  fi
  if [[ -z "${response}" ]] || ! jq -e '.success' <<<"${response}" >/dev/null 2>&1; then
    printf '%s[WARN]%s Could not retrieve server status.\n' "${YELLOW}" "${NC}"
    return 0
  fi
  local online="$(jq -r '.data.online' <<<"${response}")"
  local realm_name="$(jq -r '.data.serverName // "unknown"' <<<"${response}")"
  [[ "${quiet}" == "true" && "${online}" == "true" ]] && return 0
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
      [[ "${quiet}" == "false" ]] && printf '%s[REALMLIST]%s Updating %s to %s\n' "${YELLOW}" "${NC}" "${dest}" "${realmlist}"
      printf 'set realmlist %s\n' "${realmlist}" >"${dest}"
      realmlist_updated=true
    else
      debug "realmlist.wtf already correct."
    fi
  else
    [[ "${quiet}" == "false" ]] && printf '%s[REALMLIST]%s Creating %s with %s\n' "${YELLOW}" "${NC}" "${dest}" "${realmlist}"
    printf 'set realmlist %s\n' "${realmlist}" >"${dest}"
    realmlist_updated=true
  fi
}

collect_core_files() {
  local manifest="$1"
  local game_slug="$2"
  local full_mode="$3"
  local quick_mode="$4"
  local files_json

  if [[ "${full_mode}" == "true" ]]; then
    files_json="$(jq -c '(.data.common.files // [])[] | select(.file_path_from_game_root != "Data/enUS/realmlist.wtf")' <<<"$manifest")"
    files_json+=$'\n'"$(jq -c --arg slug "$game_slug" '.data.games[] | select(.slug == $slug) | .files[] | select(.file_path_from_game_root != "Data/enUS/realmlist.wtf")' <<<"$manifest")"
  elif [[ "${quick_mode}" == "true" ]]; then
    files_json="$(jq -c --arg slug "$game_slug" '.data.games[] | select(.slug == $slug) | .files[] | select(.option_slug == null and (.file_path_from_game_root | test("(^|/)patch"; "i")))' <<<"$manifest")"
  else
    files_json="$(jq -c '(.data.common.files // [])[] | select(.option_slug == null and .file_path_from_game_root != "Data/enUS/realmlist.wtf")' <<<"$manifest")"
    files_json+=$'\n'"$(jq -c --arg slug "$game_slug" '.data.games[] | select(.slug == $slug) | .files[] | select(.option_slug == null and .file_path_from_game_root != "Data/enUS/realmlist.wtf")' <<<"$manifest")"
  fi

  printf '%s\n' "$files_json" | grep -v '^$'
}

fetch_addon_catalog() {
  if ! addon_catalog="$(curl_authenticated "${authToken}" -sS --connect-timeout 10 --max-time 60 \
    -H "User-Agent: EbonholdLauncher/1.0" \
    -H "Accept: application/json" \
    -H "X-Client-Id: EbonholdLauncher" \
    -H "Origin: https://project-ebonhold.com" \
    -H "Referer: https://project-ebonhold.com/download" \
    "${addons_api}")"; then
    error 1 "Could not retrieve the addon catalog."
  fi

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

    local_mtime="$(while read -r directory; do find "${directory}" -type f -printf '%T@\n'; done <<<"${directories}" | sort -nr | {
      IFS= read -r first
      printf '%s' "${first}"
    })"
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
  printf '\nUpdates are available for: %s\n' "$(
    IFS=', '
    printf '%s' "${addon_update_names[*]}"
  )"
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
  ids="$(
    IFS=,
    printf '%s' "${addon_ids[*]}"
  )"
  if ! response="$(curl_authenticated "${authToken}" -sS --connect-timeout 10 --max-time 60 \
    -H "User-Agent: EbonholdLauncher/1.0" \
    -H "Accept: application/json" \
    -H "X-Client-Id: EbonholdLauncher" \
    -H "Origin: https://project-ebonhold.com" \
    -H "Referer: https://project-ebonhold.com/download" \
    "${addon_download_base}${ids}")"; then
    error 1 "Could not retrieve addon download URLs."
  fi
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
    if ! curl_url "${url}" --fail --location --show-error --connect-timeout 10 --max-time 600 --retry 2 --retry-all-errors -o "${archive}"; then
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

remove_addon_by_id() {
  local addon_id="$1"
  local addon_name folder directory backup_root backup_dir suffix state_dir temporary_state
  local moved=false skipped=false

  addon_name="$(jq -r --arg id "${addon_id}" '.addons[$id].name // empty' "${addon_state_file}" 2>/dev/null)"
  [[ -n "${addon_name}" ]] || error 1 "Addon '${addon_id}' is not installed via the launcher."

  backup_root="${targetdir}/.ebonhold-removed-addons"
  backup_dir="${backup_root}/${addon_name}-$(date +%Y%m%d-%H%M%S)"
  suffix=1
  while [[ -e "${backup_dir}" ]]; do
    backup_dir="${backup_root}/${addon_name}-$(date +%Y%m%d-%H%M%S)-${suffix}"
    suffix=$((suffix + 1))
  done

  while read -r folder; do
    [[ -z "${folder}" || "${folder}" == *"/"* || "${folder}" == "." || "${folder}" == ".." || "${folder}" =~ [[:cntrl:]] ]] && continue
    directory="${targetdir}/Interface/AddOns/${folder}"
    [[ -d "${directory}" ]] || continue
    [[ ! -L "${directory}" ]] || error 1 "Refusing symlinked addon folder: ${directory}."
    if addon_folder_is_shared "${addon_id}" "${folder}"; then
      printf '%s[SKIP]%s %s is also used by another addon; leaving it in place.\n' "${YELLOW}" "${NC}" "${directory}"
      skipped=true
      continue
    fi
    mkdir -p "${backup_dir}" || error 1 "Could not create backup directory for ${addon_name}."
    mv "${directory}" "${backup_dir}/${folder}" || error 1 "Could not move addon folder ${directory}."
    printf '%s[REMOVED]%s %s\n' "${GREEN}" "${NC}" "${directory}"
    moved=true
  done < <(jq -r --arg id "${addon_id}" '.addons[$id].folders[]? // empty' "${addon_state_file}" 2>/dev/null)

  if [[ "${moved}" == "false" ]] && directory="$(addon_directory "${addon_name}")" && [[ ! -L "${directory}" ]]; then
    mkdir -p "${backup_dir}" || error 1 "Could not create backup directory for ${addon_name}."
    mv "${directory}" "${backup_dir}/" || error 1 "Could not move addon folder ${directory}."
    printf '%s[REMOVED]%s %s\n' "${GREEN}" "${NC}" "${directory}"
    moved=true
  fi

  state_dir="$(dirname "${addon_state_file}")"
  if [[ -f "${addon_state_file}" ]]; then
    temporary_state="$(mktemp "${state_dir}/.ebonhold-launcher-addons.XXXXXX")" || error 1 "Could not update addon state."
    if ! jq --arg id "${addon_id}" 'del(.addons[$id])' "${addon_state_file}" >"${temporary_state}"; then
      rm -f "${temporary_state}"
      error 1 "Could not update addon state for ${addon_name}."
    fi
    mv -f "${temporary_state}" "${addon_state_file}" || error 1 "Could not save addon state."
  fi

  if [[ "${moved}" == "true" ]]; then
    printf '  Moved to %s\n' "${backup_dir}"
    printf '  To restore it, move the folders back or reinstall with --addons=%s\n' "${addon_name}"
  elif [[ "${skipped}" == "true" ]]; then
    printf '%s[REMOVED]%s %s launcher record removed; shared folders were left in place.\n' "${YELLOW}" "${NC}" "${addon_name}"
  else
    printf '%s[REMOVED]%s %s files were already gone; removed the launcher record.\n' "${YELLOW}" "${NC}" "${addon_name}"
  fi
}

resolve_addon_removals() {
  local requested="$1"
  local addon id installed_summary
  local -a requested_addons=() resolved=()

  IFS=',' read -r -a requested_addons <<<"${requested}"
  for addon in "${requested_addons[@]}"; do
    addon="${addon#"${addon%%[![:space:]]*}"}"
    addon="${addon%"${addon##*[![:space:]]}"}"
    [[ -n "${addon}" ]] || continue
    id="$(jq -r --arg addon "${addon}" '
      [.addons | to_entries[] | select((.key | tostring) == $addon or (.value.name | ascii_downcase) == ($addon | ascii_downcase)) | .key]
      | if length == 1 then .[0] else empty end' "${addon_state_file}" 2>/dev/null)"
    if [[ -z "${id}" ]]; then
      installed_summary="$(jq -r '.addons[] | .name' "${addon_state_file}" 2>/dev/null | tr '\n' ', ' | sed 's/, $//')"
      [[ -n "${installed_summary}" ]] || installed_summary="none"
      error 1 "Addon '${addon}' is not installed via the launcher.\nInstalled: ${installed_summary}"
    fi
    [[ " ${resolved[*]} " == *" ${id} "* ]] || resolved+=("${id}")
  done
  [[ ${#resolved[@]} -gt 0 ]] || error 1 "No addons were selected for removal."

  for id in "${resolved[@]}"; do
    remove_addon_by_id "${id}"
  done
}

remove_addons_interactively() {
  local -a installed_ids=() installed_names=() zenity_args
  local addon_id addon_name ids_csv="" selection number i

  while IFS=$'\t' read -r addon_id addon_name; do
    [[ -n "${addon_id}" ]] || continue
    installed_ids+=("${addon_id}")
    installed_names+=("${addon_name}")
  done < <(jq -r '.addons | to_entries[]? | [.key, .value.name] | @tsv' "${addon_state_file}" 2>/dev/null)

  [[ ${#installed_ids[@]} -gt 0 ]] || {
    printf 'No addons installed via the launcher are recorded.\n'
    return 0
  }

  if [[ "${GUI}" == "true" ]]; then
    zenity_args=(--list --checklist --title="Ebonhold Addons" --text="Select addons to remove" --column="Remove" --column="Name" --separator=",")
    for i in "${!installed_ids[@]}"; do
      zenity_args+=(FALSE "${installed_names[$i]}")
    done
    selection="$(zenity "${zenity_args[@]}")" || exit 0
    ids_csv="${selection}"
  else
    printf 'Addons installed via the launcher:\n'
    for i in "${!installed_ids[@]}"; do
      printf '  %d) %s\n' "$((i + 1))" "${installed_names[$i]}"
    done
    selection="$(prompt_text "Ebonhold Addons" "Enter the numbers to remove, separated by commas (e.g. 1,3)")" || exit 0
    while IFS=',' read -r number; do
      number="${number#"${number%%[![:space:]]*}"}"
      number="${number%"${number##*[![:space:]]}"}"
      [[ -n "${number}" ]] || continue
      [[ "${number}" =~ ^[0-9]+$ && "${number}" -ge 1 && "${number}" -le ${#installed_ids[@]} ]] || error 1 "Invalid selection: ${number}"
      if [[ -z "${ids_csv}" ]]; then
        ids_csv="${installed_ids[$((number - 1))]}"
      else
        ids_csv+=",${installed_ids[$((number - 1))]}"
      fi
    done <<<"${selection}"
  fi

  [[ -n "${ids_csv}" ]] || {
    printf 'Nothing selected.\n'
    return 0
  }
  resolve_addon_removals "${ids_csv}"
}

remove_addons_from_client() {
  local requested="$1"
  local addon_lock_fd
  local addons_dir="${targetdir}/Interface/AddOns"

  command -v flock >/dev/null 2>&1 || error 1 "Removing addons requires flock."
  mkdir -p "${addons_dir}" || error 1 "Could not create addon directory."
  [[ ! -L "${addons_dir}" ]] || error 1 "Refusing symlinked addon directory: ${addons_dir}."
  exec {addon_lock_fd}>"${addons_dir}/.ebonhold-launcher.lock"
  flock "${addon_lock_fd}" || error 1 "Could not lock addon removal."

  if [[ -n "${requested}" ]]; then
    resolve_addon_removals "${requested}"
  else
    remove_addons_interactively
  fi

  flock -u "${addon_lock_fd}"
  exec {addon_lock_fd}>&-
}

index_manifest_paths() {
  local files_json="$1"
  local path dest
  manifest_dest_paths=()
  while read -r file; do
    [[ -z "${file}" ]] && continue
    path="$(jq -r '.file_path_from_game_root' <<<"$file")"
    dest="$(safe_destination "${path}")" || continue
    manifest_dest_paths["${dest}"]=1
  done <<<"$files_json"
}

find_case_variants() {
  local dest="$1"
  local dir base pattern variant
  dir="$(dirname "${dest}")"
  base="$(basename "${dest}")"
  [[ -n "${base}" && -d "${dir}" ]] || return 0
  pattern="$(printf '%s' "${base}" | sed 's/[][*?]/\\&/g')"
  while IFS= read -r -d '' variant; do
    [[ "${variant}" == "${dest}" ]] && continue
    [[ -n "${manifest_dest_paths[${variant}]+x}" ]] && continue
    printf '%s\n' "${variant}"
  done < <(find "${dir}" -maxdepth 1 -type f -iname "${pattern}" -print0)
}

remove_case_variants() {
  local dest="$1"
  local variant removed=false
  while IFS= read -r variant; do
    [[ -z "${variant}" ]] && continue
    rm -f -- "${variant}" || continue
    [[ "${quiet}" == "false" ]] && printf '%s[STALE]%s Removed stale case-variant file: %s\n' "${YELLOW}" "${NC}" "${variant}"
    removed=true
  done < <(find_case_variants "${dest}")
  [[ "${removed}" == "true" ]]
}

verify_files() {
  local files_json="$1"
  local mismatches=0 missing=0 ok=0 total=0
  local path expected_b64 expected_md5 dest local_md5 stale

  index_manifest_paths "$files_json"

  printf '\nVerifying files...\n\n'

  while read -r file; do
    [[ -z "${file}" ]] && continue
    path="$(jq -r '.file_path_from_game_root' <<<"$file")"
    expected_b64="$(jq -r '.file_hash' <<<"$file")"
    if ! dest="$(safe_destination "${path}")" || ! expected_md5="$(manifest_md5 "${expected_b64}")"; then
      printf '%s[INVALID]%s Manifest entry for %s is unsafe or has an invalid checksum.\n' "${RED}" "${NC}" "${path}" >&2
      mismatches=$((mismatches + 1))
      continue
    fi
    total=$((total + 1))

    if [[ ! -f "${dest}" ]]; then
      printf '%s[MISSING]%s %s\n' "${RED}" "${NC}" "${path}" >&2
      missing=$((missing + 1))
    else
      local_md5="$(md5sum "${dest}" | cut -d' ' -f1)"
      if [[ "${local_md5}" != "${expected_md5}" ]]; then
        printf '%s[MISMATCH]%s %s\n' "${RED}" "${NC}" "${path}" >&2
        printf '  Expected: %s\n' "${expected_md5}" >&2
        printf '  Local:    %s\n' "${local_md5}" >&2
        mismatches=$((mismatches + 1))
      else
        printf '%s[OK]%s %s\n' "${GREEN}" "${NC}" "${path}"
        ok=$((ok + 1))
        while IFS= read -r stale; do
          [[ -z "${stale}" ]] && continue
          printf '%s[STALE]%s Case-variant of %s detected: %s (running the launcher normally will remove it)\n' "${YELLOW}" "${NC}" "${path}" "${stale}" >&2
        done < <(find_case_variants "${dest}")
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
  local size_before=0 size_after=0 current_size=0
  local stale_dest

  index_manifest_paths "$files_json"

  if [[ "${verify_only}" == "true" ]]; then
    if [[ "${quiet}" == "true" ]]; then
      verify_files "$files_json" >/dev/null
    else
      verify_files "$files_json"
    fi
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
    [[ "${id}" =~ ^[1-9][0-9]*$ ]] || error 1 "Invalid manifest file ID for: ${path}"

    download_needed=false
    if [[ ! -f "${dest}" ]]; then
      [[ "${quiet}" == "false" ]] && printf '%s[MISSING]%s %s is missing.\n' "${YELLOW}" "${NC}" "${path}"
      download_needed=true
    else
      local_md5="$(md5sum "${dest}" | cut -d' ' -f1)"
      if [[ "${local_md5}" != "${expected_md5}" ]]; then
        if [[ "${quiet}" == "false" ]]; then
          printf '%s[MISMATCH]%s %s MD5 verification failed.\n' "${RED}" "${NC}" "${path}"
          printf '  Expected: %s\n' "${expected_md5}"
          printf '  Local:    %s\n' "${local_md5}"
        fi
        download_needed=true
      else
        [[ "${quiet}" == "false" ]] && printf '%s[OK]%s %s matches MD5 signature: %s%s%s\n' "${GREEN}" "${NC}" "${path}" "${GREEN}" "${expected_md5}" "${NC}"
        if ! is_read_only_mode && remove_case_variants "${dest}"; then
          files_updated=true
        fi
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
    if [[ "${quiet}" == "false" ]]; then
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
    fi
    return 0
  fi

  if [[ "${quiet}" == "false" ]]; then
    printf '%sStarting %s downloads with %s concurrent jobs...%s\n\n' "${BLUE}" "${#download_tasks[@]}" "${parallel}" "${NC}"
  fi

  local failed=0
  local auth_retry_used=false
  local result=0
  local i=0
  local -a pending_tasks=("${download_tasks[@]}")
  local -a pids=()
  local -a pid_tasks=()
  local -a auth_retry_tasks=()

  while [[ ${#pending_tasks[@]} -gt 0 ]]; do
    pids=()
    pid_tasks=()
    auth_retry_tasks=()

    for task in "${pending_tasks[@]}"; do
      id="${task%%|*}"
      rest="${task#*|}"
      dest="${rest%%|*}"
      rest="${rest#*|}"
      path="${rest%%|*}"
      expected_md5="${rest#*|}"

      (
        download_file_by_id "${id}" "${dest}" "${path}" "${expected_md5}"
      ) &
      pids+=("$!")
      pid_tasks+=("${task}")

      if [[ ${#pids[@]} -ge ${parallel} ]]; then
        for i in "${!pids[@]}"; do
          if wait "${pids[$i]}"; then
            :
          else
            result=$?
            if [[ ${result} -eq 2 && "${auth_retry_used}" == "false" ]]; then
              auth_retry_tasks+=("${pid_tasks[$i]}")
            else
              failed=$((failed + 1))
            fi
          fi
        done
        pids=()
        pid_tasks=()
      fi
    done

    for i in "${!pids[@]}"; do
      if wait "${pids[$i]}"; then
        :
      else
        result=$?
        if [[ ${result} -eq 2 && "${auth_retry_used}" == "false" ]]; then
          auth_retry_tasks+=("${pid_tasks[$i]}")
        else
          failed=$((failed + 1))
        fi
      fi
    done

    if [[ ${#auth_retry_tasks[@]} -gt 0 && "${auth_retry_used}" == "false" ]]; then
      debug "One or more downloads received 401; coordinating one parent re-authentication."
      acquire_auth_lock || error 1 "Could not coordinate authentication renewal for downloads."
      renew_auth=true
      manage_token
      result=$?
      renew_auth=false
      release_auth_lock
      [[ ${result} -eq 0 ]] || error 1 "Could not renew authentication for downloads."
      auth_retry_used=true
      pending_tasks=("${auth_retry_tasks[@]}")
      continue
    fi
    break
  done

  if [[ $failed -gt 0 ]]; then
    error 1 "One or more downloads failed."
  fi
  files_updated=true

  for task in "${download_tasks[@]}"; do
    stale_dest="${task#*|}"
    stale_dest="${stale_dest%%|*}"
    if remove_case_variants "${stale_dest}"; then
      files_updated=true
    fi
  done

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
  if [[ -n "${deleted}" ]]; then
    debug "Update completed, cleared local caches:\n${deleted}"
  fi
  return 0
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
  --quick)
    quick=true
    ;;
  --status)
    status_only=true
    ;;
  --game=*)
    game="${arg#--game=}"
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
  --remove-addons)
    remove_addons_only=true
    ;;
  --remove-addons=*)
    remove_addons_only=true
    remove_addons="${arg#--remove-addons=}"
    ;;
  --dry-run)
    dry_run=true
    debug "Dry-run mode enabled"
    ;;
  --relogin)
    relogin=true
    ;;
  --forget-login)
    forget_login=true
    ;;
  --help)
    cat <<EOF
Usage: $0 [OPTIONS] [--] [game arguments]

Options:
  --debug           Enable verbose output
  --verify          Check files against manifest (no downloads or addon changes)
  --dry-run         Show what would be done without downloading
  --quick           Check only required game patch files
  --full            Update all common and game files, including optional files
  --status          Show realm status and exit
  --game=SLUG       Select a game from the launcher manifest
  --list-addons     Show addons in the official launcher catalog and exit
  --check-addons    Show installed addon update recommendations and exit
  --select-addons   Interactively select addons to download
  --remove-addons   Interactively select installed addons to remove
  --remove-addons=LIST
                    Remove comma-separated installed addon names or IDs
  --addons=LIST     Download comma-separated addon names or IDs
  --quiet           Suppress routine output; warnings and errors remain
  --relogin         Ignore cached/saved login and request a fresh manual login
  --forget-login    Remove this installation's cached token and keyring login
  --help            Show this help message

Default mode updates all required common and game files. For Steam, use: ./launcher.sh --quick --quiet -- %command%
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

if [[ "${quick}" == "true" && "${full}" == "true" ]]; then
  error 1 "--quick and --full cannot be used together."
fi

if [[ "${verify_only}" == "true" && "${dry_run}" == "true" ]]; then
  error 1 "--verify and --dry-run cannot be used together."
fi

if [[ "${select_addons}" == "true" && -n "${addons}" ]]; then
  error 1 "--select-addons and --addons cannot be used together."
fi

if [[ "${verify_only}" == "true" && ("${select_addons}" == "true" || -n "${addons}") ]]; then
  error 1 "--verify cannot be combined with --select-addons or --addons."
fi

if [[ "${status_only}" == "true" && ("${verify_only}" == "true" || "${dry_run}" == "true" || "${quick}" == "true" || "${full}" == "true" || "${list_addons_only}" == "true" || "${check_addons_only}" == "true" || "${select_addons}" == "true" || "${remove_addons_only}" == "true" || -n "${addons}" || -n "${remove_addons}" || $# -gt 0) ]]; then
  error 1 "--status cannot be combined with update, addon, or game-launch options."
fi

if [[ ("${list_addons_only}" == "true" || "${check_addons_only}" == "true") && ("${status_only}" == "true" || "${verify_only}" == "true" || "${dry_run}" == "true" || "${quick}" == "true" || "${full}" == "true" || "${list_addons_only}" == "true" && "${check_addons_only}" == "true" || "${select_addons}" == "true" || "${remove_addons_only}" == "true" || -n "${addons}" || -n "${remove_addons}" || $# -gt 0) ]]; then
  error 1 "Addon report modes cannot be combined with update, addon, or game-launch options."
fi

if [[ ("${remove_addons_only}" == "true" || -n "${remove_addons}") && ("${status_only}" == "true" || "${verify_only}" == "true" || "${dry_run}" == "true" || "${quick}" == "true" || "${full}" == "true" || "${list_addons_only}" == "true" || "${check_addons_only}" == "true" || "${select_addons}" == "true" || -n "${addons}" || $# -gt 0) ]]; then
  error 1 "Addon removal cannot be combined with update, addon, or game-launch options."
fi

if [[ "${forget_login}" == "true" && ("${relogin}" == "true" || "${verify_only}" == "true" || "${dry_run}" == "true" || "${status_only}" == "true" || "${quick}" == "true" || "${full}" == "true" || "${list_addons_only}" == "true" || "${check_addons_only}" == "true" || "${select_addons}" == "true" || "${remove_addons_only}" == "true" || -n "${addons}" || -n "${remove_addons}" || $# -gt 0) ]]; then
  error 1 "--forget-login cannot be combined with another mode or game arguments."
fi

if [[ "${relogin}" == "true" && ("${forget_login}" == "true" || "${verify_only}" == "true" || "${dry_run}" == "true") ]]; then
  error 1 "--relogin cannot be combined with --forget-login, --verify, or --dry-run."
fi

if [[ "${forget_login}" == "true" ]]; then
  if ! forget_login_state; then
    error 1 "Login forget was incomplete; see the warning above and retry after fixing the reported keyring or file issue."
  fi
  exit 0
fi

acquire_auth_lock || error 1 "Could not coordinate authentication."
manage_token
manage_result=$?
release_auth_lock
[[ ${manage_result} -eq 0 ]] || error 1 "Authentication failed."

check_server_status

if [[ "${status_only}" == "true" ]]; then
  exit 0
fi

if [[ -z "${game}" ]]; then
  mapfile -t available_games < <(jq -r '.data.games[]?.slug' <<<"${games_manifest}")
  if [[ ${#available_games[@]} -eq 1 ]]; then
    game="${available_games[0]}"
    debug "Automatically selected game '${game}'."
  else
    error 1 "No game selected. Use --game=SLUG. Available: $(printf '%s ' "${available_games[@]}")"
  fi
elif ! jq -e --arg slug "${game}" '.data.games[] | select(.slug == $slug)' <<<"${games_manifest}" >/dev/null 2>&1; then
  error 1 "Game slug '${game}' not found in manifest. Available: $(jq -r '.data.games[]?.slug' <<<"${games_manifest}" | tr '\n' ' ')"
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

if [[ "${remove_addons_only}" == "true" || -n "${remove_addons}" ]]; then
  is_read_only_mode && error 1 "--remove-addons cannot be combined with --verify or --dry-run."
  remove_addons_from_client "${remove_addons}"
  exit 0
fi

realmlist="$(jq -r --arg slug "${game}" '.data.games[] | select(.slug == $slug) | .realmlist' <<<"${games_manifest}")"
if ! is_read_only_mode && [[ -n "${realmlist}" && "${realmlist}" != "null" ]]; then
  update_realmlist "${realmlist}"
fi

files_to_process="$(collect_core_files "${games_manifest}" "${game}" "${full}" "${quick}")"

if [[ -z "${files_to_process}" ]]; then
  error 1 "No files found in manifest for game slug '${game}'."
fi

if ! update_files "${files_to_process}"; then
  exit 1
fi

if [[ ${#addon_ids[@]} -gt 0 ]]; then
  if [[ "${dry_run}" == "true" ]]; then
    [[ "${quiet}" == "false" ]] && printf '%s[DRY RUN]%s Would download selected launcher addons: %s\n' "${YELLOW}" "${NC}" "${addons}"
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
  unset authToken games_manifest login_token saved_password
  exec "${@}"
fi
