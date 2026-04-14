#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$script_dir/../.." && pwd)
default_version_file="$repo_root/erun-devops/VERSION"

version=${1:-}
if [[ -z "$version" ]]; then
  if [[ ! -f "$default_version_file" ]]; then
    echo "missing version file: $default_version_file" >&2
    exit 1
  fi
  version=$(tr -d '[:space:]' < "$default_version_file")
fi

if [[ -z "$version" ]]; then
  echo "version must not be empty" >&2
  exit 1
fi

output_dir=${2:-"$script_dir/dist"}
maintainer=${ERUN_DEB_MAINTAINER:-"ERun <erun@local>"}

arch_hint=$(uname -m)
if command -v dpkg >/dev/null 2>&1; then
  arch_hint=$(dpkg --print-architecture)
fi

case "$arch_hint" in
amd64 | x86_64)
  deb_arch=amd64
  ;;
arm64 | aarch64)
  deb_arch=arm64
  ;;
*)
  echo "unsupported architecture: $arch_hint" >&2
  exit 1
  ;;
esac

staging_dir=$(mktemp -d)
trap 'rm -rf "$staging_dir"' EXIT

package_root="$staging_dir/erun_${version}_${deb_arch}"
mkdir -p "$package_root/DEBIAN" "$package_root/usr/bin" "$package_root/usr/share/doc/erun"

cli_ldflags="-s -w -X github.com/sophium/erun/cmd.buildVersion=$version"
mcp_ldflags="-s -w -X github.com/sophium/erun/erun-mcp.buildVersion=$version"

(
  cd "$repo_root/erun-cli"
  CGO_ENABLED=0 go build -trimpath -ldflags "$cli_ldflags" -o "$package_root/usr/bin/erun" .
)

(
  cd "$repo_root/erun-mcp"
  CGO_ENABLED=0 go build -trimpath -ldflags "$mcp_ldflags" -o "$package_root/usr/bin/emcp" ./cmd/emcp
)

cat >"$package_root/DEBIAN/control" <<EOF
Package: erun
Version: $version
Section: utils
Priority: optional
Architecture: $deb_arch
Maintainer: $maintainer
Homepage: https://github.com/sophium/erun
Description: Multi-tenant multi-environment deployment and management tool
 ERun ships the erun CLI together with the emcp executable.
EOF

install -m 0644 "$repo_root/LICENSE" "$package_root/usr/share/doc/erun/copyright"

mkdir -p "$output_dir"
output_path="$output_dir/erun_${version}_${deb_arch}.deb"
dpkg-deb --build --root-owner-group "$package_root" "$output_path" >/dev/null

printf '%s\n' "$output_path"
