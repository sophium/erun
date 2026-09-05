#!/usr/bin/env sh

set -eu

ORIGINAL_DIR=$(pwd)
# -P so every path derived from here has one spelling, whichever symlinked
# route the caller reached this script through — the desktop build this
# delegates to gates on that.
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd -P)
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

# --no-shell is the eval-friendly mode (`eval "$(erun open ... --no-shell)"`);
# the binary keeps stderr silent there, so the dev wrapper's rebuild progress
# lines (and the staleness warning below) would be the only thing leaking into
# the wrapping terminal. Suppress them here while keeping `go build`'s own
# output so compile errors still surface.
QUIET_REBUILD=0
for arg in "$@"; do
	case "$arg" in
	--no-shell ) QUIET_REBUILD=1; break ;;
	-- ) break ;;
	esac
done

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
IN_GIT_TREE=0
if git -C "$SCRIPT_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
	IN_GIT_TREE=1
	BUILD_COMMIT=$(git -C "$SCRIPT_DIR" rev-parse --short=12 HEAD)
	if [ -n "$(git -C "$SCRIPT_DIR" status --porcelain 2>/dev/null)" ]; then
		BUILD_COMMIT="${BUILD_COMMIT}-dirty"
	fi
fi

# The gap between this checkout and its remote is read from refs already on
# disk, never a new fetch: this wrapper runs on every `erun` invocation, and a
# network round-trip per call would make the whole CLI slow and break offline
# use. A build sitting behind its own upstream stamps a version that looks
# exactly as plausible as a current one — only the commit tells the two apart —
# so a real gap is reported loudly rather than left for the version string to
# quietly misrepresent. This never blocks the build: building behind is fine
# when it is a deliberate choice, and the point of the warning is to make sure
# it is one.
if [ "$IN_GIT_TREE" -eq 1 ] && [ "$QUIET_REBUILD" -eq 0 ]; then
	UPSTREAM_REF=$(git -C "$SCRIPT_DIR" rev-parse --abbrev-ref --symbolic-full-name '@{upstream}' 2>/dev/null || true)
	if [ -n "$UPSTREAM_REF" ]; then
		BEHIND_COUNT=$(git -C "$SCRIPT_DIR" rev-list --count 'HEAD..@{upstream}' 2>/dev/null || echo 0)
		if [ "$BEHIND_COUNT" -gt 0 ]; then
			printf '>> WARNING: build source is %s commit(s) behind %s (as of the last fetch) -- rebuilding now will NOT include what moved upstream. Run git fetch/pull in %s to catch up.\n' \
				"$BEHIND_COUNT" "$UPSTREAM_REF" "$SCRIPT_DIR" >&2
		fi
	fi
fi

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
