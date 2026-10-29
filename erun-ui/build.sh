#!/usr/bin/env sh

set -eu

ORIGINAL_DIR=$(pwd)
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd -P)
TARGET_GOOS="${GOOS:-$(go env GOOS)}"

case "${1:-}" in
-*)
	printf 'build.sh: unknown flag %s; usage: build.sh [target]\n' "$1" >&2
	exit 2
	;;
esac
if [ $# -gt 1 ]; then
	printf 'usage: build.sh [target]\n' >&2
	exit 2
fi
TARGET=${1:-"$SCRIPT_DIR/bin/erun-app"}
VERSION_FILE="$SCRIPT_DIR/../erun-devops/VERSION"

cd "$SCRIPT_DIR"

mkdir -p "$(dirname "$TARGET")"

# Builds only; lint and test gates run in the component's test stage.
# The binding generator compiles this package, whose embedded assets need frontend/dist.
mkdir -p frontend/dist
go tool wails generate module

# Install from the root workspace lockfile, the only one the frontend resolves against.
(cd "$SCRIPT_DIR/.." && yarn install --frozen-lockfile --prefer-offline --network-timeout 600000)
(cd "$SCRIPT_DIR/frontend" && yarn build)

BUILD_VERSION=dev
if [ -f "$VERSION_FILE" ]; then
	BUILD_VERSION=$(tr -d '\n' < "$VERSION_FILE")
fi

# The stamp describes the artifact: a dirty tree says so, and BUILD_DATE is build
# time, not commit time.
BUILD_COMMIT=
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
if git -C "$SCRIPT_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
	BUILD_COMMIT=$(git -C "$SCRIPT_DIR" rev-parse --short=12 HEAD)
	if [ -n "$(git -C "$SCRIPT_DIR" status --porcelain 2>/dev/null)" ]; then
		BUILD_COMMIT="${BUILD_COMMIT}-dirty"
	fi
fi

# The About panel is the only version a released bundle shows, so CFBundleVersion
# carries the commit too.
BUNDLE_VERSION="$BUILD_VERSION"
if [ -n "$BUILD_COMMIT" ]; then
	BUNDLE_VERSION="${BUILD_VERSION}+${BUILD_COMMIT}"
fi

# Stamped because the binary runs outside its checkout; single-quoted so a path
# with spaces survives -ldflags splitting.
SKILLS_SOURCE="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd -P)/erun-skills/skills"

AGENTS_SOURCE="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd -P)/erun-skills/agents"

# `main.`, not the module path: -X silently ignores a path no package declares.
LDFLAGS="-s -w -X main.buildVersion=${BUILD_VERSION} -X main.buildCommit=${BUILD_COMMIT} -X main.buildDate=${BUILD_DATE} -X 'main.buildSkillsSource=${SKILLS_SOURCE}' -X 'main.buildAgentsSource=${AGENTS_SOURCE}'"
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

printf '>> rebuilding erun desktop binary (%s)... ' "$BUILD_VERSION" >&2
build_started_at=$(date +%s)
# webkit2_41 selects webkit2gtk-4.1/libsoup-3, which Noble ships; the tag only
# affects Linux builds.
go build \
	-tags "desktop,production,webkit2_41" \
	-ldflags "$LDFLAGS" \
	-o "$TARGET" \
	./
build_finished_at=$(date +%s)
printf 'ok (%ss) -> %s\n' "$((build_finished_at - build_started_at))" "$TARGET" >&2

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
    <string>$BUNDLE_VERSION</string>
    <key>LSMinimumSystemVersion</key>
    <string>11.0</string>
    <key>NSHighResolutionCapable</key>
    <true/>
  </dict>
</plist>
EOF
	plutil -lint "$APP_CONTENTS/Info.plist" >/dev/null
fi

# A stable identity keeps macOS privacy grants across rebuilds. Not a build gate;
# a no-op off macOS.
if [ "$TARGET_GOOS" = "darwin" ]; then
	"$SCRIPT_DIR/codesign.sh" "$APP_BUNDLE" "$TARGET" || true
else
	"$SCRIPT_DIR/codesign.sh" "$TARGET" || true
fi

cd "$ORIGINAL_DIR"
