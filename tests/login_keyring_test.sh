#!/usr/bin/env bash
set -euo pipefail

repo_root="$(dirname "$(dirname "$(realpath "$0")")")"
test_root="$(mktemp -d)"
mock_bin="${test_root}/bin"
state_dir="${test_root}/state"
runtime_dir="${test_root}/runtime"
fallback_runtime_dir="${test_root}/fallback-tmp"
trap 'rm -rf "${test_root}"' EXIT
mkdir -p "${mock_bin}" "${state_dir}" "${runtime_dir}" "${fallback_runtime_dir}"
chmod 700 "${runtime_dir}" "${fallback_runtime_dir}"

cat >"${mock_bin}/zenity" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${ZENITY_LOG:?}"
case "${1:-}" in
--entry)
  printf '%s' "${MOCK_USER:-test-user}"
  ;;
--password)
  printf '%s' "${MOCK_PASSWORD:-manual-pass}"
  ;;
--list)
  [[ "${MOCK_REMEMBER:-false}" == "true" ]] && printf '%s' 'Remember me on this computer'
  ;;
--warning|--error)
  ;;
esac
EOF
chmod +x "${mock_bin}/zenity"

cat >"${mock_bin}/secret-tool" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

store_dir="${MOCK_KEYRING_DIR:?}"
log_file="${KEYRING_LOG:?}"
index_file="${store_dir}/index"
command_name="${1:-}"
shift || true
service=""
installation=""
kind=""
account=""
while (($#)); do
  case "$1" in
  service)
    service="$2"
    shift 2
    ;;
  installation)
    installation="$2"
    shift 2
    ;;
  kind)
    kind="$2"
    shift 2
    ;;
  account)
    account="$2"
    shift 2
    ;;
  *)
    shift
    ;;
  esac
done
printf '%s service=%s installation=%s kind=%s account=%s\n' \
  "${command_name}" "${service}" "${installation}" "${kind}" "${account}" >>"${log_file}"

if [[ "${MOCK_KEYRING_LOCKED:-false}" == "true" ]]; then
  exit 1
fi
item_key="$(printf '%s\n' "${service}" "${installation}" "${kind}" "${account}" | sha256sum | cut -d' ' -f1)"

case "${command_name}" in
store)
  secret_tmp="$(mktemp "${store_dir}/.secret.XXXXXX")"
  cat >"${secret_tmp}"
  chmod 600 "${secret_tmp}"
  mv -f -- "${secret_tmp}" "${store_dir}/${item_key}"
  index_tmp="$(mktemp "${store_dir}/.index.XXXXXX")"
  if [[ -f "${index_file}" ]]; then
    while IFS=$'\t' read -r old_key old_service old_installation old_kind old_account; do
      [[ "${old_key}" == "${item_key}" ]] && continue
      printf '%s\t%s\t%s\t%s\t%s\n' "${old_key}" "${old_service}" "${old_installation}" "${old_kind}" "${old_account}" >>"${index_tmp}"
    done <"${index_file}"
  fi
  printf '%s\t%s\t%s\t%s\t%s\n' "${item_key}" "${service}" "${installation}" "${kind}" "${account}" >>"${index_tmp}"
  mv -f -- "${index_tmp}" "${index_file}"
  ;;
lookup)
  [[ -f "${store_dir}/${item_key}" ]] || exit 1
  cat "${store_dir}/${item_key}"
  ;;
clear)
  [[ "${MOCK_KEYRING_CLEAR_FAIL:-false}" == "true" ]] && exit 1
  index_tmp="$(mktemp "${store_dir}/.index.XXXXXX")"
  if [[ -f "${index_file}" ]]; then
    while IFS=$'\t' read -r old_key old_service old_installation old_kind old_account; do
      match=true
      [[ "${old_service}" == "${service}" && "${old_installation}" == "${installation}" ]] || match=false
      [[ -z "${kind}" || "${old_kind}" == "${kind}" ]] || match=false
      [[ -z "${account}" || "${old_account}" == "${account}" ]] || match=false
      if [[ "${match}" == "true" ]]; then
        rm -f -- "${store_dir}/${old_key}"
      else
        printf '%s\t%s\t%s\t%s\t%s\n' "${old_key}" "${old_service}" "${old_installation}" "${old_kind}" "${old_account}" >>"${index_tmp}"
      fi
    done <"${index_file}"
  fi
  mv -f -- "${index_tmp}" "${index_file}"
  ;;
*)
  exit 2
  ;;
esac
EOF
chmod +x "${mock_bin}/secret-tool"

cat >"${mock_bin}/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

