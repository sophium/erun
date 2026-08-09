#!/usr/bin/env sh

set -eu

ORIGINAL_DIR=$(pwd)
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
# Where this dev wrapper writes the binaries it builds. Defaults to the
# in-repo bin/ (gitignored), which keeps `erun app`'s sibling-directory lookup
# for erun-app/ERun.app working unchanged. ERUN_DEV_BIN_DIR moves them out of
# the worktree for callers that must not write into the checkout at all — a
# host-side orchestrator treats an environment's worktree as a read-only review
# directory, and merely invoking `erun` would otherwise mutate it. Set it to a
# directory you own (e.g. "$HOME/.cache/erun/dev-bin"); when erun-app is also
# resolved from there, build it there too so the sibling lookup still finds it.
BIN_DIR=${ERUN_DEV_BIN_DIR:-$SCRIPT_DIR/bin}
TARGET="$BIN_DIR/erun"
APP_TARGET="$BIN_DIR/erun-app"
UI_DIR="$SCRIPT_DIR/../erun-ui"
VERSION_FILE="$SCRIPT_DIR/../erun-devops/VERSION"

cd "$SCRIPT_DIR"

mkdir -p "$BIN_DIR"

BUILD_VERSION=dev
if [ -f "$VERSION_FILE" ]; then
	BUILD_VERSION=$(tr -d '\n' < "$VERSION_FILE")
fi

# The stamp has to describe the artifact, not the last commit. These scripts
# exist to build from a working checkout, so an uncommitted change is the normal
# case — and HEAD alone then names a commit the binary does not contain, which is
# exactly the question `erun version` is asked to answer. A dirty build says so.
#
# BUILD_DATE is when the binary was built, not when HEAD was authored: a fresh
# build off an older commit otherwise reports a stale-looking timestamp and reads
# as the wrong binary.
BUILD_COMMIT=
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
if git -C "$SCRIPT_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
	BUILD_COMMIT=$(git -C "$SCRIPT_DIR" rev-parse --short=12 HEAD)
	if [ -n "$(git -C "$SCRIPT_DIR" status --porcelain 2>/dev/null)" ]; then
		BUILD_COMMIT="${BUILD_COMMIT}-dirty"
	fi
fi

# --no-shell is the eval-friendly mode (`eval "$(erun open ... --no-shell)"`);
# the binary keeps stderr silent there, so the dev wrapper's rebuild progress
# lines would be the only thing leaking into the wrapping terminal. Suppress
# them here while keeping `go build`'s own output so compile errors still surface.
QUIET_REBUILD=0
for arg in "$@"; do
	case "$arg" in
	--no-shell ) QUIET_REBUILD=1; break ;;
	-- ) break ;;
	esac
done

if [ "$QUIET_REBUILD" -eq 0 ]; then
	printf '>> rebuilding erun CLI (%s)... ' "$BUILD_VERSION" >&2
fi
build_started_at=$(date +%s)
go build \
	-ldflags "-X github.com/sophium/erun/cmd.buildVersion=${BUILD_VERSION} -X github.com/sophium/erun/cmd.buildCommit=${BUILD_COMMIT} -X github.com/sophium/erun/cmd.buildDate=${BUILD_DATE}" \
	-o "$TARGET" \
	./
build_finished_at=$(date +%s)
if [ "$QUIET_REBUILD" -eq 0 ]; then
	printf 'ok (%ss) -> %s\n' "$((build_finished_at - build_started_at))" "$TARGET" >&2
fi

COMMAND_NAME=
for arg in "$@"; do
	case "$arg" in
	-- )
		break
		;;
	-* )
		;;
	* )
		COMMAND_NAME=$arg
		break
		;;
	esac
done

if [ "$COMMAND_NAME" = "app" ]; then
	# The desktop build needs Wails CLI + yarn + node; in environments
	# missing that toolchain (e.g. a runtime pod) build.sh exits non-zero
	# under `set -eu`. Don't take down `erun` itself when that happens —
	# the CLI's own `app` subcommand will still surface a clear "erun-app
	# executable not found" message and exit cleanly. The warning here
	# tells the user *why* the rebuild was skipped so the failure is not
	# a silent one.
	if ! (
		cd "$UI_DIR"
		./build.sh "$APP_TARGET"
	); then
		printf '>> skipping desktop rebuild: build.sh exited non-zero (missing wails/yarn/node toolchain?)\n' >&2
	fi
fi

cd "$ORIGINAL_DIR"

exec "$TARGET" "$@"
