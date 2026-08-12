#!/usr/bin/env bash
set -euo pipefail

repo_root="$(dirname "$(dirname "$(realpath "$0")")")"
test_root="$(mktemp -d)"
mock_bin="${test_root}/bin"
trap 'rm -rf "${test_root}"' EXIT
mkdir -p "${mock_bin}"

system_locale_cmd="$(command -v locale)"

format_bytes_definition="$(awk '
  /^format_bytes\(\) \{$/ { in_fn = 1; depth = 0; }
  in_fn {
    print
    for (i = 1; i <= length($0); i++) {
      ch = substr($0, i, 1)
      if (ch == "{") depth++
      if (ch == "}") depth--
    }
    if (depth == 0 && $0 !~ /^format_bytes\(\) \{$/) exit
  }
' "${repo_root}/launcher.sh")"

if [[ -z "${format_bytes_definition}" ]]; then
  echo "Unable to extract format_bytes from launcher.sh" >&2
  exit 1
fi

eval "${format_bytes_definition}"

# Mock locale output to deterministically force a comma decimal separator.
cat >"${mock_bin}/locale" <<'EOF'
#!/usr/bin/env bash
if [[ "${1}" == "decimal_point" ]]; then
  printf '%s\n' ","
  exit 0
fi
printf '\n'
EOF
chmod +x "${mock_bin}/locale"

comma_output="$(PATH="${mock_bin}:${PATH}" format_bytes 1572864)"
[[ "${comma_output}" == "1,50 MB" ]]

# Baseline C-locale formatting should remain dot-separated.
[[ "$(LC_ALL=C format_bytes 1536)" == "1.50 KB" ]]

# Optional real-locale coverage when available.
if [[ -n "${system_locale_cmd}" ]]; then
  real_decimal_locale="$(${system_locale_cmd} -a 2>/dev/null | awk '/^de_DE(\.|$)/ { print; exit }')"
  if [[ -n "${real_decimal_locale}" ]]; then
    real_decimal_point="$(LC_ALL="${real_decimal_locale}" "${system_locale_cmd}" decimal_point 2>/dev/null || true)"
    if [[ "${real_decimal_point}" == "," ]]; then
      [[ "$(LC_ALL="${real_decimal_locale}" format_bytes 1572864)" == "1,50 MB" ]]
    fi
  fi
fi

echo "format_bytes locale regression test passed"
