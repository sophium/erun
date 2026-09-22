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
# BuildKit cache mount -- this script keys the generated output on the Go
# source that actually defines the bound API and skips the (expensive)
# `wails generate module` call when that key matches the last run, restoring
# the previously generated output instead. Content-hashed rather than
# mtime-checked because every Dockerfile COPY stamps a fresh mtime on every
# file on every build. This is what makes the same build's second call
# (test-frontend's own generation, then build.sh's inside test-playwright) a
# no-op, and what survives a COPY-layer cache invalidation that has nothing
# to do with the bound Go API (e.g. an unrelated frontend source edit).
#
# A hit reaches for no toolchain at all. That includes the `go env GOPATH` that
# resolves WAILS_BIN's default, which is why that default is resolved on the
# generation path rather than up front: a hit whose decision depends on the
# toolchain is not reliable, and one that dies when the toolchain cannot answer
# is worse than no cache.

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd -P)
WAILS_BIN="${WAILS_BIN:-}"
CACHE_DIR="${ERUN_WAILSJS_CACHE_DIR:-}"
STARTED_AT=$(date +%s)

cd "$SCRIPT_DIR"

# The bound API surface is defined by this module's own top-level and
# headlessserver Go packages plus every erun-common type they can reference
# (any of it can cross into a generated TS shape), and generation itself
# depends on the pinned wails module version (go.mod/go.sum) and wails.json.
#
# Paths are relative to the module either way, so a key is a function of what
# the repository contains rather than of where the checkout happens to sit.
hash_bound_module_inputs() {
	{
		find . -maxdepth 1 -name '*.go' -print
		find ./headlessserver -name '*.go' -print 2>/dev/null
		printf '%s\n' ./wails.json ./go.mod ./go.sum
	} | sort | xargs sha256sum | sha256sum | awk '{print $1}'
}

# Everything `go doc` reads out of erun-common, hashed by content. This half
# costs no toolchain and no network: it is what gates the declaration digest
# below, because recomputing that digest unconditionally is what made a cache
# *hit* as expensive as a miss.
hash_common_sources() {
	(
		cd "$SCRIPT_DIR/../erun-common"
		{
			find . -name '*.go' -print
			for file in ./go.mod ./go.sum; do
				if [ -e "$file" ]; then printf '%s\n' "$file"; fi
			done
		} | sort | xargs sha256sum
	) | sha256sum | awk '{print $1}'
}

# erun-common contributes its *declarations* rather than its file contents.
# `wails generate module` emits TS from the bound methods' signatures and the
# types those reach, so a change confined to a function body cannot alter the
# output -- yet hashing whole files made every such edit a cache miss, and
# erun-common is the shared module nearly every change touches.
#
# `go doc -all -u` is a superset of what can reach the generated TS, not a
# heuristic: -u includes unexported declarations, so an exported field whose
# type is an unexported struct still has that struct's shape in the hash.
#
# It is also the expensive half. It type-checks the package and everything it
# imports, which is seconds against a warm build cache and far longer against
# a cold one, and it cannot answer at all without a module cache it can
# resolve. So it is never paid when erun-common is byte-identical to the run
# that already paid it (see the caller). Prints nothing and returns non-zero
# when the declarations cannot be read.
hash_common_declarations() {
	DECLARATIONS=$(cd "$SCRIPT_DIR/../erun-common" && go doc -all -u . 2>/dev/null) || return 1
	if [ -z "$DECLARATIONS" ]; then
		return 1
	fi
	printf '%s' "$DECLARATIONS" | sha256sum | awk '{print $1}'
}

if [ -n "$CACHE_DIR" ]; then
	mkdir -p "$CACHE_DIR"
	STATE_FILE="$CACHE_DIR/state"
	CACHED_WAILSJS="$CACHE_DIR/wailsjs"

	SOURCE_HASH=$(hash_common_sources)
	STORED_KEY=''
	STORED_SOURCES=''
	STORED_DECLARATIONS=''
	if [ -f "$STATE_FILE" ]; then
		# Three space-separated fields, written as the last thing a successful
		# run does; a torn or absent file reads as no cache at all.
		read -r STORED_KEY STORED_SOURCES STORED_DECLARATIONS < "$STATE_FILE" || true
	fi

	# The cheap hash gates the expensive one. Byte-identical erun-common means
	# the stored digest was computed from exactly these bytes, and a digest is
	# only ever a stand-in for those bytes, so it is reused without asking the
	# toolchain again. A changed erun-common still pays for a fresh digest --
	# and a body-only change lands back on the digest it already had, which is
	# the whole point of hashing declarations.
	if [ -n "$STORED_DECLARATIONS" ] && [ "$STORED_SOURCES" = "$SOURCE_HASH" ]; then
		DECLARATION_KEY="$STORED_DECLARATIONS"
	elif DECLARATION_HASH=$(hash_common_declarations); then
		DECLARATION_KEY="declarations:$DECLARATION_HASH"
	else
		# Conservative, and reachable: a package the toolchain cannot read
		# falls back to hashing its file contents, which is at least a
		# function of what is on disk. It must never fall back to a constant
		# -- a constant matches the next unreadable package too, whatever it
		# contains, and hands back bindings generated from source that is
		# gone. The mode prefix keeps the two key spaces apart, so a run that
		# could read the package and a run that could not never collide.
		DECLARATION_KEY="contents:$SOURCE_HASH"
		printf 'generate-wailsjs.sh: erun-common declarations unreadable; keying on its file contents instead\n' >&2
	fi

	NEW_KEY=$(
		{
			hash_bound_module_inputs
			printf '%s\n' "$DECLARATION_KEY"
		} | sha256sum | awk '{print $1}'
	)

	if [ -n "$STORED_KEY" ] && [ "$STORED_KEY" = "$NEW_KEY" ] && [ -d "$CACHED_WAILSJS" ]; then
		if [ ! -d frontend/wailsjs ] || [ -z "$(ls -A frontend/wailsjs 2>/dev/null)" ]; then
			rm -rf frontend/wailsjs
			cp -a "$CACHED_WAILSJS" frontend/wailsjs
		fi
		printf 'generate-wailsjs.sh: bound Go API unchanged, reusing cached bindings (%ss)\n' "$(($(date +%s) - STARTED_AT))" >&2
		exit 0
	fi
fi

# Resolved here, not at the top, because `go env GOPATH` is a toolchain
# invocation like any other: run above the cache check it put the toolchain on
# the hit path, so an unanswerable `go env` killed the script instead of
# hitting or regenerating. The Dockerfile sets no WAILS_BIN, so this is the
# path the gate actually takes, not a corner case.
if [ -z "$WAILS_BIN" ]; then
	WAILS_BIN="$(go env GOPATH)/bin/wails"
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
	# Key last: a run interrupted before this point leaves a cache that reads
	# as a miss, never a key paired with bindings it did not come from.
	printf '%s %s %s\n' "$NEW_KEY" "$SOURCE_HASH" "$DECLARATION_KEY" > "$STATE_FILE"
fi

printf 'generate-wailsjs.sh: generated bindings (%ss)\n' "$(($(date +%s) - STARTED_AT))" >&2