url=""
config=""
output=""
for ((i = 1; i <= $#; i++)); do
  arg="${!i}"
  [[ "${arg}" == http* ]] && url="${arg}"
  if [[ "${arg}" == "--config" ]]; then
    next=$((i + 1))
    config="${!next}"
  fi
  if [[ "${arg}" == "-o" ]]; then
    next=$((i + 1))
    output="${!next}"
  fi
done

if [[ -n "${config}" && -f "${config}" ]]; then
  while IFS= read -r config_line; do
    case "${config_line}" in
    url\ =\ \"*)
      url="${config_line#url = \"}"
      url="${url%\"}"
      ;;
    esac
  done <"${config}"
fi

printf '%s\n' "$*" >>"${CURL_ARG_LOG:?}"
old_token=false
new_token=false
if [[ -n "${config}" && -f "${config}" ]]; then
  grep -q 'old-token' "${config}" && old_token=true || true
  grep -q 'new-token' "${config}" && new_token=true || true
fi

body=""
code="200"
case "${url}" in
*'/api/auth/login')
  request_body="$(cat)"
  if [[ "${MOCK_BLOCK_LOGIN:-false}" == "true" ]]; then
    : >"${MOCK_BLOCK_MARKER:?}"
    while [[ ! -e "${MOCK_BLOCK_RELEASE:?}" ]]; do
      sleep 0.02
    done
  fi
  if [[ "${MOCK_LOGIN_NETWORK_FAIL:-false}" == "true" ]]; then
    exit 7
  elif [[ "${MOCK_LOGIN_SERVER_FAIL:-false}" == "true" ]]; then
    body='{"success":false,"message":"service unavailable"}'
    code="503"
  elif [[ "${MOCK_LOGIN_RATE_LIMIT:-false}" == "true" ]]; then
    body='{"success":false,"message":"rate limited"}'
    code="429"
  elif [[ "${MOCK_REJECT_SAVED:-false}" == "true" && "${request_body}" == *'saved-pass'* ]]; then
    body='{"success":false,"message":"invalid credentials"}'
    code="401"
  elif [[ "${MOCK_REJECT_MANUAL:-false}" == "true" ]]; then
    body='{"success":false,"message":"invalid credentials"}'
    code="401"
  else
    body="{\"success\":true,\"token\":\"${MOCK_LOGIN_TOKEN:-new-token}\"}"
  fi
  ;;
*'/api/launcher/games')
  if [[ "${MOCK_GAMES_NETWORK_FAIL:-false}" == "true" ]]; then
    exit 7
  elif [[ "${MOCK_GAMES_SERVER_FAIL:-false}" == "true" ]]; then
    body='{"success":false,"error":"manifest service unavailable"}'
    code="503"
  elif [[ "${MOCK_EXPIRE_OLD_TOKEN:-false}" == "true" && "${old_token}" == "true" ]]; then
    body='{"error":"expired"}'
    code="401"
  elif [[ "${MOCK_MANIFEST_KIND:-empty}" == "files" ]]; then
    body="{\"success\":true,\"data\":{\"games\":[{\"slug\":\"roguelike-prod\",\"realmlist\":\"realm\",\"files\":[{\"id\":\"1\",\"file_path_from_game_root\":\"Data/patch-a\",\"file_hash\":\"${MOCK_HASH}\",\"option_slug\":null},{\"id\":\"2\",\"file_path_from_game_root\":\"Data/patch-b\",\"file_hash\":\"${MOCK_HASH}\",\"option_slug\":null}]}]}}"
  else
    body='{"success":true,"data":{"games":[{"slug":"roguelike-prod","realmlist":"realm","files":[]}]}}'
  fi
  ;;
*'/api/server/status')
  body='{"success":true,"data":{"online":true,"serverName":"test"}}'
  ;;
*'/api/launcher/download?file_ids='*)
  if [[ "${MOCK_DOWNLOAD_401_OLD:-false}" == "true" && "${old_token}" == "true" ]] ||
    [[ "${MOCK_DOWNLOAD_401_NEW:-false}" == "true" && "${new_token}" == "true" ]]; then
    body='{"error":"expired"}'
    code="401"
  else
    body='{"url":"https://download.test/file"}'
  fi
  ;;
https://download.test/file)
  printf '%s' "${MOCK_DOWNLOAD_CONTENT:-payload}" >"${output}"
  exit 0
  ;;
*)
  printf 'Unexpected mocked curl URL: %s\n' "${url}" >&2
  exit 2
  ;;
esac

if [[ -n "${output}" ]]; then
  printf '%s' "${body}" >"${output}"
else
  printf '%s' "${body}"
fi
if [[ "$*" == *'%{http_code}'* ]]; then
  [[ -n "${output}" ]] || printf '\n'
  printf '%s' "${code}"
fi
EOF
chmod +x "${mock_bin}/curl"

