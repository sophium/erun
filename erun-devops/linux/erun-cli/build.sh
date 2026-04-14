#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$script_dir/../../.." && pwd)
version=${ERUN_BUILD_VERSION:-}

"$repo_root/packaging/apt/build-deb.sh" "$version" "$script_dir/dist"
