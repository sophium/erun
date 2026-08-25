#!/usr/bin/env sh
# Canonical entry point for the erun-ui Playwright suite.
#
# Used both interactively and by erun-ui's build/packaging flow. Builds the
# desktop binary if needed, ensures Yarn deps + bundled Chromium are present,
# then runs `playwright test` against `erun-app --headless`.
#
# Flags:
#   --build               Force a desktop-binary rebuild even when
#                         ../bin/erun-app already exists. By default the
#                         script only builds when the binary is missing, so
#                         packaging pipelines that produced the binary in
#                         an earlier step don't pay the build cost twice.
#   --skip-build          (Deprecated alias for the default behaviour. Kept
#                         so older invocations still work.)
#   --skip-lint           Skip typecheck/lint/format:check for this invocation
#                         only (and forward the same skip to build.sh when a
#                         rebuild runs). Use only when iterating locally;
#                         never in CI.
#   --port N              Backend port (default 34123).
#   --headed              Run the browser with a visible window.
#   --                    Forward everything after this to `playwright test`.
#
# Anything not matching a known flag is forwarded to `playwright test`, so
# `./run.sh --grep sidebar` and `yarn test --grep sidebar` both work.

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
ERUN_UI_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
BIN_PATH="$ERUN_UI_DIR/bin/erun-app"

FORCE_BUILD=0
HEADED=0
PORT=34123
PLAYWRIGHT_ARGS=""
E2E_K3D=0
SKIP_LINT=0

