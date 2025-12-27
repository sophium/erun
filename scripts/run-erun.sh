#!/usr/bin/env bash
set -euo pipefail

if ! command -v go >/dev/null 2>&1; then
  echo "error: go toolchain not found in PATH" >&2
  exit 1
fi

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/.." && pwd)
CLI_DIR="$REPO_ROOT/erun-cli"
BIN_DIR="$REPO_ROOT/bin"
TARGET="$BIN_DIR/erun"

mkdir -p "$BIN_DIR"

cd "$CLI_DIR"

go build -o "$TARGET" .

exec "$TARGET" "$@"