new_client() {
  local name="$1"
  local client="${test_root}/${name}"
  mkdir -p "${client}"
  cp "${repo_root}/launcher.sh" "${client}/launcher.sh"
  chmod +x "${client}/launcher.sh"
  printf '%s' "${client}"
}

run_launcher() {
  local client="$1"
  shift
  XDG_RUNTIME_DIR="${MOCK_RUNTIME_DIR:-${runtime_dir}}" XDG_SESSION_TYPE=x11 GUI=true PATH="${mock_bin}:${PATH}" \
    MOCK_KEYRING_DIR="${state_dir}/keyring" KEYRING_LOG="${state_dir}/keyring.log" \
    ZENITY_LOG="${state_dir}/zenity.log" CURL_ARG_LOG="${state_dir}/curl.log" \
    remember_login="${remember_login:-}" \
    MOCK_USER="${MOCK_USER:-test-user}" MOCK_PASSWORD="${MOCK_PASSWORD:-manual-pass}" \
    MOCK_REMEMBER="${MOCK_REMEMBER:-false}" MOCK_LOGIN_TOKEN="${MOCK_LOGIN_TOKEN:-new-token}" \
    MOCK_MANIFEST_KIND="${MOCK_MANIFEST_KIND:-empty}" MOCK_HASH="${MOCK_HASH:-}" \
    MOCK_EXPIRE_OLD_TOKEN="${MOCK_EXPIRE_OLD_TOKEN:-false}" \
    MOCK_DOWNLOAD_401_OLD="${MOCK_DOWNLOAD_401_OLD:-false}" MOCK_DOWNLOAD_401_NEW="${MOCK_DOWNLOAD_401_NEW:-false}" \
    MOCK_REJECT_SAVED="${MOCK_REJECT_SAVED:-false}" MOCK_REJECT_MANUAL="${MOCK_REJECT_MANUAL:-false}" \
    MOCK_LOGIN_NETWORK_FAIL="${MOCK_LOGIN_NETWORK_FAIL:-false}" MOCK_LOGIN_SERVER_FAIL="${MOCK_LOGIN_SERVER_FAIL:-false}" \
    MOCK_LOGIN_RATE_LIMIT="${MOCK_LOGIN_RATE_LIMIT:-false}" \
    MOCK_GAMES_NETWORK_FAIL="${MOCK_GAMES_NETWORK_FAIL:-false}" MOCK_GAMES_SERVER_FAIL="${MOCK_GAMES_SERVER_FAIL:-false}" \
    MOCK_BLOCK_LOGIN="${MOCK_BLOCK_LOGIN:-false}" MOCK_BLOCK_MARKER="${MOCK_BLOCK_MARKER:-}" MOCK_BLOCK_RELEASE="${MOCK_BLOCK_RELEASE:-}" \
    "${client}/launcher.sh" "$@"
}

seed_saved_client() {
  local client="$1"
  MOCK_REMEMBER=true MOCK_PASSWORD=saved-pass run_launcher "${client}" --status >/dev/null
}

zenity_prompt_count() {
  grep -Ec '^--(entry|password)' "${state_dir}/zenity.log" || true
}

keyring_log_lines() {
  wc -l <"${state_dir}/keyring.log"
}

keyring_appended_count() {
  local start_line="$1"
  local pattern="$2"
  local appended

  appended="$(tail -n +$((start_line + 1)) "${state_dir}/keyring.log")"
  grep -Ec "${pattern}" <<<"${appended}" || true
}

assert_no_new_keyring_mutations() {
  local start_line="$1"

  [[ "$(keyring_appended_count "${start_line}" '^store ')" -eq 0 &&
  "$(keyring_appended_count "${start_line}" '^clear ')" -eq 0 ]]
}

installation_key_for_client() {
  local client="$1"

  printf '%s' "$(realpath -e "${client}")" | sha256sum | cut -d' ' -f1
}

mkdir -p "${state_dir}/keyring"
: >"${state_dir}/keyring.log"
: >"${state_dir}/zenity.log"
: >"${state_dir}/curl.log"

# Explicit opt-out: authenticate, cache only the token, and do not call keyring store.
client_optout="$(new_client optout)"
MOCK_REMEMBER=false run_launcher "${client_optout}" --status >/dev/null
[[ "$(<"${client_optout}/.updaterToken")" == "new-token" ]]
[[ "$(stat -c '%a' "${client_optout}/.updaterToken")" == "600" ]]
! grep -q '^store ' "${state_dir}/keyring.log"

