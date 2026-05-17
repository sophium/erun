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

while [ $# -gt 0 ]; do
	case "$1" in
		--build)
			FORCE_BUILD=1
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
	"$ERUN_UI_DIR/build.sh" "$BIN_PATH"
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

# `playwright install chromium` is idempotent — it checks whether the
# expected revision is already on disk and skips the download when it is.
printf '>> playwright: ensuring chromium\n' >&2
"$PLAYWRIGHT_BIN" install chromium >/dev/null

ERUN_PLAYWRIGHT_PORT="$PORT"
export ERUN_PLAYWRIGHT_PORT

PLAYWRIGHT_FLAGS=""
if [ "$HEADED" -eq 1 ]; then
	PLAYWRIGHT_FLAGS="--headed"
fi

printf '>> playwright: running tests on port %s\n' "$PORT" >&2
# shellcheck disable=SC2086
eval "\"$PLAYWRIGHT_BIN\" test $PLAYWRIGHT_FLAGS $PLAYWRIGHT_ARGS"
