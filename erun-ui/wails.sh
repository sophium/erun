#!/usr/bin/env sh

# wails.sh — rebuild the erun CLI binary, then launch `wails dev`
# instead of the production desktop binary. Mirrors run.sh's CLI
# rebuild step so PTY tabs spawn the freshly-built `erun`, but stops
# short of building the desktop bundle: wails dev compiles + runs in
# place with frontend hot-reload.

set -eu

ORIGINAL_DIR=$(pwd)
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
CLI_DIR="$SCRIPT_DIR/../erun-cli"
CLI_BIN="$CLI_DIR/bin/erun"
VERSION_FILE="$SCRIPT_DIR/../erun-devops/VERSION"
WAILS_BIN="${WAILS_BIN:-$(go env GOPATH)/bin/wails}"
YARN_BIN="${YARN_BIN:-}"
NODE_BIN_DIR="${NODE_BIN_DIR:-}"

mkdir -p "$CLI_DIR/bin"

BUILD_VERSION=dev
if [ -f "$VERSION_FILE" ]; then
	BUILD_VERSION=$(tr -d '\n' < "$VERSION_FILE")
fi

BUILD_COMMIT=
BUILD_DATE=
if git -C "$SCRIPT_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
	BUILD_COMMIT=$(git -C "$SCRIPT_DIR" rev-parse --short=12 HEAD)
	BUILD_DATE=$(git -C "$SCRIPT_DIR" show -s --format=%cI HEAD)
fi

printf '>> rebuilding erun CLI (%s)... ' "$BUILD_VERSION" >&2
build_started_at=$(date +%s)
(
	cd "$CLI_DIR"
	go build \
		-ldflags "-X github.com/sophium/erun/cmd.buildVersion=${BUILD_VERSION} -X github.com/sophium/erun/cmd.buildCommit=${BUILD_COMMIT} -X github.com/sophium/erun/cmd.buildDate=${BUILD_DATE}" \
		-o "$CLI_BIN" \
		./
)
build_finished_at=$(date +%s)
printf 'ok (%ss) -> %s\n' "$((build_finished_at - build_started_at))" "$CLI_BIN" >&2

# Resolve the wails CLI, installing the module-pinned version if not
# already on disk (same fallback path build.sh uses).
if [ ! -x "$WAILS_BIN" ]; then
	WAILS_VERSION=$(cd "$SCRIPT_DIR" && go list -m -f '{{.Version}}' github.com/wailsapp/wails/v2)
	WAILS_TMP="${TMPDIR:-/tmp}/erun-wails-$$"
	mkdir -p "$WAILS_TMP"
	(cd "$SCRIPT_DIR" && GOBIN="$WAILS_TMP" go install "github.com/wailsapp/wails/v2/cmd/wails@$WAILS_VERSION")
	WAILS_BIN="$WAILS_TMP/wails"
fi

# Ensure node + yarn are visible to wails dev's frontend devserver.
if [ -z "$YARN_BIN" ]; then
	YARN_BIN=$(command -v yarn)
fi
if [ -z "$NODE_BIN_DIR" ]; then
	if [ -x /opt/homebrew/opt/node@24/bin/node ]; then
		NODE_BIN_DIR=/opt/homebrew/opt/node@24/bin
	else
		NODE_BIN_DIR=$(dirname "$(command -v node)")
	fi
fi

# Prepend the freshly-built erun binary to PATH so the desktop process
# (and every PTY tab it spawns) finds it. Without this, the desktop's
# `exec.LookPath("erun")` falls back to whatever older build the user
# has installed elsewhere — or fails outright with
# `exec: "erun": executable file not found in $PATH` when nothing is
# installed system-wide.
export PATH="$CLI_DIR/bin:$NODE_BIN_DIR:$(dirname "$YARN_BIN"):$PATH"

cd "$SCRIPT_DIR"
trap 'cd "$ORIGINAL_DIR"' EXIT
exec "$WAILS_BIN" dev "$@"