# Explicit opt-in stores username and password only through secret-tool stdin.
client_saved="$(new_client saved)"
MOCK_REMEMBER=true MOCK_PASSWORD=saved-pass run_launcher "${client_saved}" --status >/dev/null
[[ -f "${state_dir}/keyring/index" ]]
saved_account="$(printf '%s' test-user | sha256sum | cut -d' ' -f1)"
[[ "$(grep -c "password" "${state_dir}/keyring/index")" -eq 1 ]]
[[ "$(<"${state_dir}/keyring/$(printf '%s\n' com.project-ebonhold.updater "$(realpath -e "${client_saved}")" password "${saved_account}" | sha256sum | cut -d' ' -f1)")" == "saved-pass" ]]
! grep -q 'manual-pass\|new-token' "${state_dir}/keyring.log"

# A valid cached token short-circuits both keyring lookup and login.
cp "${state_dir}/keyring.log" "${state_dir}/keyring.before-cache"
cp "${state_dir}/zenity.log" "${state_dir}/zenity.before-cache"
run_launcher "${client_saved}" --status >/dev/null
cmp -s "${state_dir}/keyring.log" "${state_dir}/keyring.before-cache"
cmp -s "${state_dir}/zenity.log" "${state_dir}/zenity.before-cache"

# Expiry uses saved credentials exactly once and writes a replacement token.
printf '%s' old-token >"${client_saved}/.updaterToken"
MOCK_EXPIRE_OLD_TOKEN=true run_launcher "${client_saved}" --status >/dev/null
[[ "$(<"${client_saved}/.updaterToken")" == "new-token" ]]

# Inherited remember_login=true cannot turn a normal opt-out into consent.
client_inherited="$(new_client inherited-consent)"
keyring_before_inherited="$(keyring_log_lines)"
login_before_inherited="$(grep -c '/api/auth/login' "${state_dir}/curl.log" || true)"
remember_login=true MOCK_REMEMBER=false run_launcher "${client_inherited}" --status >/dev/null
login_after_inherited="$(grep -c '/api/auth/login' "${state_dir}/curl.log" || true)"
[[ ${login_after_inherited} -gt ${login_before_inherited} ]]
[[ "$(keyring_appended_count "${keyring_before_inherited}" '^store ')" -eq 0 ]]
[[ "$(keyring_appended_count "${keyring_before_inherited}" '^clear ')" -eq 1 ]]

# A writable opt-in fixture is a negative control: the mutation assertion must catch its stores.
client_mutation_control="$(new_client mutation-control)"
keyring_before_control="$(keyring_log_lines)"
MOCK_REMEMBER=true MOCK_PASSWORD=control-pass run_launcher "${client_mutation_control}" --status >/dev/null
if assert_no_new_keyring_mutations "${keyring_before_control}"; then
  printf 'Keyring mutation negative control unexpectedly passed.\n' >&2
  exit 1
fi
[[ "$(keyring_appended_count "${keyring_before_control}" '^store ')" -gt 0 ]]

# A missing optional helper does not block manual login and reports the fallback.
client_missing="$(new_client missing-keyring)"
mock_path_without_secret="${test_root}/bin-no-secret"
mkdir -p "${mock_path_without_secret}"
for utility in env bash basename dirname realpath sha256sum cut mktemp chmod stat mv rm cat grep jq flock mkdir sleep; do
  ln -s "$(command -v "${utility}")" "${mock_path_without_secret}/${utility}"
done
ln -s "${mock_bin}/curl" "${mock_path_without_secret}/curl"
ln -s "${mock_bin}/zenity" "${mock_path_without_secret}/zenity"
missing_output="$(XDG_RUNTIME_DIR= TMPDIR="${fallback_runtime_dir}" XDG_SESSION_TYPE=x11 GUI=true PATH="${mock_path_without_secret}" \
  ZENITY_LOG="${state_dir}/zenity.log" CURL_ARG_LOG="${state_dir}/curl.log" MOCK_REMEMBER=true \
  "${client_missing}/launcher.sh" --status 2>&1)"
[[ "${missing_output}" == *"secret-tool desktop-keyring helper is unavailable"* ]]
missing_curl_before="${state_dir}/curl.before-missing-forget"
cp "${state_dir}/curl.log" "${missing_curl_before}"
missing_forget_output="$(XDG_RUNTIME_DIR= TMPDIR="${fallback_runtime_dir}" XDG_SESSION_TYPE=x11 GUI=true PATH="${mock_path_without_secret}" \
  ZENITY_LOG="${state_dir}/zenity.log" CURL_ARG_LOG="${state_dir}/curl.log" \
  "${client_missing}/launcher.sh" --forget-login 2>&1 || true)"
[[ ! -e "${client_missing}/.updaterToken" ]]
[[ "${missing_forget_output}" == *"secret-tool is unavailable"* ]]
cmp -s "${state_dir}/curl.log" "${missing_curl_before}"

