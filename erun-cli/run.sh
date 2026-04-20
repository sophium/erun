#!/usr/bin/env sh

set -eu

ORIGINAL_DIR=$(pwd)
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
TARGET="$SCRIPT_DIR/bin/erun"
APP_TARGET="$SCRIPT_DIR/bin/erun-app"
UI_DIR="$SCRIPT_DIR/../erun-ui"
VERSION_FILE="$SCRIPT_DIR/../erun-devops/VERSION"

cd "$SCRIPT_DIR"

mkdir -p bin

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

go build \
	-ldflags "-X github.com/sophium/erun/cmd.buildVersion=${BUILD_VERSION} -X github.com/sophium/erun/cmd.buildCommit=${BUILD_COMMIT} -X github.com/sophium/erun/cmd.buildDate=${BUILD_DATE}" \
	-o "$TARGET" \
	./

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
	(
		cd "$UI_DIR"
		./build.sh "$APP_TARGET"
	)
fi

cd "$ORIGINAL_DIR"

exec "$TARGET" "$@"
