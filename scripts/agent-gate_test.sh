#!/bin/sh

# Tests for agent-gate.sh's own control flow: outside an agent pod (or once
# the recursion guard is set) it must exec the real command untouched; inside
# one it must detach through `erun exec job start`/`await`/`output` with the
# right arguments and propagate the job's real exit status and output. It also
# covers erun-ui/playwright/run.sh's wiring into that same wrapper, and the
# job-status probe that decides whether to replay a finished job's outcome or
# start a fresh run.
#
# Run directly (not wired into `make check`, same reasoning as
# erun-devops/docker/erun-devops/entrypoint_test.sh): a stub `erun` on PATH
# stands in for the real job engine so these assert argv shape and exit-code
# plumbing, not the job store itself.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
gate="${script_dir}/agent-gate.sh"

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t agent-gate-test)"
trap 'rm -rf "${work_root}"' EXIT INT TERM

fail() {
	echo "FAIL: $1" >&2
	exit 1
}

# stub_erun writes a fake `erun` on PATH that records every invocation's argv
# (one line per call) and answers `exec job start`/`await`/`output`/`status`
# per the STUB_* env vars a test case sets, without ever touching a real job
# store. `exec job status` defaults to "no job" (status 1) so every existing
# case that never sets STUB_STATUS_* keeps behaving as if no prior record
# existed, matching what a first invocation actually sees.
stub_erun() {
	bin_dir="$1"
	mkdir -p "$bin_dir"
	cat >"${bin_dir}/erun" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$STUB_ARGV_FILE"
case "$1 $2 $3" in
"exec job start")
	if [ -n "${STUB_START_STDERR:-}" ]; then
		printf '%s' "$STUB_START_STDERR" >&2
	fi
	exit "${STUB_START_STATUS:-0}"
	;;
"exec job await")
	exit "${STUB_AWAIT_STATUS:-0}"
	;;
"exec job output")
	printf '%s' "${STUB_JOB_OUTPUT:-}"
	exit "${STUB_OUTPUT_STATUS:-0}"
	;;
"exec job status")
	if [ "${STUB_STATUS_STATUS:-1}" -eq 0 ]; then
		printf '%s\n' "${STUB_STATUS_LINE:-}"
	fi
	exit "${STUB_STATUS_STATUS:-1}"
	;;
*)
	exit 0
	;;
esac
EOF
	chmod +x "${bin_dir}/erun"
}

# run_gate invokes agent-gate.sh with a clean environment plus whatever the
# caller exported, capturing stdout+stderr and the exit code without letting
# `set -e` end the test on a nonzero (expected in several cases).
run_gate() {
	set +e
	OUT=$("$gate" "$@" 2>&1)
	STATUS=$?
	set -e
}

# --- outside an agent pod: exec the real command untouched, erun never runs.
case_dir="${work_root}/outside-agent"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun "${case_dir}/bin"
(
	unset ERUN_ENV_TYPE AGENT_GATE_DETACHED
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	run_gate direct-id "direct name" -- sh -c 'echo direct output; exit 7'
	[ "$STATUS" -eq 7 ] || fail "outside agent pod: expected exit 7, got $STATUS"
	case "$OUT" in
	*"direct output"*) ;;
	*) fail "outside agent pod: expected the command's own output, got: $OUT" ;;
	esac
	if [ -s "$STUB_ARGV_FILE" ]; then
		fail "outside agent pod: erun must never be invoked, argv was: $(cat "$STUB_ARGV_FILE")"
	fi
)

# --- the recursion guard: even inside an agent pod, a job already marked
# AGENT_GATE_DETACHED=1 must run the real command directly.
case_dir="${work_root}/detached-guard"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun "${case_dir}/bin"
(
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	export ERUN_ENV_TYPE=remote-agent AGENT_GATE_DETACHED=1
	export ERUN_TENANT=t ERUN_ENVIRONMENT=e
	run_gate guarded-id "guarded name" -- sh -c 'echo guarded output; exit 3'
	[ "$STATUS" -eq 3 ] || fail "detached guard: expected exit 3, got $STATUS"
	case "$OUT" in
	*"guarded output"*) ;;
	*) fail "detached guard: expected the command's own output, got: $OUT" ;;
	esac
	if [ -s "$STUB_ARGV_FILE" ]; then
		fail "detached guard: erun must never be invoked, argv was: $(cat "$STUB_ARGV_FILE")"
	fi
)