# Locked saved credentials fall back to one manual login attempt.
client_locked="$(new_client locked-keyring)"
printf '%s' old-token >"${client_locked}/.updaterToken"
locked_output="$(MOCK_KEYRING_LOCKED=true MOCK_EXPIRE_OLD_TOKEN=true run_launcher "${client_locked}" --status 2>&1)"
[[ "${locked_output}" == *"desktop-keyring login is unavailable or locked"* ]]

# Saved account A is rejected, manual account B succeeds with Remember OFF, and A is cleared.
client_rejected="$(new_client rejected-saved)"
MOCK_USER=account-a MOCK_PASSWORD=saved-pass MOCK_REMEMBER=true run_launcher "${client_rejected}" --status >/dev/null
printf '%s' old-token >"${client_rejected}/.updaterToken"
client_remembered="$(new_client remembered-other-installation)"
MOCK_USER=remembered-user MOCK_PASSWORD=remembered-pass MOCK_REMEMBER=true run_launcher "${client_remembered}" --status >/dev/null
rejected_installation="$(realpath -e "${client_rejected}")"
rejected_keyring_before="$(keyring_log_lines)"
zenity_before_reject="$(zenity_prompt_count)"
MOCK_USER=account-b MOCK_PASSWORD=manual-pass MOCK_REMEMBER=false MOCK_EXPIRE_OLD_TOKEN=true MOCK_REJECT_SAVED=true \
  run_launcher "${client_rejected}" --status >/dev/null
zenity_after_reject="$(zenity_prompt_count)"
((zenity_after_reject > zenity_before_reject))
[[ "$(keyring_appended_count "${rejected_keyring_before}" '^store ')" -eq 0 ]]
[[ "$(keyring_appended_count "${rejected_keyring_before}" "^clear .*installation=${rejected_installation}")" -eq 1 ]]
printf '%s' old-token >"${client_rejected}/.updaterToken"
zenity_before_rejected_expiry="$(zenity_prompt_count)"
MOCK_USER=account-b MOCK_PASSWORD=manual-pass MOCK_EXPIRE_OLD_TOKEN=true run_launcher "${client_rejected}" --status >/dev/null
(($(zenity_prompt_count) > zenity_before_rejected_expiry))
remembered_zenity_before="$(zenity_prompt_count)"
printf '%s' old-token >"${client_remembered}/.updaterToken"
MOCK_USER=remembered-user MOCK_PASSWORD=remembered-pass MOCK_EXPIRE_OLD_TOKEN=true run_launcher "${client_remembered}" --status >/dev/null
[[ "$(zenity_prompt_count)" -eq "${remembered_zenity_before}" ]]

# Transient login failures stop after the single saved attempt and preserve state.
client_login_fail="$(new_client login-failure)"
seed_saved_client "${client_login_fail}"
printf '%s' old-token >"${client_login_fail}/.updaterToken"
for transient in server rate; do
  login_before="$(grep -c '/api/auth/login' "${state_dir}/curl.log" || true)"
  zenity_before="$(zenity_prompt_count)"
  if [[ "${transient}" == server ]]; then
    if MOCK_EXPIRE_OLD_TOKEN=true MOCK_LOGIN_SERVER_FAIL=true run_launcher "${client_login_fail}" --status >/dev/null 2>&1; then
      printf 'Login HTTP 503 unexpectedly succeeded.\n' >&2
      exit 1
    fi
  else
    if MOCK_EXPIRE_OLD_TOKEN=true MOCK_LOGIN_RATE_LIMIT=true run_launcher "${client_login_fail}" --status >/dev/null 2>&1; then
      printf 'Login HTTP 429 unexpectedly succeeded.\n' >&2
      exit 1
    fi
  fi
  login_after="$(grep -c '/api/auth/login' "${state_dir}/curl.log" || true)"
  [[ $((login_after - login_before)) -eq 1 ]]
  [[ "$(zenity_prompt_count)" -eq "${zenity_before}" ]]
  [[ "$(<"${client_login_fail}/.updaterToken")" == old-token ]]
done
login_before="$(grep -c '/api/auth/login' "${state_dir}/curl.log" || true)"
if MOCK_EXPIRE_OLD_TOKEN=true MOCK_LOGIN_NETWORK_FAIL=true run_launcher "${client_login_fail}" --status >/dev/null 2>&1; then
  printf 'Login network failure unexpectedly succeeded.\n' >&2
  exit 1
fi
login_after="$(grep -c '/api/auth/login' "${state_dir}/curl.log" || true)"
[[ $((login_after - login_before)) -eq 1 ]]
[[ "$(<"${client_login_fail}/.updaterToken")" == old-token ]]

