#!/usr/bin/env sh

set -eu

ORIGINAL_DIR=$(pwd)
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
TARGET=${1:-"$SCRIPT_DIR/bin/erun-app"}
VERSION_FILE="$SCRIPT_DIR/../erun-devops/VERSION"
WAILS_BIN="${WAILS_BIN:-$(go env GOPATH)/bin/wails}"
TARGET_GOOS="${GOOS:-$(go env GOOS)}"
YARN_BIN="${YARN_BIN:-}"
NODE_BIN_DIR="${NODE_BIN_DIR:-}"

cd "$SCRIPT_DIR"

mkdir -p "$(dirname "$TARGET")"

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
export PATH="$NODE_BIN_DIR:$(dirname "$YARN_BIN"):$PATH"

if [ -d frontend ]; then
	mkdir -p frontend/dist
fi

if [ -x "$WAILS_BIN" ]; then
	"$WAILS_BIN" generate module
fi

if [ -d frontend ]; then
	cd frontend
	if [ -f yarn.lock ]; then
		"$YARN_BIN" install --frozen-lockfile
	else
		"$YARN_BIN" install
	fi
	"$YARN_BIN" build
	cd "$SCRIPT_DIR"
fi

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

LDFLAGS="-s -w -X github.com/sophium/erun/erun-ui.buildVersion=${BUILD_VERSION} -X github.com/sophium/erun/erun-ui.buildCommit=${BUILD_COMMIT} -X github.com/sophium/erun/erun-ui.buildDate=${BUILD_DATE}"
if [ "$TARGET_GOOS" = "windows" ]; then
	LDFLAGS="${LDFLAGS} -H windowsgui"
fi

if [ "$TARGET_GOOS" = "darwin" ]; then
	export CGO_ENABLED=1
	export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-11.0}"
	MACOS_MIN_FLAG="-mmacosx-version-min=${MACOSX_DEPLOYMENT_TARGET}"
	export CGO_CFLAGS="${CGO_CFLAGS:+$CGO_CFLAGS }$MACOS_MIN_FLAG"
	export CGO_CXXFLAGS="${CGO_CXXFLAGS:+$CGO_CXXFLAGS }$MACOS_MIN_FLAG"
	export CGO_LDFLAGS="${CGO_LDFLAGS:+$CGO_LDFLAGS }$MACOS_MIN_FLAG"
fi

go build \
	-tags "desktop,production" \
	-ldflags "$LDFLAGS" \
	-o "$TARGET" \
	./

if [ "$TARGET_GOOS" = "darwin" ]; then
	APP_BUNDLE="$(dirname "$TARGET")/ERun.app"
	APP_CONTENTS="$APP_BUNDLE/Contents"
	APP_MACOS="$APP_CONTENTS/MacOS"
	APP_RESOURCES="$APP_CONTENTS/Resources"
	ICONSET="$APP_RESOURCES/iconfile.iconset"
	ICON_SOURCE="$SCRIPT_DIR/build/appicon.png"

	rm -rf "$APP_BUNDLE"
	mkdir -p "$APP_MACOS" "$APP_RESOURCES" "$ICONSET"
	cp "$TARGET" "$APP_MACOS/erun-app"

	for icon_size in 16 32 128 256 512; do
		sips -z "$icon_size" "$icon_size" "$ICON_SOURCE" --out "$ICONSET/icon_${icon_size}x${icon_size}.png" >/dev/null
		double_size=$((icon_size * 2))
		sips -z "$double_size" "$double_size" "$ICON_SOURCE" --out "$ICONSET/icon_${icon_size}x${icon_size}@2x.png" >/dev/null
	done
	iconutil -c icns "$ICONSET" -o "$APP_RESOURCES/iconfile.icns"
	rm -rf "$ICONSET"

	cat > "$APP_CONTENTS/Info.plist" <<EOF
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>CFBundleDevelopmentRegion</key>
    <string>en</string>
    <key>CFBundleExecutable</key>
    <string>erun-app</string>
    <key>CFBundleIconFile</key>
    <string>iconfile</string>
    <key>CFBundleIdentifier</key>
    <string>com.sophium.erun</string>
    <key>CFBundleName</key>
    <string>ERun</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleShortVersionString</key>
    <string>$BUILD_VERSION</string>
    <key>CFBundleVersion</key>
    <string>$BUILD_VERSION</string>
    <key>LSMinimumSystemVersion</key>
    <string>11.0</string>
    <key>NSHighResolutionCapable</key>
    <true/>
  </dict>
</plist>
EOF
	plutil -lint "$APP_CONTENTS/Info.plist" >/dev/null
fi

cd "$ORIGINAL_DIR"