# --- inside an agent pod: detach through start/await/output, with the right
# tenant/environment/id, and propagate the job's captured output and exit code.
case_dir="${work_root}/happy-path"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun "${case_dir}/bin"
(
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	export ERUN_ENV_TYPE=local-agent
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev
	export STUB_AWAIT_STATUS=0
	export STUB_JOB_OUTPUT='job output line'
	run_gate check "make check" -- make check-gate
	[ "$STATUS" -eq 0 ] || fail "happy path: expected exit 0, got $STATUS ($OUT)"
	case "$OUT" in
	*"job output line"*) ;;
	*) fail "happy path: expected the job's captured output, got: $OUT" ;;
	esac
	grep -q 'exec job start' "$STUB_ARGV_FILE" || fail "happy path: job start was never called"
	grep -q -- '--tenant acme' "$STUB_ARGV_FILE" || fail "happy path: tenant was not passed through"
	grep -q -- '--environment dev' "$STUB_ARGV_FILE" || fail "happy path: environment was not passed through"
	grep -q -- '--id check' "$STUB_ARGV_FILE" || fail "happy path: job id was not passed through"
	grep -q -- '--env AGENT_GATE_DETACHED=1' "$STUB_ARGV_FILE" || fail "happy path: recursion guard was not threaded into the job's env"
	grep -q -- 'make check-gate' "$STUB_ARGV_FILE" || fail "happy path: the real command was not forwarded to job start"
	grep -q 'exec job await' "$STUB_ARGV_FILE" || fail "happy path: job await was never called"
	grep -q 'exec job output' "$STUB_ARGV_FILE" || fail "happy path: job output was never read back"
)

# --- a job already running from a prior invocation: start refuses, and the
# script must fall through to await rather than treating that as fatal.
case_dir="${work_root}/already-running"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun "${case_dir}/bin"
(
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	export ERUN_ENV_TYPE=remote-agent
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev
	export STUB_START_STATUS=1
	export STUB_START_STDERR='job "check" is already running (pid 123); pass a different id or cancel it first'
	export STUB_AWAIT_STATUS=0
	export STUB_JOB_OUTPUT='resumed output'
	run_gate check "make check" -- make check-gate
	[ "$STATUS" -eq 0 ] || fail "already running: expected exit 0, got $STATUS ($OUT)"
	case "$OUT" in
	*"resumed output"*) ;;
	*) fail "already running: expected the running job's output once it finished, got: $OUT" ;;
	esac
	grep -q 'exec job await' "$STUB_ARGV_FILE" || fail "already running: must still await the in-flight job"
)

# --- the job-status probe reports "running": the start/await/output flow
# below must still run untouched (start falls through on "already running"
# exactly as the case above), never treating a running job as finished.
case_dir="${work_root}/status-running"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun "${case_dir}/bin"
(
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	export ERUN_ENV_TYPE=remote-agent
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev
	export STUB_STATUS_STATUS=0
	export STUB_STATUS_LINE='running: make check, pid 123'
	export STUB_START_STATUS=1
	export STUB_START_STDERR='job "check" is already running (pid 123); pass a different id or cancel it first'
	export STUB_AWAIT_STATUS=0
	export STUB_JOB_OUTPUT='resumed output'
	run_gate check "make check" -- make check-gate
	[ "$STATUS" -eq 0 ] || fail "status running: expected exit 0, got $STATUS ($OUT)"
	case "$OUT" in
	*"resumed output"*) ;;
	*) fail "status running: expected the running job's output once it finished, got: $OUT" ;;
	esac
	grep -q 'exec job start' "$STUB_ARGV_FILE" || fail "status running: must still start/attach through the normal flow"
	if grep -q -- '--timeout 1s' "$STUB_ARGV_FILE"; then
		fail "status running: must not take the finished-replay path for a job still running"
	fi
)

# --- a finished job's outcome is replayed rather than starting a new run.
# `exec job start` must never be called, since that is what discards the
# finished record.
case_dir="${work_root}/status-finished-pass"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun "${case_dir}/bin"
(
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	export ERUN_ENV_TYPE=local-agent
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev
	export STUB_STATUS_STATUS=0
	export STUB_STATUS_LINE='exited 0: make check'
	export STUB_AWAIT_STATUS=0
	export STUB_JOB_OUTPUT='earlier passing output'
	run_gate check "make check" -- make check-gate
	[ "$STATUS" -eq 0 ] || fail "status finished pass: expected exit 0, got $STATUS ($OUT)"
	case "$OUT" in
	*"earlier passing output"*) ;;
	*) fail "status finished pass: expected the finished job's captured output, got: $OUT" ;;
	esac
	case "$OUT" in
	*"already finished"*) ;;
	*) fail "status finished pass: expected the replay notice, got: $OUT" ;;
	esac
	if grep -q 'exec job start' "$STUB_ARGV_FILE"; then
		fail "status finished pass: must never start a new run over a finished record"
	fi
	grep -q -- '--timeout 1s' "$STUB_ARGV_FILE" || fail "status finished pass: must await the finished job to learn its real exit status"
)

