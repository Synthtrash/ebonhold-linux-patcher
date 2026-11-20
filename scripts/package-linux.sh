#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
dist_dir=${1:-dist}
version=${2:-}
if [[ -z $version ]]; then
  if revision=$(git -C "$repo_root" rev-parse --short=12 HEAD 2>/dev/null); then
    version="commit-$revision"
  else
    version=dev
  fi
fi
if [[ ! $version =~ ^[a-zA-Z0-9][a-zA-Z0-9.+-]*$ ]]; then
  printf 'Invalid launcher version: %s\n' "$version" >&2
  exit 2
fi
mkdir -p -- "$dist_dir"
dist_dir=$(cd -- "$dist_dir" && pwd)

cd "$repo_root"
export CGO_ENABLED=1 GOOS=linux GOARCH=amd64
staging=$(mktemp -d)
trap 'rm -rf -- "$staging"' EXIT
build_tags=(no_emoji no_emoji,wayland)
ldflags="-s -w -buildid= -X github.com/synthtrash/ebonhold-linux-patcher/internal/launcher.buildVersion=$version"

notice() {
  printf '=== %s ===\n\n' "$1"
  cat -- "$2"
  printf '\n\n'
}

# Include licenses for the union of packages compiled into the two executables.
modules=$(
  for tags in "${build_tags[@]}"; do
    go list -tags "$tags" -deps -f '{{with .Module}}{{if not .Main}}{{.Path}}{{"\t"}}{{.Dir}}{{end}}{{end}}' ./cmd/ebonhold-launcher || exit
  done | LC_ALL=C sort -u
)
go_root=$(go env GOROOT)
go_license=$go_root/LICENSE
if [[ ! -f $go_license && $go_root == /usr/lib/go ]]; then
  go_license=/usr/share/licenses/go/LICENSE
fi
{
  notice Go/LICENSE "$go_license"
  if [[ -f $go_root/PATENTS ]]; then
    notice Go/PATENTS "$go_root/PATENTS"
  fi
  for file in "$go_root"/src/vendor/golang.org/x/*/{LICENSE,PATENTS}; do
    [[ -f $file ]] || continue
    notice "Go/${file#"$go_root/src/"}" "$file"
  done
  while IFS=$'\t' read -r module dir; do
    [[ -n $module ]] || continue
    # Also collect nested notices for assets such as fonts and bundled C code.
    files=$(find "$dir" -type d -name testdata -prune -o -type f \
      \( -iname 'licen[cs]e*' -o -iname 'copying*' -o -iname 'notice*' -o -iname 'copyright*' -o -iname patents -o -iname 'ofl*' \) \
      -printf '%P\n' | LC_ALL=C sort)
    if [[ -z $files ]]; then
      printf 'No license notice found for %s\n' "$module" >&2
      exit 1
    fi
    while IFS= read -r file; do
      notice "$module/$file" "$dir/$file"
    done <<< "$files"
  done <<< "$modules"
} > "$staging/THIRD-PARTY-NOTICES.txt"

for tags in "${build_tags[@]}"; do
  suffix=
  if [[ $tags == *,wayland ]]; then
    suffix=-wayland
  fi

  binary="ebonhold-launcher-linux-amd64${suffix}"
  archive="${binary}.tar.gz"
  go build -trimpath -buildvcs=false -tags "$tags" -ldflags="$ldflags" -o "$dist_dir/$binary" ./cmd/ebonhold-launcher
  tar --sort=name --mtime='@0' --owner=0 --group=0 --numeric-owner \
    -C "$dist_dir" -cf - "$binary" -C "$repo_root" LICENSE \
    -C "$staging" THIRD-PARTY-NOTICES.txt | gzip -n > "$dist_dir/$archive"
  (cd "$dist_dir" && sha256sum "$archive" > "$archive.sha256" && sha256sum --check "$archive.sha256")
  expected=$(printf '%s\n' "$binary" LICENSE THIRD-PARTY-NOTICES.txt)
  [[ $(tar -tzf "$dist_dir/$archive") == "$expected" ]]
done
