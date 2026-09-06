#!/usr/bin/env sh

# Regenerates erun-ui/frontend/wailsjs/, the generated Wails bindings
# erun-ui/frontend imports. Gitignored/dockerignored like any other
# generated build artifact (dist, node_modules), so it is absent from a
# fresh checkout and from the erun-devops image test stage's build context
# alike -- anything that type-checks or builds erun-ui/frontend must
# regenerate it first. Shared by build.sh and the root Makefile's
# test-frontend target so the WAILS_BIN-or-install fallback lives in one
# place.
#
# ERUN_WAILSJS_CACHE_DIR opts into skip-when-unchanged: unset (the default
# for a local `./generate-wailsjs.sh` run), generation is unconditional, same
# as always. When set -- the erun-devops Dockerfile points it at a
# BuildKit cache mount -- this script hashes the Go source that actually
# defines the bound API and skips the (expensive) `wails generate module`
# call when that hash matches the last run, restoring the previously
# generated output instead. Content-hashed rather than mtime-checked because
# every Dockerfile COPY stamps a fresh mtime on every file on every build.
# This is what makes the same build's second call (test-frontend's own
# generation, then build.sh's inside test-playwright) a no-op, and what
# survives a COPY-layer cache invalidation that has nothing to do with the
# bound Go API (e.g. an unrelated frontend source edit).

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd -P)
WAILS_BIN="${WAILS_BIN:-$(go env GOPATH)/bin/wails}"
CACHE_DIR="${ERUN_WAILSJS_CACHE_DIR:-}"
STARTED_AT=$(date +%s)

cd "$SCRIPT_DIR"

# The bound API surface is defined by this module's own top-level and
# headlessserver Go packages plus every erun-common type they can reference
# (any of it can cross into a generated TS shape), and generation itself
# depends on the pinned wails module version (go.mod/go.sum) and wails.json.
hash_wails_inputs() {
	{
		find "$SCRIPT_DIR" -maxdepth 1 -name '*.go' -print
		find "$SCRIPT_DIR/headlessserver" -name '*.go' -print 2>/dev/null
		find "$SCRIPT_DIR/../erun-common" -name '*.go' -print
		printf '%s\n' "$SCRIPT_DIR/wails.json" "$SCRIPT_DIR/go.mod" "$SCRIPT_DIR/go.sum"
	} | sort | xargs sha256sum | sha256sum | awk '{print $1}'
}

if [ -n "$CACHE_DIR" ]; then
	mkdir -p "$CACHE_DIR"
	HASH_FILE="$CACHE_DIR/hash"
	CACHED_WAILSJS="$CACHE_DIR/wailsjs"
	NEW_HASH=$(hash_wails_inputs)
	if [ -f "$HASH_FILE" ] && [ "$(cat "$HASH_FILE")" = "$NEW_HASH" ] && [ -d "$CACHED_WAILSJS" ]; then
		if [ ! -d frontend/wailsjs ] || [ -z "$(ls -A frontend/wailsjs 2>/dev/null)" ]; then
			rm -rf frontend/wailsjs
			cp -a "$CACHED_WAILSJS" frontend/wailsjs
		fi
		printf 'generate-wailsjs.sh: bound Go API unchanged, reusing cached bindings (%ss)\n' "$(($(date +%s) - STARTED_AT))" >&2
		exit 0
	fi
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

if [ -n "$CACHE_DIR" ]; then
	rm -rf "$CACHED_WAILSJS"
	cp -a frontend/wailsjs "$CACHED_WAILSJS"
	printf '%s' "$NEW_HASH" > "$HASH_FILE"
fi

printf 'generate-wailsjs.sh: generated bindings (%ss)\n' "$(($(date +%s) - STARTED_AT))" >&2