# A token write failure is reported without replacing an unsafe destination.
client_write_fail="$(new_client token-write-failure)"
mkdir "${client_write_fail}/.updaterToken"
if run_launcher "${client_write_fail}" --status >/dev/null 2>&1; then
  printf 'Unsafe token destination unexpectedly succeeded.\n' >&2
  exit 1
fi
[[ -d "${client_write_fail}/.updaterToken" ]]

# Read-only manual authentication never stores token/keyring state, even with inherited consent.
client_verify="$(new_client readonly-verify)"
printf payload >"${client_verify}/payload"
readonly_hash="$(md5sum "${client_verify}/payload" | cut -d' ' -f1)"
readonly_b64="$(printf '%s' "${readonly_hash}" | xxd -r -p | base64 -w0)"
keyring_before_readonly="$(keyring_log_lines)"
login_before_readonly="$(grep -c '/api/auth/login' "${state_dir}/curl.log" || true)"
if remember_login=true MOCK_MANIFEST_KIND=files MOCK_HASH="${readonly_b64}" run_launcher "${client_verify}" --verify --quick >/dev/null 2>&1; then
  printf 'Verify unexpectedly succeeded with missing files.\n' >&2
  exit 1
fi
login_after_readonly="$(grep -c '/api/auth/login' "${state_dir}/curl.log" || true)"
[[ ${login_after_readonly} -gt ${login_before_readonly} ]]
[[ ! -e "${client_verify}/.updaterToken" && ! -e "${client_verify}/Data/patch-a" ]]
assert_no_new_keyring_mutations "${keyring_before_readonly}"

client_dry="$(new_client readonly-dry-run)"
printf payload >"${client_dry}/payload"
dry_hash="$(md5sum "${client_dry}/payload" | cut -d' ' -f1)"
dry_b64="$(printf '%s' "${dry_hash}" | xxd -r -p | base64 -w0)"
keyring_before_dry="$(keyring_log_lines)"
login_before_dry="$(grep -c '/api/auth/login' "${state_dir}/curl.log" || true)"
remember_login=true MOCK_MANIFEST_KIND=files MOCK_HASH="${dry_b64}" run_launcher "${client_dry}" --dry-run --quick >/dev/null
login_after_dry="$(grep -c '/api/auth/login' "${state_dir}/curl.log" || true)"
[[ ${login_after_dry} -gt ${login_before_dry} ]]
[[ ! -e "${client_dry}/.updaterToken" && ! -e "${client_dry}/Data/patch-a" ]]
assert_no_new_keyring_mutations "${keyring_before_dry}"

# Parallel 401s are renewed once by the parent; workers never prompt or race token writes.
client_parallel="$(new_client parallel-reauth)"
seed_saved_client "${client_parallel}"
printf '%s' old-token >"${client_parallel}/.updaterToken"
printf payload >"${client_parallel}/payload"
parallel_hash="$(md5sum "${client_parallel}/payload" | cut -d' ' -f1)"
parallel_b64="$(printf '%s' "${parallel_hash}" | xxd -r -p | base64 -w0)"
login_count_before="$(grep -c '/api/auth/login' "${state_dir}/curl.log" || true)"
MOCK_MANIFEST_KIND=files MOCK_HASH="${parallel_b64}" MOCK_DOWNLOAD_401_OLD=true run_launcher "${client_parallel}" --quiet >/dev/null
login_count_after="$(grep -c '/api/auth/login' "${state_dir}/curl.log" || true)"
[[ $((login_count_after - login_count_before)) -eq 1 ]]
[[ "$(<"${client_parallel}/Data/patch-a")" == payload ]]
[[ "$(<"${client_parallel}/Data/patch-b")" == payload ]]

# A replacement token receiving another 401 is bounded to one renewal.
client_repeat="$(new_client repeated-401)"
seed_saved_client "${client_repeat}"
printf '%s' old-token >"${client_repeat}/.updaterToken"
repeat_hash="$(md5sum "${client_repeat}/payload" 2>/dev/null | cut -d' ' -f1 || true)"
if [[ -z "${repeat_hash}" ]]; then
  printf payload >"${client_repeat}/payload"
  repeat_hash="$(md5sum "${client_repeat}/payload" | cut -d' ' -f1)"
fi
repeat_b64="$(printf '%s' "${repeat_hash}" | xxd -r -p | base64 -w0)"
login_count_before="$(grep -c '/api/auth/login' "${state_dir}/curl.log" || true)"
if MOCK_MANIFEST_KIND=files MOCK_HASH="${repeat_b64}" MOCK_DOWNLOAD_401_OLD=true MOCK_DOWNLOAD_401_NEW=true run_launcher "${client_repeat}" --quiet >/dev/null 2>&1; then
  printf 'Repeated new-token 401 unexpectedly succeeded.\n' >&2
  exit 1
