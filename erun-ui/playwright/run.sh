#!/usr/bin/env sh
# Canonical entry point for the erun-ui Playwright suite.
#
# Used both interactively and by erun-ui's build/packaging flow. Builds the
# desktop binary if needed, ensures Yarn deps + bundled Chromium are present,
# then runs `playwright test` against `erun-app --headless`.
#
# Flags:
#   --build               Force a desktop-binary rebuild even when
#                         ../bin/erun-app already exists and is current.
#   --skip-build          Never rebuild, even when ../bin/erun-app is missing
#                         or stale against its sources. Use only when you
#                         know the existing binary is the one you want to
#                         test; a stale-but-present binary still prints a
#                         loud warning naming its age so the run is never
#                         silently wrong. A missing binary is always built
#                         regardless of this flag — there is nothing to run
#                         otherwise.
#
#   By default the script builds when ../bin/erun-app is missing OR older
#   than the Go/frontend sources that feed it (see build.sh), so packaging
#   pipelines that produced a current binary in an earlier step don't pay
#   the build cost twice, but a stale binary from before a rebase never gets
#   silently reused.
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

# Detach the whole run through scripts/agent-gate.sh, the same wrapper `make
# check` uses, so this suite (longer than `make check` itself) never sits as
# an ordinary foreground command for an in-pod coding agent's harness to
# auto-background into a bare task handle. Outside an agent pod agent-gate.sh
# execs straight through with no behaviour change. RUN_SH_AGENT_GATED marks
# that this invocation already made that one pass, so the re-exec of this
# same script (either agent-gate.sh's own straight-through, or the job body
# it starts) does not route through here again.
if [ -z "${AGENT_GATE_DETACHED:-}" ] && [ "${RUN_SH_AGENT_GATED:-0}" != "1" ]; then
	RUN_SH_AGENT_GATED=1
	export RUN_SH_AGENT_GATED
	exec "$ERUN_UI_DIR/../scripts/agent-gate.sh" ui-playwright "erun-ui/playwright/run.sh $*" -- "$SCRIPT_DIR/run.sh" "$@"
fi

FORCE_BUILD=0
EXPLICIT_SKIP_BUILD=0
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
			# Explicit opt-out: never rebuild a present-but-stale binary
			# (see the staleness check below). A missing binary still gets
			# built regardless, since there would be nothing to run.
			EXPLICIT_SKIP_BUILD=1
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

# Portable file-mtime-in-epoch-seconds: GNU stat (Linux) and BSD stat
# (macOS) disagree on flags, so try both and use whichever works.
mtime_epoch() {
	stat -c %Y "$1" 2>/dev/null || stat -f %m "$1" 2>/dev/null
}

# Coarse human-readable age for the "still ran the stale binary" warning
# below. Doesn't need to be precise, just enough to show the binary isn't
# new.
binary_age() {
	then_epoch=$(mtime_epoch "$1")
	if [ -z "$then_epoch" ]; then
		printf 'unknown'
		return
	fi
	secs=$(($(date +%s) - then_epoch))
	if [ "$secs" -lt 60 ]; then
		printf '%ss' "$secs"
	elif [ "$secs" -lt 3600 ]; then
		printf '%sm' "$((secs / 60))"
	else
		printf '%sh%sm' "$((secs / 3600))" "$(((secs % 3600) / 60))"
	fi
}

# Mirrors what build.sh actually reads to produce $BIN_PATH: this module's
# own Go sources (top-level + headlessserver), erun-common (unioned in via
# go.work and imported directly, including the assets it go:embeds), and
# the frontend project's source plus the build config `wails generate
# module` / `yarn build` consume. Each find is guarded so a missing
# directory can't trip `set -e` and abort the whole script.
find_stale_binary_sources() {
	find "$ERUN_UI_DIR" -maxdepth 1 -name '*.go' -newer "$BIN_PATH" -print 2>/dev/null || true
	find "$ERUN_UI_DIR/headlessserver" -name '*.go' -newer "$BIN_PATH" -print 2>/dev/null || true
	find "$ERUN_UI_DIR/../erun-common" -name '*.go' -newer "$BIN_PATH" -print 2>/dev/null || true
	find "$ERUN_UI_DIR/../erun-common/assets" -type f -newer "$BIN_PATH" -print 2>/dev/null || true
	find "$ERUN_UI_DIR/frontend/src" -type f -newer "$BIN_PATH" -print 2>/dev/null || true
	find "$ERUN_UI_DIR/frontend/index.html" "$ERUN_UI_DIR/frontend/vite.config.ts" \
		"$ERUN_UI_DIR/frontend/package.json" "$ERUN_UI_DIR/frontend/tsconfig.json" \
		-newer "$BIN_PATH" -print 2>/dev/null || true
}