# --- a FAILING finished job replays as failing, not as a fresh pass. This is
# the regression that matters most: replaying a stale pass over a
# since-broken tree would be worse than the original bug.
case_dir="${work_root}/status-finished-fail"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun "${case_dir}/bin"
(
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	export ERUN_ENV_TYPE=local-agent
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev
	export STUB_STATUS_STATUS=0
	export STUB_STATUS_LINE='exited 7: make check'
	export STUB_AWAIT_STATUS=1
	export STUB_JOB_OUTPUT='earlier failing output'
	run_gate check "make check" -- make check-gate
	[ "$STATUS" -eq 1 ] || fail "status finished fail: expected the replayed failure to exit nonzero, got $STATUS ($OUT)"
	case "$OUT" in
	*"earlier failing output"*) ;;
	*) fail "status finished fail: expected the finished job's captured output, got: $OUT" ;;
	esac
	if grep -q 'exec job start' "$STUB_ARGV_FILE"; then
		fail "status finished fail: must never start a new run over a finished record"
	fi
)

# --- AGENT_GATE_RERUN=1 skips the finished-job replay and starts a genuinely
# new run, so a caller who changed the tree can still force a fresh result.
case_dir="${work_root}/status-forced-rerun"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun "${case_dir}/bin"
(
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	export ERUN_ENV_TYPE=local-agent AGENT_GATE_RERUN=1
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev
	export STUB_STATUS_STATUS=0
	export STUB_STATUS_LINE='exited 0: make check'
	export STUB_AWAIT_STATUS=0
	export STUB_JOB_OUTPUT='fresh output'
	run_gate check "make check" -- make check-gate
	[ "$STATUS" -eq 0 ] || fail "forced rerun: expected exit 0, got $STATUS ($OUT)"
	case "$OUT" in
	*"fresh output"*) ;;
	*) fail "forced rerun: expected the fresh run's captured output, got: $OUT" ;;
	esac
	grep -q 'exec job start' "$STUB_ARGV_FILE" || fail "forced rerun: must start a new run when AGENT_GATE_RERUN=1"
	if grep -q 'exec job status' "$STUB_ARGV_FILE"; then
		fail "forced rerun: must not even probe status when a fresh run was explicitly requested"
	fi
)

# --- an unrelated start failure must be fatal: never silently await a job
# that was never actually started.
case_dir="${work_root}/start-fails"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun "${case_dir}/bin"
(
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	export ERUN_ENV_TYPE=remote-agent
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev
	export STUB_START_STATUS=1
	export STUB_START_STDERR='some unrelated failure'
	run_gate check "make check" -- make check-gate
	[ "$STATUS" -eq 1 ] || fail "start fails: expected exit 1, got $STATUS ($OUT)"
	case "$OUT" in
	*"some unrelated failure"*) ;;
	*) fail "start fails: expected the start error to surface, got: $OUT" ;;
	esac
	if grep -q 'exec job await' "$STUB_ARGV_FILE"; then
		fail "start fails: must not await a job that was never started"
	fi
)

# --- await timing out must exit 124 and must not read job output yet.
case_dir="${work_root}/still-running"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun "${case_dir}/bin"
(
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	export ERUN_ENV_TYPE=local-agent
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev
	export STUB_AWAIT_STATUS=124
	run_gate check "make check" -- make check-gate
	[ "$STATUS" -eq 124 ] || fail "still running: expected exit 124, got $STATUS ($OUT)"
	case "$OUT" in
	*"run this command again"*) ;;
	*) fail "still running: expected the retry hint, got: $OUT" ;;
	esac
	if grep -q 'exec job output' "$STUB_ARGV_FILE"; then
		fail "still running: must not read job output before the job finishes"
	fi
)

# --- erun missing from PATH: degrade to running the command directly rather
# than failing outright.
case_dir="${work_root}/no-erun"
mkdir -p "$case_dir"
(
	export PATH="/usr/bin:/bin"
	export ERUN_ENV_TYPE=local-agent
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev
	run_gate check "make check" -- sh -c 'echo no erun on PATH; exit 5'
	[ "$STATUS" -eq 5 ] || fail "no erun: expected exit 5, got $STATUS ($OUT)"
	case "$OUT" in
	*"no erun on PATH"*) ;;
	*) fail "no erun: expected the command's own output, got: $OUT" ;;
	esac
)

# --- erun-ui/playwright/run.sh: wired through the same agent-gate.sh wrapper
# as `make check`. run.sh's own body needs yarn/node/playwright to actually
# run the suite, so these cases stub only `erun` and assert on what reaches
# `erun exec job start` -- the start/await/output plumbing itself is already
# covered above; this section only checks that run.sh routes into it
# correctly and forwards its own arguments faithfully.
playwright_run_sh="${script_dir}/../erun-ui/playwright/run.sh"

run_playwright() {
	set +e
	OUT=$("$playwright_run_sh" "$@" 2>&1)
	STATUS=$?
	set -e
}

