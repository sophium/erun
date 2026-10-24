#!/usr/bin/env sh
# Development wrapper: builds erun (and, for `erun app`, the desktop) from this
# checkout, then runs it. The contribute shim forwards every `erun` call here.

set -eu

ORIGINAL_DIR=$(pwd)
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd -P)
# ERUN_DEV_BIN_DIR moves the binaries out of the checkout, for callers that must
# not write into it (a host orchestrator's read-only review directory).
BIN_DIR=${ERUN_DEV_BIN_DIR:-$SCRIPT_DIR/bin}
TARGET="$BIN_DIR/erun"
APP_TARGET="$BIN_DIR/erun-app"
UI_DIR="$SCRIPT_DIR/../erun-ui"

cd "$SCRIPT_DIR"
mkdir -p "$BIN_DIR"

# `--no-shell` output is eval'd by the caller, so the wrapper stays silent.
QUIET_REBUILD=0
for arg in "$@"; do
	case "$arg" in
	--no-shell ) QUIET_REBUILD=1; break ;;
	-- ) break ;;
	esac
done

COMMAND_NAME=
HELP_REQUEST=0
for arg in "$@"; do
	case "$arg" in
	-- ) break ;;
	-h | --help ) HELP_REQUEST=1 ;;
	-* ) ;;
	* )
		if [ -z "$COMMAND_NAME" ]; then
			COMMAND_NAME=$arg
		fi
		case "$arg" in
		help | __complete | __completeNoDesc ) HELP_REQUEST=1 ;;
		esac
		;;
	esac
done

BUILD_VERSION=$(tr -d '[:space:]' 2>/dev/null < "$SCRIPT_DIR/../erun-devops/VERSION" || true)
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
# The commit names what the binary contains, so an uncommitted change says so.
BUILD_COMMIT=$(git -C "$SCRIPT_DIR" rev-parse --short=12 HEAD 2>/dev/null || true)
if [ -n "$BUILD_COMMIT" ] && [ -n "$(git -C "$SCRIPT_DIR" status --porcelain 2>/dev/null)" ]; then
	BUILD_COMMIT="${BUILD_COMMIT}-dirty"
fi

# Read from refs on disk, never fetched: this runs on every `erun` call.
if [ -n "$BUILD_COMMIT" ] && [ "$QUIET_REBUILD" -eq 0 ]; then
	UPSTREAM_REF=$(git -C "$SCRIPT_DIR" rev-parse --abbrev-ref --symbolic-full-name '@{upstream}' 2>/dev/null || true)
	if [ -n "$UPSTREAM_REF" ]; then
		BEHIND_COUNT=$(git -C "$SCRIPT_DIR" rev-list --count 'HEAD..@{upstream}' 2>/dev/null || echo 0)
		if [ "$BEHIND_COUNT" -gt 0 ]; then
			printf '>> WARNING: build source is %s commit(s) behind %s (as of the last fetch). Run git fetch/pull in %s to catch up.\n' \
				"$BEHIND_COUNT" "$UPSTREAM_REF" "$SCRIPT_DIR" >&2
		fi
	fi
fi

# Help text is static, so it never pays for a build.
if [ "$HELP_REQUEST" -eq 1 ] && [ -x "$TARGET" ]; then
	cd "$ORIGINAL_DIR"
	exec "$TARGET" "$@"
fi

if [ "$QUIET_REBUILD" -eq 0 ]; then
	printf '>> rebuilding erun CLI (%s)... ' "${BUILD_VERSION:-dev}" >&2
fi
build_started_at=$(date +%s)
go build \
	-ldflags "-X github.com/sophium/erun/cmd/shared.buildVersion=${BUILD_VERSION:-dev} -X github.com/sophium/erun/cmd/shared.buildCommit=${BUILD_COMMIT} -X github.com/sophium/erun/cmd/shared.buildDate=${BUILD_DATE}" \
	-o "$TARGET" \
	./
if [ "$QUIET_REBUILD" -eq 0 ]; then
	printf 'ok (%ss) -> %s\n' "$(($(date +%s) - build_started_at))" "$TARGET" >&2
fi

# `erun app` launches the newest desktop on disk, so an app launch rebuilds it
# here. A host without yarn/node still reaches `erun app`'s own diagnosis.
if [ "$COMMAND_NAME" = "app" ] && [ "$HELP_REQUEST" -eq 0 ]; then
	if ! (cd "$UI_DIR" && ./build.sh "$APP_TARGET"); then
		printf '>> skipping desktop rebuild: build.sh exited non-zero (missing yarn/node toolchain?)\n' >&2
	fi
fi

cd "$ORIGINAL_DIR"
exec "$TARGET" "$@"
