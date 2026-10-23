#!/usr/bin/env sh
# Development wrapper: builds erun from this checkout, then runs it. The
# contribute shim forwards every `erun` call here.

set -eu

ORIGINAL_DIR=$(pwd)
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd -P)
# ERUN_DEV_BIN_DIR moves the binary out of the checkout, for callers that must
# not write into it (a host orchestrator's read-only review directory).
BIN_DIR=${ERUN_DEV_BIN_DIR:-$SCRIPT_DIR/bin}

BUILD_VERSION=$(tr -d '[:space:]' 2>/dev/null < "$SCRIPT_DIR/../erun-devops/VERSION" || true)
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
# The commit names what the binary contains, so an uncommitted change says so.
BUILD_COMMIT=$(git -C "$SCRIPT_DIR" rev-parse --short=12 HEAD 2>/dev/null || true)
if [ -n "$BUILD_COMMIT" ] && [ -n "$(git -C "$SCRIPT_DIR" status --porcelain 2>/dev/null)" ]; then
	BUILD_COMMIT="${BUILD_COMMIT}-dirty"
fi

cd "$SCRIPT_DIR"
mkdir -p "$BIN_DIR"
go build \
	-ldflags "-X github.com/sophium/erun/cmd.buildVersion=${BUILD_VERSION:-dev} -X github.com/sophium/erun/cmd.buildCommit=${BUILD_COMMIT} -X github.com/sophium/erun/cmd.buildDate=${BUILD_DATE}" \
	-o "$BIN_DIR/erun" \
	./

cd "$ORIGINAL_DIR"
exec "$BIN_DIR/erun" "$@"
