#!/usr/bin/env sh

set -eu

ORIGINAL_DIR=$(pwd)
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
TARGET="$SCRIPT_DIR/bin/erun-app"

"$SCRIPT_DIR/build.sh" "$TARGET"

cd "$ORIGINAL_DIR"

exec "$TARGET" "$@"
