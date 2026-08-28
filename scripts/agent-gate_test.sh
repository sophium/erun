#!/bin/sh

# Tests for agent-gate.sh's own control flow: outside an agent pod (or once
# the recursion guard is set) it must exec the real command untouched; inside
# one it must detach through `erun exec job start`/`await`/`output` with the
# right arguments and propagate the job's real exit status and output.
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
# (one line per call) and answers `exec job start`/`await`/`output` per the
# STUB_* env vars a test case sets, without ever touching a real job store.
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

echo "ok: agent-gate.sh"