while [ $# -gt 0 ]; do
	case "$1" in
		--build)
			FORCE_BUILD=1
			shift
			;;
		--skip-lint)
			SKIP_LINT=1
			shift
			;;
		--e2e-k3d)
			# Opt-in k3d-backed e2e mode (issue #647): run the real
			# create -> build -> push -> deploy -> open flow against a live
			# local k3d cluster. Requires Docker + k3d + binfmt on the host.
			E2E_K3D=1
			shift
			;;
		--skip-build)
			# Default behaviour now skips the build unless --build is set;
			# keep the flag as a no-op so older callers don't break.
			shift
			;;
		--headed)
			HEADED=1
			shift
			;;
		--port)
			if [ $# -lt 2 ]; then
				printf 'run.sh: --port requires a value\n' >&2
				exit 2
			fi
			PORT="$2"
			shift 2
			;;
		--port=*)
			PORT="${1#--port=}"
			shift
			;;
		--)
			shift
			while [ $# -gt 0 ]; do
				PLAYWRIGHT_ARGS="$PLAYWRIGHT_ARGS \"$1\""
				shift
			done
			;;
		-h|--help)
			grep '^#' "$0" | sed 's/^# \{0,1\}//'
			exit 0
			;;
		*)
			# Unknown flag — assume it's a playwright passthrough. Yarn 1
			# strips its own "--" separator before invoking the script, so
			# `yarn test -- --grep foo` reaches run.sh as `--grep foo`
			# without a separator. Treat the unknown flag and everything
			# after it as playwright args so both invocation styles work.
			while [ $# -gt 0 ]; do
				PLAYWRIGHT_ARGS="$PLAYWRIGHT_ARGS \"$1\""
				shift
			done
			;;
	esac
done

# Locate yarn the same way build.sh does so packaging environments stay
# aligned.
YARN_BIN="${YARN_BIN:-}"
if [ -z "$YARN_BIN" ]; then
	YARN_BIN=$(command -v yarn)
fi
if [ -z "$YARN_BIN" ]; then
	printf 'run.sh: yarn not found on PATH\n' >&2
	exit 1
fi

NODE_BIN_DIR="${NODE_BIN_DIR:-}"
if [ -z "$NODE_BIN_DIR" ]; then
	if [ -x /opt/homebrew/opt/node@24/bin/node ]; then
		NODE_BIN_DIR=/opt/homebrew/opt/node@24/bin
	else
		NODE_BIN_DIR=$(dirname "$(command -v node)")
	fi
fi
export PATH="$NODE_BIN_DIR:$(dirname "$YARN_BIN"):$PATH"

cd "$SCRIPT_DIR"

# Build the desktop binary so the webServer fixture has something to spawn.
# By default we only build when the binary is missing — packaging flows do
# their own build step right before invoking this script. Devs who changed
# Go code and want a fresh binary pass --build.
if [ "$FORCE_BUILD" -eq 1 ] || [ ! -x "$BIN_PATH" ]; then
	printf '>> playwright: building %s...\n' "$BIN_PATH" >&2
	if [ "$SKIP_LINT" -eq 1 ]; then
		"$ERUN_UI_DIR/build.sh" --skip-lint "$BIN_PATH"
	else
		"$ERUN_UI_DIR/build.sh" "$BIN_PATH"
	fi
fi

if [ ! -x "$BIN_PATH" ]; then
	printf 'run.sh: %s is not executable after build step\n' "$BIN_PATH" >&2
	exit 1
fi

# Idempotent dependency setup. Yarn no-ops when the lockfile is satisfied,
# and `playwright install` short-circuits when the bundled Chromium revision
# is already cached, so this is cheap on warm runs.
if [ ! -d node_modules ] || [ ! -f node_modules/.yarn-integrity ]; then
	printf '>> playwright: yarn install\n' >&2
	"$YARN_BIN" install --frozen-lockfile
fi

PLAYWRIGHT_BIN="$SCRIPT_DIR/node_modules/.bin/playwright"
if [ ! -x "$PLAYWRIGHT_BIN" ]; then
	printf 'run.sh: playwright not installed under %s; did yarn install fail?\n' "$PLAYWRIGHT_BIN" >&2
	exit 1
fi

# Gate the suite on the same checks CI would run. Skip with --skip-lint for
# one invocation when iterating locally; never in CI.
if [ "$SKIP_LINT" -eq 1 ]; then
	printf '>> SKIPPING typecheck/lint/format:check (--skip-lint)\n' >&2
else
	printf '>> playwright: typecheck + lint + format:check\n' >&2
	"$YARN_BIN" typecheck
	"$YARN_BIN" lint
	"$YARN_BIN" format:check
fi

# `playwright install chromium` is idempotent — it checks whether the
# expected revision is already on disk and skips the download when it is.
printf '>> playwright: ensuring chromium\n' >&2
"$PLAYWRIGHT_BIN" install chromium >/dev/null

ERUN_PLAYWRIGHT_PORT="$PORT"
export ERUN_PLAYWRIGHT_PORT

# Isolated config root (issue #483): the headless backend and every erun
# child process run against a throwaway HOME, so the suite never reads or
# writes the developer's real ~/.erun / ~/.config/erun. playwright.config.ts
# points the webServer's HOME/XDG_* at this root, global-setup seeds the
# deterministic baseline, and global-teardown removes it. The EXIT trap
# below covers aborted runs; a caller-provided ERUN_PLAYWRIGHT_HOME is
# respected and never deleted by the trap.
ERUN_PLAYWRIGHT_HOME_CREATED=0
if [ -z "${ERUN_PLAYWRIGHT_HOME:-}" ]; then
	ERUN_PLAYWRIGHT_HOME=$(mktemp -d "${TMPDIR:-/tmp}/erun-playwright-home.XXXXXX")
	ERUN_PLAYWRIGHT_HOME_CREATED=1
fi
export ERUN_PLAYWRIGHT_HOME
cleanup_isolated_home() {
	if [ "$ERUN_PLAYWRIGHT_HOME_CREATED" -eq 1 ]; then
		rm -rf "$ERUN_PLAYWRIGHT_HOME"
	fi
}
trap cleanup_isolated_home EXIT

# Opt-in k3d e2e mode (issue #647): un-stub the backend (real docker/kubectl/
# helm + the real erun CLI), register binfmt for the mandatory multi-arch build,
# and run ONLY the e2e specs (the inert specs assume stubs). global-setup brings
# the k3d cluster up; global-teardown (and the EXIT trap) tear it down. Must be
# exported before the playwright invocation below so playwright.config.ts /
# backendEnv() see it at config-load and at webServer boot.
if [ "$E2E_K3D" -eq 1 ]; then
	export ERUN_E2E_K3D=1
	printf '>> playwright(k3d): building the erun CLI for the desktop tabs...\n' >&2
	ERUN_E2E_ERUN_BIN="$ERUN_UI_DIR/bin/erun"
	( cd "$ERUN_UI_DIR/../erun-cli" && go build -o "$ERUN_E2E_ERUN_BIN" . )
	export ERUN_E2E_ERUN_BIN
	# erun always builds linux/amd64 + linux/arm64; register binfmt so the
	# foreign arch builds (the production preflight from #645 fails fast
	# otherwise). Idempotent and harmless when already registered.
	printf '>> playwright(k3d): registering binfmt for multi-arch builds...\n' >&2
	docker run --privileged --rm tonistiigi/binfmt --install all >/dev/null 2>&1 || true
	# Inert specs must not run against the real-tool backend; default the target
	# to the e2e dir when the caller did not pass an explicit one.
	if [ -z "$PLAYWRIGHT_ARGS" ]; then
		PLAYWRIGHT_ARGS='"tests/e2e"'
	fi
fi

PLAYWRIGHT_FLAGS=""
if [ "$HEADED" -eq 1 ]; then
	PLAYWRIGHT_FLAGS="--headed"
fi

printf '>> playwright: running tests on port %s\n' "$PORT" >&2
# shellcheck disable=SC2086
eval "\"$PLAYWRIGHT_BIN\" test $PLAYWRIGHT_FLAGS $PLAYWRIGHT_ARGS"