fi
login_count_after="$(grep -c '/api/auth/login' "${state_dir}/curl.log" || true)"
[[ $((login_count_after - login_count_before)) -eq 1 ]]

# --relogin opt-out clears prior credentials; opt-in can configure them again.
client_relogin="$(new_client relogin)"
seed_saved_client "${client_relogin}"
remember_login=true MOCK_REMEMBER=false MOCK_PASSWORD=manual-pass run_launcher "${client_relogin}" --relogin --status >/dev/null
printf '%s' old-token >"${client_relogin}/.updaterToken"
zenity_before_relogin="$(zenity_prompt_count)"
MOCK_EXPIRE_OLD_TOKEN=true run_launcher "${client_relogin}" --status >/dev/null
(($(zenity_prompt_count) > zenity_before_relogin))
MOCK_REMEMBER=true MOCK_PASSWORD=new-save run_launcher "${client_relogin}" --relogin --status >/dev/null
printf '%s' old-token >"${client_relogin}/.updaterToken"
zenity_before_saved_relogin="$(zenity_prompt_count)"
MOCK_EXPIRE_OLD_TOKEN=true run_launcher "${client_relogin}" --status >/dev/null
[[ "$(zenity_prompt_count)" -eq "${zenity_before_saved_relogin}" ]]

# Terminal Remember me defaults to No.
client_terminal="$(new_client terminal-default-no)"
terminal_before="$(keyring_log_lines)"
terminal_output="$(printf 'terminal-user\nterminal-pass\n\n' |
  XDG_RUNTIME_DIR="${runtime_dir}" XDG_SESSION_TYPE= GUI=false PATH="${mock_bin}:${PATH}" \
    MOCK_KEYRING_DIR="${state_dir}/keyring" KEYRING_LOG="${state_dir}/keyring.log" \
    ZENITY_LOG="${state_dir}/zenity.log" CURL_ARG_LOG="${state_dir}/curl.log" \
    script -q -e -c "${client_terminal}/launcher.sh --status" /dev/null 2>&1)"
[[ "${terminal_output}" == *"[y/N]"* ]]
[[ "$(<"${client_terminal}/.updaterToken")" == "new-token" ]]
[[ "$(keyring_appended_count "${terminal_before}" '^store ')" -eq 0 ]]
[[ "$(keyring_appended_count "${terminal_before}" '^clear ')" -eq 1 ]]

# A symlink or FIFO lock fixture is rejected without truncation or blocking.
client_lock="$(new_client lock-fixture)"
lock_dir="${runtime_dir}/ebonhold-updater"
lock_file="${lock_dir}/auth-$(installation_key_for_client "${client_lock}").lock"
mkdir -p "${lock_dir}"
chmod 700 "${lock_dir}"
rm -f "${lock_file}"
printf intact >"${test_root}/lock-victim"
ln -s "${test_root}/lock-victim" "${lock_file}"
if MOCK_RUNTIME_DIR="${runtime_dir}" run_launcher "${client_lock}" --status >/dev/null 2>&1; then
  printf 'Symlink lock unexpectedly succeeded.\n' >&2
  exit 1
fi
[[ "$(<"${test_root}/lock-victim")" == intact ]]
rm -f "${lock_file}"
mkfifo "${lock_file}"
set +e
timeout 10 env XDG_RUNTIME_DIR="${runtime_dir}" XDG_SESSION_TYPE=x11 GUI=true PATH="${mock_bin}:${PATH}" \
  MOCK_KEYRING_DIR="${state_dir}/keyring" KEYRING_LOG="${state_dir}/keyring.log" \
  ZENITY_LOG="${state_dir}/zenity.log" CURL_ARG_LOG="${state_dir}/curl.log" \
  "${client_lock}/launcher.sh" --status >/dev/null 2>&1
fifo_rc=$?
set -e
[[ ${fifo_rc} -ne 0 && ${fifo_rc} -ne 124 ]]
rm -f "${lock_file}"

# An unrelated installation with a valid token must not wait behind a blocked login prompt.
client_blocked="$(new_client blocked-installation)"
client_unrelated="$(new_client unrelated-cached-token)"
printf '%s' new-token >"${client_unrelated}/.updaterToken"
blocked_marker="${test_root}/blocked-installation-login"
blocked_release="${test_root}/release-blocked-installation"
MOCK_RUNTIME_DIR="${runtime_dir}" MOCK_BLOCK_LOGIN=true MOCK_BLOCK_MARKER="${blocked_marker}" MOCK_BLOCK_RELEASE="${blocked_release}" \
  run_launcher "${client_blocked}" --status >"${state_dir}/blocked-installation.out" 2>&1 &
