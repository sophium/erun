#!/usr/bin/env sh

set -eu

ORIGINAL_DIR=$(pwd)
# -P so a checkout reached through a symlink resolves to the one spelling the
# lint gate below caches its findings against.
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd -P)
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
else
	WAILS_VERSION=$(go list -m -f '{{.Version}}' github.com/wailsapp/wails/v2)
	WAILS_TMP="${TMPDIR:-/tmp}/erun-wails-$$"
	mkdir -p "$WAILS_TMP"
	GOBIN="$WAILS_TMP" go install "github.com/wailsapp/wails/v2/cmd/wails@$WAILS_VERSION"
	"$WAILS_TMP/wails" generate module
	rm -rf "$WAILS_TMP"
fi

if [ -d frontend ]; then
	cd frontend
	if [ -f yarn.lock ]; then
		"$YARN_BIN" install --frozen-lockfile
	else
		"$YARN_BIN" install
	fi
	# Gate the bundle on the same checks CI would run. `ERUN_SKIP_LINT=1`
	# escapes the gates locally when iterating; CI never sets it.
	if [ "${ERUN_SKIP_LINT:-0}" != "1" ]; then
		"$YARN_BIN" typecheck
		"$YARN_BIN" lint
		"$YARN_BIN" format:check
		# The frontend unit tests cover pure logic the Playwright suite
		# cannot isolate (stream parsing, row-state derivation). They were
		# runnable but ungated, which is the same as absent.
		"$YARN_BIN" test
		# Go-side lint gate. erun-ui's Go module is not built by the
		# erun-devops image (it needs this CGO/webkit + frontend toolchain),
		# so its golangci-lint gate lives here next to the frontend checks
		# rather than in the shared `make check` the image runs.
		#
		# The cache is scoped to this checkout because golangci-lint keys it by
		# module, not by working tree: a second checkout of erun-ui would
		# otherwise replay the first one's results against a tree that never
		# produced them. Correctness does not rest on that alone — a replayed
		# finding still renders the path of the run that cached it, so
		# .golangci.yml matches excluded paths by segment instead of anchoring
		# them to a working tree.
		(cd "$SCRIPT_DIR" && GOLANGCI_LINT_CACHE="${GOLANGCI_LINT_CACHE:-$SCRIPT_DIR/.golangci-cache}" golangci-lint run ./...)
	fi
	"$YARN_BIN" build
	cd "$SCRIPT_DIR"
fi

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

# Where this build's skills come from. The desktop installs them for a host
# orchestrator, and the binary it runs is routinely copied out of the checkout
# (a dev build runs from ~/.cache/erun), so nothing above the running bundle
# names a source tree. Stamping the path is what keeps an installed skill
# tracking the checkout this binary was built from; on a machine that does not
# carry that checkout the resolution falls back to the layout it runs in.
# Single-quoted so a checkout path containing spaces survives -ldflags splitting.
SKILLS_SOURCE="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd -P)/erun-skills/skills"

# `main.`, not the module path. -X addresses a symbol by package path, and these
# vars are declared in package main, whose symbols the compiler names `main.x`
# whatever the module is called. A path nothing declares is not an error — the
# linker drops it in silence and the binary keeps its default, which is how the
# desktop shipped build info and a skills source it never actually carried.
LDFLAGS="-s -w -X main.buildVersion=${BUILD_VERSION} -X main.buildCommit=${BUILD_COMMIT} -X main.buildDate=${BUILD_DATE} -X 'main.buildSkillsSource=${SKILLS_SOURCE}'"
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
# `webkit2_41` opts Wails into webkit2gtk-4.1 + libsoup-3.0 via pkg-config
# instead of the legacy 4.0 / libsoup-2.4 defaults. Ubuntu Noble (and the
# runtime image built on top of it) ships 4.1; without this tag the in-pod
# CGO build aborts with "Package 'webkit2gtk-4.0', required by
# 'virtual:world', not found". Harmless on macOS / Windows because the
# matching build-constraints only fire on Linux.
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
