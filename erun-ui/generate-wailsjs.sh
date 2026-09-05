#!/usr/bin/env sh

# Regenerates erun-ui/frontend/wailsjs/, the generated Wails bindings
# erun-ui/frontend imports. Gitignored/dockerignored like any other
# generated build artifact (dist, node_modules), so it is absent from a
# fresh checkout and from the erun-devops image test stage's build context
# alike -- anything that type-checks or builds erun-ui/frontend must
# regenerate it first. Shared by build.sh and the root Makefile's
# test-frontend target so the WAILS_BIN-or-install fallback lives in one
# place.

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd -P)
WAILS_BIN="${WAILS_BIN:-$(go env GOPATH)/bin/wails}"

cd "$SCRIPT_DIR"

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
