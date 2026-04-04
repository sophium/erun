#!/usr/bin/env sh

set -eu

ORIGINAL_DIR=$(pwd)
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
TARGET="$SCRIPT_DIR/bin/erun"

cd "$SCRIPT_DIR"

mkdir -p bin

go build -o "$TARGET" ./

cd "$ORIGINAL_DIR"

exec "$TARGET" "$@"