# Build the desktop binary so the webServer fixture has something to spawn.
# By default we build when the binary is missing OR older than the sources
# that feed it, so a binary built before a rebase never silently tests the
# rebase's specs against pre-rebase code. Packaging flows that already
# produced a current binary in an earlier step still skip the cost.
BUILD_NEEDED=0
STALE_ONLY=0
BUILD_REASON=""
if [ "$FORCE_BUILD" -eq 1 ]; then
	BUILD_NEEDED=1
	BUILD_REASON="--build was passed"
elif [ ! -x "$BIN_PATH" ]; then
	BUILD_NEEDED=1
	BUILD_REASON="$BIN_PATH does not exist"
elif STALE_SOURCE=$(find_stale_binary_sources | head -n1) && [ -n "$STALE_SOURCE" ]; then
	BUILD_NEEDED=1
	STALE_ONLY=1
	BUILD_REASON="$BIN_PATH predates $STALE_SOURCE"
fi

if [ "$BUILD_NEEDED" -eq 1 ] && [ "$STALE_ONLY" -eq 1 ] && [ "$EXPLICIT_SKIP_BUILD" -eq 1 ]; then
	printf '>> playwright: WARNING: %s is stale (%s), binary age %s, but --skip-build was passed — running the stale binary anyway.\n' "$BIN_PATH" "$BUILD_REASON" "$(binary_age "$BIN_PATH")" >&2
	BUILD_NEEDED=0
fi

if [ "$BUILD_NEEDED" -eq 1 ]; then
	printf '>> playwright: building %s (%s)...\n' "$BIN_PATH" "$BUILD_REASON" >&2
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

# reap_process_group kills anything still alive in this script's own process
# group before it exits. The seeded PATH stub for `erun open` (fixtures/
# seedRoot.ts) execs `sleep` to hold a tab's session open for the life of the
# suite, and that child is never explicitly waited on or killed by anything
# that tears the suite down -- stopping the headless backend it belongs to
# only waits for the backend's own process to exit, not its children. Left
# alone, that orphan stays a member of this script's process group long after
# this script itself is done, and a supervisor watching this script for
# exactly that shape (background work started and never waited for) records
# a fully passing run as abandoned.
#
# Only ever acts when this script's own pid is the process group's own
# leader: that is true whenever something gave this invocation a fresh group
# (a supervisor's Setpgid, or ordinary job-control launching it as its own
# foreground job), and in that case every other member is provably this
# script's own descendant -- nothing it would be unsafe to signal. When it is
# not the leader (this pgid predates this script, e.g. a caller's shell with
# job control off), this is a no-op rather than a guess at what else shares
# that group.
reap_process_group() {
	own_pgid=$(ps -axo pid=,pgid= 2>/dev/null | awk -v me="$$" '$1==me {print $2}')
	if [ -z "$own_pgid" ] || [ "$own_pgid" != "$$" ]; then
		return 0
	fi
	survivors() {
		ps -axo pid=,pgid= 2>/dev/null | awk -v pg="$own_pgid" -v me="$$" '$2==pg && $1!=me {print $1}'
	}
	pids=$(survivors)
	[ -n "$pids" ] || return 0
	# shellcheck disable=SC2086
	kill -TERM $pids 2>/dev/null || true
	settle_attempts=0
	while [ "$settle_attempts" -lt 20 ]; do
		pids=$(survivors)
		[ -z "$pids" ] && return 0
		sleep 0.05
		settle_attempts=$((settle_attempts + 1))
	done
	# shellcheck disable=SC2086
	kill -KILL $pids 2>/dev/null || true
}

trap 'reap_process_group; cleanup_isolated_home' EXIT

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