# --- outside an agent pod: run.sh must behave exactly as before. --port with
# no value fails fast inside run.sh's own flag parsing, well before it would
# ever reach yarn/playwright, so this proves both "no erun call happened" and
# "the real script body still ran in place".
case_dir="${work_root}/playwright-outside-agent"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun "${case_dir}/bin"
(
	unset ERUN_ENV_TYPE AGENT_GATE_DETACHED RUN_SH_AGENT_GATED
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	run_playwright --port
	[ "$STATUS" -eq 2 ] || fail "playwright outside agent pod: expected exit 2, got $STATUS ($OUT)"
	case "$OUT" in
	*"--port requires a value"*) ;;
	*) fail "playwright outside agent pod: expected run.sh's own error, got: $OUT" ;;
	esac
	if [ -s "$STUB_ARGV_FILE" ]; then
		fail "playwright outside agent pod: erun must never be invoked, argv was: $(cat "$STUB_ARGV_FILE")"
	fi
)

# --- the recursion guard: already inside the detached job body
# (AGENT_GATE_DETACHED=1), run.sh must not wrap itself again.
case_dir="${work_root}/playwright-detached-guard"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun "${case_dir}/bin"
(
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	export ERUN_ENV_TYPE=local-agent AGENT_GATE_DETACHED=1
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev
	unset RUN_SH_AGENT_GATED
	run_playwright --port
	[ "$STATUS" -eq 2 ] || fail "playwright detached guard: expected exit 2, got $STATUS ($OUT)"
	if [ -s "$STUB_ARGV_FILE" ]; then
		fail "playwright detached guard: erun must never be invoked, argv was: $(cat "$STUB_ARGV_FILE")"
	fi
)

# --- inside an agent pod: detach through the same start/await/output flow as
# make check, with every original argument forwarded to the re-invoked run.sh
# faithfully, and the job's captured output/exit status returned untouched.
case_dir="${work_root}/playwright-happy-path"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun "${case_dir}/bin"
(
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	export ERUN_ENV_TYPE=remote-agent
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev
	export STUB_AWAIT_STATUS=0
	export STUB_JOB_OUTPUT='playwright job output'
	unset RUN_SH_AGENT_GATED
	run_playwright --build --skip-lint -- --grep "manage dialog sizing" --reporter=list
	[ "$STATUS" -eq 0 ] || fail "playwright happy path: expected exit 0, got $STATUS ($OUT)"
	case "$OUT" in
	*"playwright job output"*) ;;
	*) fail "playwright happy path: expected the job's captured output, got: $OUT" ;;
	esac
	grep -q 'exec job start' "$STUB_ARGV_FILE" || fail "playwright happy path: job start was never called"
	grep -q -- '--id ui-playwright' "$STUB_ARGV_FILE" || fail "playwright happy path: job id was not passed through"
	grep -q -- '--env AGENT_GATE_DETACHED=1' "$STUB_ARGV_FILE" || fail "playwright happy path: recursion guard was not threaded into the job's env"
	grep -q -- 'playwright/run.sh --build --skip-lint -- --grep manage dialog sizing --reporter=list' "$STUB_ARGV_FILE" \
		|| fail "playwright happy path: original arguments were not forwarded intact, argv was: $(cat "$STUB_ARGV_FILE")"
	grep -q 'exec job await' "$STUB_ARGV_FILE" || fail "playwright happy path: job await was never called"
	grep -q 'exec job output' "$STUB_ARGV_FILE" || fail "playwright happy path: job output was never read back"
)

# --- the job id stays stable across invocations even as the arguments
# change, so a caller re-invoking run.sh with the same command after a
# timeout keeps awaiting the same job rather than starting a new one.
case_dir="${work_root}/playwright-stable-id"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun "${case_dir}/bin"
(
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	export ERUN_ENV_TYPE=local-agent
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev
	export STUB_AWAIT_STATUS=0

	unset RUN_SH_AGENT_GATED
	export STUB_JOB_OUTPUT=first
	run_playwright --grep "spec one" >/dev/null

	unset RUN_SH_AGENT_GATED
	export STUB_JOB_OUTPUT=second
	run_playwright --headed --grep "spec two" >/dev/null

	starts=$(grep -c -- 'exec job start.*--id ui-playwright' "$STUB_ARGV_FILE" || true)
	[ "$starts" -eq 2 ] || fail "playwright stable id: expected both invocations to start a job with the same id, argv was: $(cat "$STUB_ARGV_FILE")"
	grep -q -- 'spec one' "$STUB_ARGV_FILE" || fail "playwright stable id: first invocation's arguments were lost"
	grep -q -- 'spec two' "$STUB_ARGV_FILE" || fail "playwright stable id: second invocation's arguments were lost"
)

echo "ok: agent-gate.sh"
echo "ok: erun-ui/playwright/run.sh detachment wiring"