blocked_pid=$!
for ((i = 0; i < 200; i++)); do
  [[ -e "${blocked_marker}" ]] && break
  sleep 0.02
done
[[ -e "${blocked_marker}" ]]
MOCK_RUNTIME_DIR="${runtime_dir}" run_launcher "${client_unrelated}" --status >"${state_dir}/unrelated-cached.out" 2>&1 &
unrelated_pid=$!
unrelated_finished=false
for ((i = 0; i < 150; i++)); do
  unrelated_state="$(ps -o stat= -p "${unrelated_pid}" 2>/dev/null || true)"
  if [[ -z "${unrelated_state}" || "${unrelated_state}" == Z* ]]; then
    unrelated_finished=true
    break
  fi
  sleep 0.02
done
[[ "${unrelated_finished}" == true ]]
set +e
wait "${unrelated_pid}"
unrelated_rc=$?
touch "${blocked_release}"
wait "${blocked_pid}"
blocked_rc=$?
set -e
[[ ${unrelated_rc} -eq 0 && ${blocked_rc} -eq 0 ]]

# A concurrent login cannot win the lock after successful forget; token stays absent.
client_concurrent="$(new_client concurrent-forget)"
marker="${test_root}/login-blocked"
release="${test_root}/release-login"
MOCK_RUNTIME_DIR="${runtime_dir}" MOCK_BLOCK_LOGIN=true MOCK_BLOCK_MARKER="${marker}" MOCK_BLOCK_RELEASE="${release}" \
  run_launcher "${client_concurrent}" --status >"${state_dir}/blocked-login.out" 2>&1 &
login_pid=$!
for ((i = 0; i < 200; i++)); do
  [[ -e "${marker}" ]] && break
  sleep 0.02
done
[[ -e "${marker}" ]]
MOCK_RUNTIME_DIR="${runtime_dir}" MOCK_BLOCK_LOGIN=false run_launcher "${client_concurrent}" --forget-login >"${state_dir}/concurrent-forget.out" 2>&1 &
forget_pid=$!
sleep 0.2
touch "${release}"
set +e
wait "${login_pid}"
login_rc=$?
set -e
[[ ${login_rc} -eq 0 ]]
for ((i = 0; i < 300; i++)); do
  process_state="$(ps -o stat= -p "${forget_pid}" 2>/dev/null || true)"
  [[ -z "${process_state}" || "${process_state}" == Z* ]] && break
  sleep 0.02
done
if kill -0 "${forget_pid}" 2>/dev/null; then
  kill "${forget_pid}" 2>/dev/null || true
  wait "${forget_pid}" 2>/dev/null || true
  printf 'Concurrent forget did not finish.\n' >&2
  exit 1
fi
set +e
wait "${forget_pid}"
forget_rc=$?
set -e
[[ ${forget_rc} -eq 0 ]]
[[ ! -e "${client_concurrent}/.updaterToken" ]]

# Forget is local-only, scoped by installation, and reports keyring-clear failure accurately.
client_forget="$(new_client forget)"
client_other="$(new_client other-installation)"
seed_saved_client "${client_forget}"
seed_saved_client "${client_other}"
: >"${state_dir}/curl.before-forget"
cp "${state_dir}/curl.log" "${state_dir}/curl.before-forget"
run_launcher "${client_forget}" --forget-login >/dev/null
cmp -s "${state_dir}/curl.log" "${state_dir}/curl.before-forget"
[[ ! -e "${client_forget}/.updaterToken" ]]
printf '%s' old-token >"${client_other}/.updaterToken"
other_zenity_before="$(zenity_prompt_count)"
MOCK_EXPIRE_OLD_TOKEN=true run_launcher "${client_other}" --status >/dev/null
[[ "$(zenity_prompt_count)" -eq "${other_zenity_before}" ]]
printf token >"${client_forget}/.updaterToken"
if MOCK_KEYRING_CLEAR_FAIL=true run_launcher "${client_forget}" --forget-login >"${state_dir}/forget-fail.out" 2>&1; then
  printf 'Keyring clear failure unexpectedly succeeded.\n' >&2
  exit 1
fi
[[ ! -e "${client_forget}/.updaterToken" ]]
grep -q 'Could not clear desktop-keyring credentials' "${state_dir}/forget-fail.out"

# The debug stream and curl argv log never contain password or bearer-token values.
client_debug="$(new_client debug-secrets)"
debug_output="$(MOCK_REMEMBER=false run_launcher "${client_debug}" --debug --status 2>&1)"
! grep -q 'manual-pass\|new-token\|saved-pass' <<<"${debug_output}"
! grep -q 'manual-pass\|new-token\|saved-pass' "${state_dir}/curl.log"

printf 'login/keyring mocked tests passed\n'
