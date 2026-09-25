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
	# The capability probe agent-gate.sh reads --exclusive support off. Defaults
	# to supporting it, so every existing case keeps taking the claim; a case
	# that sets STUB_NO_EXCLUSIVE=1 stands in for an environment whose installed
	# erun predates the flag.
	for a in "$@"; do
		if [ "$a" = "--help" ]; then
			if [ "${STUB_NO_EXCLUSIVE:-}" != "1" ]; then
				printf '      --exclusive   Claim the environment for this job\n'
			fi
			exit 0
		fi
	done
	# A case that sets STUB_STATUS_LINE_FILE gets the job's recorded line
	# written out the way a real store records it -- the name this start was
	# given, which is where agent-gate.sh's environment marker rides. A case
	# that needs a record it can trust as *this* environment's derives it from
	# a real invocation rather than reproducing the key by hand. It is written
	# to a file rather than handed back through the environment on purpose: the
	# key covers the whole environment, so anything a case exports between two
	# invocations would read as a different run to the very check under test.
	if [ -n "${STUB_STATUS_LINE_FILE:-}" ]; then
		prev=""
		for a in "$@"; do
			if [ "$prev" = "--name" ]; then
				printf 'exited %s: %s' "${STUB_START_EXIT_CODE:-0}" "$a" >"$STUB_STATUS_LINE_FILE"
			fi
			prev="$a"
		done
	fi
	if [ -n "${STUB_START_STDERR:-}" ]; then
		printf '%s' "$STUB_START_STDERR" >&2
	fi
	exit "${STUB_START_STATUS:-0}"
	;;
"exec job await")
	# A test that sets STUB_AWAIT_STATUS_QUEUE (one exit status per line) gets a
	# different answer on each successive await call, popped in order -- this is
	# what lets a test prove the AGENT_GATE_AWAIT_VERDICT=1 retry loop actually
	# re-awaits the same job across multiple calls instead of trusting the
	# first one. Falls back to the fixed STUB_AWAIT_STATUS every existing case
	# already uses when no queue is set.
	if [ -n "${STUB_AWAIT_STATUS_QUEUE:-}" ] && [ -s "$STUB_AWAIT_STATUS_QUEUE" ]; then
		line=$(head -n1 "$STUB_AWAIT_STATUS_QUEUE")
		tail -n +2 "$STUB_AWAIT_STATUS_QUEUE" >"${STUB_AWAIT_STATUS_QUEUE}.tmp"
		mv "${STUB_AWAIT_STATUS_QUEUE}.tmp" "$STUB_AWAIT_STATUS_QUEUE"
		exit "$line"
	fi
	exit "${STUB_AWAIT_STATUS:-0}"
	;;
"exec job output")
	printf '%s' "${STUB_JOB_OUTPUT:-}"
	exit "${STUB_OUTPUT_STATUS:-0}"
	;;
"exec job status")
	# A record this same stub wrote out from a real start (see
	# STUB_STATUS_LINE_FILE) is reported back verbatim, exactly as a real store
	# would report the job it recorded.
	if [ -n "${STUB_STATUS_LINE_FILE:-}" ] && [ -s "$STUB_STATUS_LINE_FILE" ]; then
		cat "$STUB_STATUS_LINE_FILE"
		printf '\n'
		exit 0
	fi
	# A test that sets STUB_STATUS_QUEUE (one "<exitstatus>|<line>" per line)
	# gets a different answer on each successive status call, popped in order.
	# The wrapper probes the record once before deciding whether to replay or
	# start, then again when a wait expires, so proving it reads the job's own
	# record *after* a timeout needs those two calls to disagree -- otherwise
	# the same answer would have to serve both.
	if [ -n "${STUB_STATUS_QUEUE:-}" ] && [ -s "$STUB_STATUS_QUEUE" ]; then
		entry=$(head -n1 "$STUB_STATUS_QUEUE")
		tail -n +2 "$STUB_STATUS_QUEUE" >"${STUB_STATUS_QUEUE}.tmp"
		mv "${STUB_STATUS_QUEUE}.tmp" "$STUB_STATUS_QUEUE"
		queue_status="${entry%%|*}"
		queue_line="${entry#*|}"
		if [ "$queue_status" -eq 0 ]; then
			printf '%s\n' "$queue_line"
		fi
		exit "$queue_status"
	fi
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

# stub_erun_stateful writes a fake `erun` that behaves like a minimal real job
# store, keyed by the exact --id value it receives: `exec job start` actually
# runs the trailing command synchronously and records its output/exit code
# under that id; `exec job status`/`await`/`output` answer from whatever is
# recorded under the id they're asked about, and report "no such job" (status
# 1) for an id nothing was ever recorded under. Unlike stub_erun above (which
# answers every call the same way regardless of id, for testing argv shape),
# this is what lets a test prove the *replay-vs-rerun* decision actually
# tracks the id agent-gate.sh computes, not just that some erun call happened.
stub_erun_stateful() {
	bin_dir="$1"
	mkdir -p "$bin_dir"
	cat >"${bin_dir}/erun" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$STUB_ARGV_FILE"
mkdir -p "$STUB_STORE_DIR"

job_id=""
job_name=""
prev=""
for a in "$@"; do
	if [ "$prev" = "--id" ]; then
		job_id="$a"
	fi
	if [ "$prev" = "--name" ]; then
		job_name="$a"
	fi
	prev="$a"
done

verb="$1 $2 $3"

if [ "$verb" = "exec job status" ]; then
	if [ -f "${STUB_STORE_DIR}/${job_id}.status" ]; then
		cat "${STUB_STORE_DIR}/${job_id}.status"
		exit 0
	fi
	exit 1
fi

if [ "$verb" = "exec job start" ]; then
	for a in "$@"; do
		if [ "$a" = "--help" ]; then
			printf '      --exclusive   Claim the environment for this job\n'
			exit 0
		fi
	done
	# Drop everything up to and including the literal "--" separator, leaving
	# only the real command's own argv (untouched, so quoting survives).
	while [ $# -gt 0 ]; do
		cur="$1"
		shift
		if [ "$cur" = "--" ]; then
			break
		fi
	done
	set +e
	out=$("$@" 2>&1)
	code=$?
	set -e
	printf '%s' "$out" >"${STUB_STORE_DIR}/${job_id}.output"
	printf '%s' "$code" >"${STUB_STORE_DIR}/${job_id}.exitcode"
	# The recorded name, reported back exactly as a real store reports it: the
	# name is where agent-gate.sh's environment marker rides, so a stub that
	# dropped it would answer every replay probe with an unattributable record.
	printf 'exited %s: %s' "$code" "$job_name" >"${STUB_STORE_DIR}/${job_id}.status"
	exit 0
fi

if [ "$verb" = "exec job await" ]; then
	if [ -f "${STUB_STORE_DIR}/${job_id}.exitcode" ]; then
		exit "$(cat "${STUB_STORE_DIR}/${job_id}.exitcode")"
	fi
	exit 0
fi

if [ "$verb" = "exec job output" ]; then
	if [ -f "${STUB_STORE_DIR}/${job_id}.output" ]; then
		cat "${STUB_STORE_DIR}/${job_id}.output"
	fi
	exit 0
fi

exit 0
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
	grep -q -- '--exclusive' "$STUB_ARGV_FILE" || fail "happy path: the gate must claim the environment exclusively, or a neighbour job can make its verdict wrong"
	grep -q -- 'make check-gate' "$STUB_ARGV_FILE" || fail "happy path: the real command was not forwarded to job start"
	grep -q 'exec job await' "$STUB_ARGV_FILE" || fail "happy path: job await was never called"
	grep -q 'exec job output' "$STUB_ARGV_FILE" || fail "happy path: job output was never read back"
)

# --- an outer `timeout` wrapping this invocation is warned about: it can only
# ever truncate the bounded await below, never extend it, so it risks killing
# exactly the gate this script exists to protect. The warning must not change
# the outcome -- the happy path underneath still runs and reports normally.
case_dir="${work_root}/outer-timeout-warning"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun "${case_dir}/bin"
if ! command -v timeout >/dev/null 2>&1; then
	echo "skip: outer-timeout-warning (no timeout(1) on this host)" >&2
else
	(
		export PATH="${case_dir}/bin:$PATH"
		export STUB_ARGV_FILE
		export ERUN_ENV_TYPE=local-agent
		export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev
		export STUB_AWAIT_STATUS=0
		export STUB_JOB_OUTPUT='job output line'
		set +e
		OUT=$(timeout 30 "$gate" check "make check" -- make check-gate 2>&1)
		STATUS=$?
		set -e
		[ "$STATUS" -eq 0 ] || fail "outer timeout warning: expected exit 0, got $STATUS ($OUT)"
		case "$OUT" in
		*"appears to run under an outer"*) ;;
		*) fail "outer timeout warning: expected the outer-timeout warning, got: $OUT" ;;
		esac
		case "$OUT" in
		*"job output line"*) ;;
		*) fail "outer timeout warning: expected the job's captured output despite the warning, got: $OUT" ;;
		esac
	)
fi

# --- no outer timeout: the warning must not fire for a plain foreground call.
case_dir="${work_root}/no-outer-timeout-no-warning"
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
	[ "$STATUS" -eq 0 ] || fail "no outer timeout: expected exit 0, got $STATUS ($OUT)"
	case "$OUT" in
	*"appears to run under an outer"*) fail "no outer timeout: warning fired with no outer timeout present: $OUT" ;;
	*) ;;
	esac
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
	export STUB_AWAIT_STATUS=0
	export STUB_JOB_OUTPUT='earlier passing output'
	export STUB_STATUS_LINE_FILE="${case_dir}/recorded-line"

	# A replayed pass has to be one this environment could have produced, so the
	# record this case replays is written by a real invocation -- the name it
	# records is what carries the environment marker -- rather than a literal
	# this case would have to keep in step with the key by hand. Nothing else is
	# exported between the two invocations below: the key covers the whole
	# environment, so an environment change here would read as a different run
	# to the very check under test. AGENT_GATE_RERUN is the one exception, and
	# only because it is this wrapper's own control -- it decides whether a
	# replay is considered at all, never what the gated command does.
	export AGENT_GATE_RERUN=1
	run_gate check "make check" -- make check-gate >/dev/null
	unset AGENT_GATE_RERUN
	[ -s "$STUB_STATUS_LINE_FILE" ] || fail "status finished pass: could not learn the line a real invocation records"
	: >"$STUB_ARGV_FILE"

	run_gate check "make check" -- make check-gate
	[ "$STATUS" -eq 0 ] || fail "status finished pass: expected exit 0, got $STATUS ($OUT)"
	case "$OUT" in
	*"earlier passing output"*) ;;
	*) fail "status finished pass: expected the finished job's captured output, got: $OUT" ;;
	esac
	case "$OUT" in
	*"replaying a cached PASS"*) ;;
	*) fail "status finished pass: expected the replay notice, got: $OUT" ;;
	esac
	replayed_id=$(grep -o -- '--id [^ ]*' "$STUB_ARGV_FILE" | head -1 | cut -d' ' -f2)
	[ -n "$replayed_id" ] || fail "status finished pass: could not recover the job id used"
	case "$OUT" in
	*"$replayed_id"*) ;;
	*) fail "status finished pass: replay notice must name the job it came from ($replayed_id), got: $OUT" ;;
	esac
	case "$OUT" in
	*"exited 0: make check"*) ;;
	*) fail "status finished pass: replay notice must include the recorded outcome, got: $OUT" ;;
	esac
	if grep -q 'exec job start' "$STUB_ARGV_FILE"; then
		fail "status finished pass: must never start a new run over a finished record"
	fi
	grep -q -- '--timeout 1s' "$STUB_ARGV_FILE" || fail "status finished pass: must await the finished job to learn its real exit status"
)

# --- a recorded pass carrying no environment marker is not attributable to any
# environment, so it is not replayed either. Records written before the marker
# existed stay readable for the store's own retention window, and a pass no one
# can attribute is exactly the case where treating it as this environment's
# would be a guess about the one input the record cannot vouch for. Refusing
# costs one fresh run; replaying it costs a verdict nobody produced.
case_dir="${work_root}/status-finished-pass-unattributable"
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
	export STUB_JOB_OUTPUT='fresh output'
	run_gate check "make check" -- make check-gate
	[ "$STATUS" -eq 0 ] || fail "status finished pass unattributable: expected exit 0, got $STATUS ($OUT)"
	case "$OUT" in
	*"fresh output"*) ;;
	*) fail "status finished pass unattributable: expected a fresh run's captured output, got: $OUT" ;;
	esac
	case "$OUT" in
	*"replaying a cached PASS"*) fail "status finished pass unattributable: must never replay a pass it cannot attribute to this environment, got: $OUT" ;;
	*) ;;
	esac
	case "$OUT" in
	*"no environment marker"*) ;;
	*) fail "status finished pass unattributable: expected the refusal to say the record carries no marker, got: $OUT" ;;
	esac
	grep -q 'exec job start' "$STUB_ARGV_FILE" || fail "status finished pass unattributable: must start a genuinely fresh run instead of replaying"
)

# --- a FAILING finished job is never replayed: a stale failure has no value
# and re-checking it is cheap, so the wrapper must start a genuinely fresh
# run instead of reporting the old failure back. This is the regression that
# matters most, in the direction that actually bit (#1798): a stale RED
# blocked good work outright, and the same replay path could just as easily
# have greened a since-broken tree.
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
	export STUB_AWAIT_STATUS=0
	export STUB_JOB_OUTPUT='fresh output'
	run_gate check "make check" -- make check-gate
	[ "$STATUS" -eq 0 ] || fail "status finished fail: expected the fresh run's own exit code, got $STATUS ($OUT)"
	case "$OUT" in
	*"fresh output"*) ;;
	*) fail "status finished fail: expected the fresh run's captured output, got: $OUT" ;;
	esac
	case "$OUT" in
	*"non-passing recorded result"*) ;;
	*) fail "status finished fail: expected the skip-replay notice, got: $OUT" ;;
	esac
	case "$OUT" in
	*"replaying a cached PASS"*) fail "status finished fail: must never report a stale failure as a replayed pass, got: $OUT" ;;
	*) ;;
	esac
	grep -q 'exec job start' "$STUB_ARGV_FILE" || fail "status finished fail: must start a fresh run rather than replaying a recorded failure"
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

# --- the id used against the job store stays identical across two
# invocations of an unchanged tree, but changes the moment a tracked file is
# edited in between. This is what makes replay safe: same id -> same
# finished record found -> replay; different id -> no record found -> fresh
# run. Runs against a real scratch git repo (not the actual checkout) so the
# tree state is fully controlled.
case_dir="${work_root}/tree-state-id"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun "${case_dir}/bin"

repo_dir="${case_dir}/repo"
mkdir -p "$repo_dir"
(
	cd "$repo_dir"
	git init -q
	git config user.email test@example.com
	git config user.name test
	echo v1 >tracked.txt
	git add tracked.txt
	git commit -q -m initial
)
(
	cd "$repo_dir"
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	export ERUN_ENV_TYPE=local-agent
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev
	export STUB_AWAIT_STATUS=0
	export STUB_JOB_OUTPUT=out

	run_gate check "make check" -- make check-gate >/dev/null
	first_id=$(grep -o -- '--id [^ ]*' "$STUB_ARGV_FILE" | head -1)

	: >"$STUB_ARGV_FILE"
	run_gate check "make check" -- make check-gate >/dev/null
	second_id=$(grep -o -- '--id [^ ]*' "$STUB_ARGV_FILE" | head -1)
	[ "$first_id" = "$second_id" ] || fail "tree state id: unchanged tree must resolve to the same id, got $first_id then $second_id"

	echo v2 >tracked.txt
	: >"$STUB_ARGV_FILE"
	run_gate check "make check" -- make check-gate >/dev/null
	third_id=$(grep -o -- '--id [^ ]*' "$STUB_ARGV_FILE" | head -1)
	[ "$third_id" != "$first_id" ] || fail "tree state id: editing a tracked file must resolve to a different id, stayed $first_id"
)

# --- the id change actually changes replay behavior end to end: a job store
# that only knows the tree's ORIGINAL id must be treated as no record found
# once the tree changes, so the wrapper runs the real command again instead
# of replaying the stale result. Uses stub_erun_stateful so the command
# genuinely does or doesn't execute, rather than asserting on argv alone.
case_dir="${work_root}/tree-state-replay"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun_stateful "${case_dir}/bin"

repo_dir="${case_dir}/repo"
mkdir -p "$repo_dir"
(
	cd "$repo_dir"
	git init -q
	git config user.email test@example.com
	git config user.name test
	echo v1 >tracked.txt
	git add tracked.txt
	git commit -q -m initial
)

counter_file="${case_dir}/counter"
: >"$counter_file"
(
	cd "$repo_dir"
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	export STUB_STORE_DIR="${case_dir}/store"
	export ERUN_ENV_TYPE=local-agent
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev

	run_gate check "make check" -- sh -c "echo run >>'${counter_file}'; cat tracked.txt"
	[ "$STATUS" -eq 0 ] || fail "tree state replay: first run expected exit 0, got $STATUS ($OUT)"
	case "$OUT" in
	*v1*) ;;
	*) fail "tree state replay: first run expected to read v1, got: $OUT" ;;
	esac
	runs=$(wc -l <"$counter_file" | tr -d ' ')
	[ "$runs" -eq 1 ] || fail "tree state replay: first run must actually execute the command, ran $runs times"

	# Same tree, same command: must replay the recorded outcome rather than
	# executing the command again.
	run_gate check "make check" -- sh -c "echo run >>'${counter_file}'; cat tracked.txt"
	[ "$STATUS" -eq 0 ] || fail "tree state replay: replay expected exit 0, got $STATUS ($OUT)"
	case "$OUT" in
	*v1*) ;;
	*) fail "tree state replay: replay expected the recorded v1 output, got: $OUT" ;;
	esac
	runs=$(wc -l <"$counter_file" | tr -d ' ')
	[ "$runs" -eq 1 ] || fail "tree state replay: unchanged tree must replay, not re-execute, ran $runs times"

	# Edit the tracked file: the same job name must now resolve to a fresh id,
	# find no record under it, and actually re-run and pick up v2.
	echo v2 >tracked.txt
	run_gate check "make check" -- sh -c "echo run >>'${counter_file}'; cat tracked.txt"
	[ "$STATUS" -eq 0 ] || fail "tree state replay: rerun after edit expected exit 0, got $STATUS ($OUT)"
	case "$OUT" in
	*v2*) ;;
	*) fail "tree state replay: changed tree must re-run and read v2, got: $OUT" ;;
	esac
	runs=$(wc -l <"$counter_file" | tr -d ' ')
	[ "$runs" -eq 2 ] || fail "tree state replay: changed tree must actually re-execute the command instead of replaying the stale v1 result, ran $runs times"
)

# --- the id used against the job store also changes with the exact command
# being gated, holding the tree and job id fixed. This is what stops a
# narrower run (e.g. a focused lint) from satisfying a later request for the
# full suite under the same job id, the second failure mode reported in
# #1798 alongside the stale-red replay.
case_dir="${work_root}/cmd-state-id"
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
	export STUB_JOB_OUTPUT=out

	run_gate check "make check" -- make check-gate >/dev/null
	full_id=$(grep -o -- '--id [^ ]*' "$STUB_ARGV_FILE" | head -1)

	: >"$STUB_ARGV_FILE"
	run_gate check "make check" -- make lint >/dev/null
	narrow_id=$(grep -o -- '--id [^ ]*' "$STUB_ARGV_FILE" | head -1)
	[ "$full_id" != "$narrow_id" ] || fail "cmd state id: a full and a narrower command under the same job id must resolve to different ids, both got $full_id"

	: >"$STUB_ARGV_FILE"
	run_gate check "make check" -- make check-gate >/dev/null
	full_id_again=$(grep -o -- '--id [^ ]*' "$STUB_ARGV_FILE" | head -1)
	[ "$full_id" = "$full_id_again" ] || fail "cmd state id: the same command must keep resolving to the same id, got $full_id then $full_id_again"
)

# --- end to end: a job store that only knows a narrower command's id must be
# treated as no record found once the full command is requested, so the
# wrapper actually re-runs the full command instead of replaying the
# narrower run's result under the same job id.
case_dir="${work_root}/cmd-state-replay"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun_stateful "${case_dir}/bin"
(
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	export STUB_STORE_DIR="${case_dir}/store"
	export ERUN_ENV_TYPE=local-agent
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev

	run_gate check "make check" -- sh -c 'echo narrow result'
	[ "$STATUS" -eq 0 ] || fail "cmd state replay: narrow run expected exit 0, got $STATUS ($OUT)"
	case "$OUT" in
	*"narrow result"*) ;;
	*) fail "cmd state replay: narrow run expected its own output, got: $OUT" ;;
	esac

	# Same job id, a broader command: must not replay the narrow run's
	# recorded result -- must actually execute the full command.
	run_gate check "make check" -- sh -c 'echo full result'
	[ "$STATUS" -eq 0 ] || fail "cmd state replay: full run expected exit 0, got $STATUS ($OUT)"
	case "$OUT" in
	*"full result"*) ;;
	*) fail "cmd state replay: full run must actually execute rather than replaying the narrow run's result, got: $OUT" ;;
	esac
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

# --- the environment is held exclusively by other work: the start is refused,
# and the wrapper must surface that refusal and stop. Awaiting here would be
# the worst outcome available -- the job was never started, so the await would
# resolve against some other record (or nothing) and report a verdict this
# gate never produced.
case_dir="${work_root}/exclusively-held"
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
	export STUB_START_STDERR='refusing to start: this environment is held exclusively by orchestrator other-gate (make check, lease id job-exclusive-check-abc)'
	run_gate check "make check" -- make check-gate
	[ "$STATUS" -eq 1 ] || fail "exclusively held: expected exit 1, got $STATUS ($OUT)"
	case "$OUT" in
	*"held exclusively by orchestrator other-gate"*) ;;
	*) fail "exclusively held: the refusal must surface verbatim so the holder is named, got: $OUT" ;;
	esac
	case "$OUT" in
	*"was not started"*) ;;
	*) fail "exclusively held: expected the wrapper to say the gate never started, got: $OUT" ;;
	esac
	if grep -q 'exec job await' "$STUB_ARGV_FILE"; then
		fail "exclusively held: must not await a job the refusal means was never started"
	fi
)

# --- an environment whose installed erun predates --exclusive: the gate must
# still run, warn that it is unprotected, and never pass a flag that binary
# would reject. This script runs against whatever erun the pod has, so failing
# closed here would take the gate out entirely on every environment that has
# not upgraded yet.
case_dir="${work_root}/no-exclusive-support"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun "${case_dir}/bin"
(
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	export ERUN_ENV_TYPE=local-agent
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev
	export STUB_NO_EXCLUSIVE=1
	export STUB_AWAIT_STATUS=0
	export STUB_JOB_OUTPUT='job output line'
	run_gate check "make check" -- make check-gate
	[ "$STATUS" -eq 0 ] || fail "no exclusive support: expected exit 0, got $STATUS ($OUT)"
	case "$OUT" in
	*"job output line"*) ;;
	*) fail "no exclusive support: the gate must still run, got: $OUT" ;;
	esac
	case "$OUT" in
	*"predates"*) ;;
	*) fail "no exclusive support: expected a loud warning that the gate is unprotected, got: $OUT" ;;
	esac
	if grep -v -- '--help' "$STUB_ARGV_FILE" | grep -q -- '--exclusive'; then
		fail "no exclusive support: must not pass a flag the installed erun would reject, argv was: $(cat "$STUB_ARGV_FILE")"
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

# --- a genuine orphan: the job exited 0 but left unsupervised
# background work running, so `job await` reports it nonzero (state
# "abandoned" is never Succeeded, see environmentJobSucceeded in
# erun-common/job.go) exactly like a real failure would. The wrapper must
# still tell them apart by re-reading the job record: this must PASS (exit 0)
# with a named, unmissable warning, not fail like a real red.
case_dir="${work_root}/abandoned-clean-exit-passes"
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
	export STUB_STATUS_LINE='abandoned 0: make check (background work left running: pid 4242 chromium)'
	export STUB_AWAIT_STATUS=1
	export STUB_JOB_OUTPUT='job output line'
	run_gate check "make check" -- make check-gate
	[ "$STATUS" -eq 0 ] || fail "abandoned clean exit: expected exit 0 (pass), got $STATUS ($OUT)"
	case "$OUT" in
	*"job output line"*) ;;
	*) fail "abandoned clean exit: expected the job's captured output, got: $OUT" ;;
	esac
	case "$OUT" in
	*"WARNING"*"left background work running behind it"*) ;;
	*) fail "abandoned clean exit: expected a named, unmissable orphan warning, got: $OUT" ;;
	esac
	case "$OUT" in
	*"pid 4242 chromium"*) ;;
	*) fail "abandoned clean exit: warning must name the actual leftover work, got: $OUT" ;;
	esac
)

# --- a genuine failure (real nonzero gate exit) must still fail. This is the
# case the fix above must never widen: only a clean exit (0) with leftover
# work passes, not every nonzero await outcome.
case_dir="${work_root}/genuine-failure-still-fails"
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
	export STUB_STATUS_LINE='exited 1: make check'
	export STUB_AWAIT_STATUS=1
	export STUB_JOB_OUTPUT='job output line'
	run_gate check "make check" -- make check-gate
	[ "$STATUS" -eq 1 ] || fail "genuine failure: expected exit 1 (fail), got $STATUS ($OUT)"
	case "$OUT" in
	*"WARNING"*) fail "genuine failure: must never print the orphan-pass warning for a real failure, got: $OUT" ;;
	*) ;;
	esac
)

# --- an abandoned job whose gated command itself exited nonzero must still
# fail: leftover work never turns a real failure into a pass, only a clean
# (exit 0) abandoned run does.
case_dir="${work_root}/abandoned-nonzero-exit-still-fails"
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
	export STUB_STATUS_LINE='abandoned 2: make check (background work left running: pid 4242 chromium)'
	export STUB_AWAIT_STATUS=1
	export STUB_JOB_OUTPUT='job output line'
	run_gate check "make check" -- make check-gate
	[ "$STATUS" -eq 1 ] || fail "abandoned nonzero exit: expected exit 1 (fail), got $STATUS ($OUT)"
	case "$OUT" in
	*"WARNING"*) fail "abandoned nonzero exit: must never print the orphan-pass warning when the gate itself failed, got: $OUT" ;;
	*) ;;
	esac
)

# --- with AGENT_GATE_AWAIT_VERDICT=1, a gate whose wait times out twice
# before it actually passes must report the real pass, not the intermediate
# timeout -- `make` collapses any nonzero recipe exit to its own generic exit
# 2, so a caller reading only `make check`'s own exit status (rather than
# re-invoking this script or the named inner job id) cannot tell a timeout
# from a real failure once this script reports 124 for a job that was,
# moments later, actually green.
case_dir="${work_root}/await-verdict-eventual-pass"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun "${case_dir}/bin"
(
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	export ERUN_ENV_TYPE=local-agent
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev
	export AGENT_GATE_AWAIT_VERDICT=1
	export STUB_AWAIT_STATUS_QUEUE="${case_dir}/await-queue"
	printf '124\n124\n0\n' >"$STUB_AWAIT_STATUS_QUEUE"
	export STUB_JOB_OUTPUT='job finished output'
	run_gate check "make check" -- make check-gate
	[ "$STATUS" -eq 0 ] || fail "await verdict eventual pass: expected the real pass (exit 0), got $STATUS ($OUT)"
	case "$OUT" in
	*"job finished output"*) ;;
	*) fail "await verdict eventual pass: expected the job's captured output, got: $OUT" ;;
	esac
	case "$OUT" in
	*"AGENT_GATE_AWAIT_VERDICT=1 is set"*) ;;
	*) fail "await verdict eventual pass: expected the keep-waiting notice, got: $OUT" ;;
	esac
	awaits=$(grep -c 'exec job await' "$STUB_ARGV_FILE")
	[ "$awaits" -eq 3 ] || fail "await verdict eventual pass: expected 3 await calls (2 timeouts + 1 real verdict), got $awaits, argv was: $(cat "$STUB_ARGV_FILE")"
)

# --- with AGENT_GATE_AWAIT_VERDICT=1, a gate whose wait times out once
# before it genuinely fails must still report that failure -- the previous
# case's fix must not over-correct into swallowing a real red as a pass.
case_dir="${work_root}/await-verdict-eventual-failure"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun "${case_dir}/bin"
(
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	export ERUN_ENV_TYPE=local-agent
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev
	export AGENT_GATE_AWAIT_VERDICT=1
	export STUB_AWAIT_STATUS_QUEUE="${case_dir}/await-queue"
	printf '124\n7\n' >"$STUB_AWAIT_STATUS_QUEUE"
	export STUB_JOB_OUTPUT='job failed output'
	run_gate check "make check" -- make check-gate
	[ "$STATUS" -eq 7 ] || fail "await verdict eventual failure: expected the real failure's own exit code (7), got $STATUS ($OUT)"
	case "$OUT" in
	*"job failed output"*) ;;
	*) fail "await verdict eventual failure: expected the failed job's captured output, got: $OUT" ;;
	esac
	awaits=$(grep -c 'exec job await' "$STUB_ARGV_FILE")
	[ "$awaits" -eq 2 ] || fail "await verdict eventual failure: expected 2 await calls (1 timeout + 1 real verdict), got $awaits, argv was: $(cat "$STUB_ARGV_FILE")"
)

# --- without AGENT_GATE_AWAIT_VERDICT=1, a timeout must still bail out
# immediately even when a later await in the same sequence would have shown a
# real pass -- the opt-in must never change the default, foreground-safe
# behaviour that protects a coding agent's own bounded tool-call window.
case_dir="${work_root}/await-verdict-not-set-still-bails-immediately"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun "${case_dir}/bin"
(
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	export ERUN_ENV_TYPE=local-agent
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev
	unset AGENT_GATE_AWAIT_VERDICT
	export STUB_AWAIT_STATUS_QUEUE="${case_dir}/await-queue"
	printf '124\n0\n' >"$STUB_AWAIT_STATUS_QUEUE"
	run_gate check "make check" -- make check-gate
	[ "$STATUS" -eq 124 ] || fail "await verdict not set: expected exit 124 without opting in, got $STATUS ($OUT)"
	awaits=$(grep -c 'exec job await' "$STUB_ARGV_FILE")
	[ "$awaits" -eq 1 ] || fail "await verdict not set: must not peek at a later await result, expected 1 call, got $awaits"
)

# --- a wait that expires on a job which has, by the time the wait gives up,
# actually PASSED must report that pass. The wait's own 124 is a deadline, not
# an outcome, and the two are independent -- so before this expiry is allowed
# to stand for a result, the job's own record is read. Reporting 124 here is
# the reported bug exactly: a gate reported red for work that was green, with
# nothing in the exit code telling the caller which it was.
case_dir="${work_root}/timeout-then-verdict-pass"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun "${case_dir}/bin"
(
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	export ERUN_ENV_TYPE=local-agent
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev
	unset AGENT_GATE_AWAIT_VERDICT
	export STUB_AWAIT_STATUS=124
	export STUB_JOB_OUTPUT='late pass output'
	# First status call is the pre-start probe and finds no record; the second
	# is the one the wrapper makes once its wait has expired.
	export STUB_STATUS_QUEUE="${case_dir}/status-queue"
	printf '1|\n0|exited 0: make check\n' >"$STUB_STATUS_QUEUE"
	run_gate check "make check" -- make check-gate
	[ "$STATUS" -eq 0 ] || fail "timeout then verdict pass: expected the job's real pass (exit 0), got $STATUS ($OUT)"
	case "$OUT" in
	*"late pass output"*) ;;
	*) fail "timeout then verdict pass: expected the job's captured output, got: $OUT" ;;
	esac
	case "$OUT" in
	*"has reached no verdict"*) fail "timeout then verdict pass: must not report a non-verdict for a job that reached one, got: $OUT" ;;
	*) ;;
	esac
)

# --- the same expiry on a job that has actually FAILED must report that
# failure, and report it as the job's own outcome rather than as a bare
# timeout: the record is read and the job's output follows.
case_dir="${work_root}/timeout-then-verdict-failure"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun "${case_dir}/bin"
(
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	export ERUN_ENV_TYPE=local-agent
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev
	unset AGENT_GATE_AWAIT_VERDICT
	export STUB_AWAIT_STATUS=124
	export STUB_JOB_OUTPUT='late failure output'
	export STUB_STATUS_QUEUE="${case_dir}/status-queue"
	# A record exists, so `job status` itself succeeds (0) even though the job
	# it reports on failed -- the two exit statuses are unrelated.
	printf '1|\n0|exited 1: make check\n' >"$STUB_STATUS_QUEUE"
	run_gate check "make check" -- make check-gate
	[ "$STATUS" -eq 1 ] || fail "timeout then verdict failure: expected the job's real failure (exit 1), got $STATUS ($OUT)"
	case "$OUT" in
	*"late failure output"*) ;;
	*) fail "timeout then verdict failure: expected the failed job's captured output, got: $OUT" ;;
	esac
)

# --- a wait that expires with the job genuinely still running is a
# non-verdict, and must say so: exit 124 (never a gate failure), name the job
# a caller has to query, and tell them re-invoking re-attaches rather than
# starting the gated work over. This is the third outcome, and the one the
# issue is about -- it must be impossible to read as "your change is broken".
case_dir="${work_root}/timeout-with-no-verdict"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun "${case_dir}/bin"
(
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE
	export ERUN_ENV_TYPE=local-agent
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev
	unset AGENT_GATE_AWAIT_VERDICT
	export STUB_AWAIT_STATUS=124
	export STUB_STATUS_QUEUE="${case_dir}/status-queue"
	printf '1|\n0|running: make check\n' >"$STUB_STATUS_QUEUE"
	run_gate check "make check" -- make check-gate
	[ "$STATUS" -eq 124 ] || fail "timeout with no verdict: expected exit 124, got $STATUS ($OUT)"
	job_id=$(grep 'exec job start' "$STUB_ARGV_FILE" | sed -n 's/.*--id \([^ ]*\).*/\1/p')
	[ -n "$job_id" ] || fail "timeout with no verdict: could not read the job id the wrapper started"
	case "$OUT" in
	*"$job_id"*) ;;
	*) fail "timeout with no verdict: must name the job a caller can query, expected $job_id in: $OUT" ;;
	esac
	case "$OUT" in
	*"NOT a failure"*) ;;
	*) fail "timeout with no verdict: must say plainly that this is not a failure, got: $OUT" ;;
	esac
	case "$OUT" in
	*"re-attach to the same job"*) ;;
	*) fail "timeout with no verdict: must say re-invoking re-attaches, got: $OUT" ;;
	esac
	if grep -q 'exec job output' "$STUB_ARGV_FILE"; then
		fail "timeout with no verdict: must not read job output before the job finishes"
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

# --- the gate's Playwright area selection survives the detach boundary.
#
# The reported failure: `erun exec resolve-playwright-areas` on a clean tree
# printed `smoke` while `make check` -- the gate that actually runs in an agent
# pod -- ran the full suite, with nothing in either output saying which of the
# two it had used. The selection travels in PLAYWRIGHT_TEST_AREAS, and `check`
# depends on test-playwright rather than being one of its prerequisites, so the
# target-specific variable test-playwright declares is out of scope in the
# recipe that hands the environment to agent-gate.sh and on to the job. What
# crossed instead was the bare `export`'s empty-but-*defined* value: inside the
# job, test-playwright's own `?=` read that as "the caller already supplied
# this" and skipped the resolution, and run.sh read it as "no selection".
#
# This case drives the shipped Makefile: its `check` target and the
# target-specific declarations and bare `export` that decide its environment,
# the real agent-gate.sh, and a job boundary whose command runs under the
# environment it inherited (what `erun exec job start` was measured doing).
# Only the recipes that would actually run the suite are replaced, so the
# environment under test is the shipped one; the observer is the real
# test-playwright target, still carrying its own `?=`, reporting the value it
# would hand to run.sh.
case_dir="${work_root}/playwright-area-selection"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun_stateful "${case_dir}/bin"
cat >"${case_dir}/inner-observe.mk" <<'EOF'
test-erun-ui-windows-build test-frontend:
	@:
test-playwright:
	@printf 'gate-consumer PLAYWRIGHT_TEST_AREAS=[%s]\n' "$${PLAYWRIGHT_TEST_AREAS-}"
EOF
cat >"${case_dir}/boundary.mk" <<'EOF'
check:
	@./scripts/agent-gate.sh check "make check" -- $(MAKE) -f Makefile -f "$$INNER_OBSERVE_MK" test-playwright
EOF
(
	cd "${script_dir}/.."
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE STUB_STORE_DIR="${case_dir}/store"
	export ERUN_ENV_TYPE=remote-agent
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev
	export INNER_OBSERVE_MK="${case_dir}/inner-observe.mk"
	unset PLAYWRIGHT_TEST_AREAS AGENT_GATE_DETACHED RUN_SH_AGENT_GATED

	# What the tree resolves to, computed the same way the Makefile does.
	expected=$(cd erun-cli && go run . exec resolve-playwright-areas)
	[ -n "$expected" ] || fail "area selection: the resolver produced no selection to compare against"

	set +e
	OUT=$(make -f Makefile -f "${case_dir}/boundary.mk" check 2>&1)
	STATUS=$?
	set -e
	[ "$STATUS" -eq 0 ] || fail "area selection: the gate was expected to reach its observer cleanly, got $STATUS ($OUT)"

	got=$(printf '%s\n' "$OUT" | sed -n 's/^gate-consumer PLAYWRIGHT_TEST_AREAS=\[\(.*\)\]$/\1/p' | head -1)
	[ -n "$got" ] || fail "area selection: the gate never reported a selection to its consumer, output was: $OUT"
	[ "$got" = "$expected" ] || fail "area selection: the gate ran with PLAYWRIGHT_TEST_AREAS=[$got] but the tree resolved [$expected] -- the gate must run the selection it resolved, not an empty one it inherited"
)

# --- the gate-scoping environment is part of what a job id identifies: a
# cached pass recorded for one selection must never be replayed for a
# differently-scoped request under the same job id and tree. Without this, a
# narrowed run's green stands in for a full-suite request -- a gate reporting a
# verdict for a selection it did not run.
#
# The two requests below are identical in argv and differ only in
# PLAYWRIGHT_TEST_AREAS, and execution is observed through a side effect
# rather than through the command's own output. Argv is already part of the
# job id, so a case whose two runs differed there -- a "narrow selection"
# label against a "full selection" one, say -- separates them whether or not
# the selection is ever consulted, and passes against a key that ignores it.
# Same argv, one channel of difference: only the selection can tell them
# apart.
case_dir="${work_root}/gate-scope-replay"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun_stateful "${case_dir}/bin"
runs_file="${case_dir}/runs"
: >"$runs_file"
(
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE STUB_STORE_DIR="${case_dir}/store"
	export ERUN_ENV_TYPE=local-agent
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev

	PLAYWRIGHT_TEST_AREAS=smoke run_gate ui-playwright scoped-run -- sh -c "echo run >>'${runs_file}'"
	[ "$STATUS" -eq 0 ] || fail "gate scope replay: narrowed run expected exit 0, got $STATUS ($OUT)"

	PLAYWRIGHT_TEST_AREAS=all run_gate ui-playwright scoped-run -- sh -c "echo run >>'${runs_file}'"
	[ "$STATUS" -eq 0 ] || fail "gate scope replay: full run expected exit 0, got $STATUS ($OUT)"

	runs=$(wc -l <"$runs_file" | tr -d ' ')
	[ "$runs" -eq 2 ] || fail "gate scope replay: a request under a different selection must actually run rather than replaying the other selection's recorded pass, ran $runs of 2"
)

# --- the environment a run executes under is part of what its recorded result
# is a result *of*: a cached pass recorded under one environment must never
# stand in for a request under another, or the second configuration is
# reported green without ever having executed.
#
# This is the reported failure, in the shape it actually happened: a lane
# proved a fix under three starvation delays set by an environment variable,
# with an identical command and tree each iteration, so iterations 2 and 3
# replayed iteration 1 and only one job ever ran -- reported as "15/15 green
# across 200/750/2000 ms" when two of those three numbers never executed.
#
# The two requests below are identical in argv and tree and differ only in
# ERUN_2696_KNOB, and execution is observed through a side effect rather than
# through the command's own output. Argv is already part of the job id and the
# tree is already part of it, so a case whose two runs differed in either would
# separate them whether or not the environment is ever consulted, and would
# pass against a key that ignores it. Same argv, same tree, one channel of
# difference: only the environment can tell them apart.
case_dir="${work_root}/env-state-replay"
mkdir -p "$case_dir"
STUB_ARGV_FILE="${case_dir}/argv"
: >"$STUB_ARGV_FILE"
stub_erun_stateful "${case_dir}/bin"
runs_file="${case_dir}/runs"
: >"$runs_file"
(
	export PATH="${case_dir}/bin:$PATH"
	export STUB_ARGV_FILE STUB_STORE_DIR="${case_dir}/store"
	export ERUN_ENV_TYPE=local-agent
	export ERUN_TENANT=acme ERUN_ENVIRONMENT=dev

	ERUN_2696_KNOB=200 run_gate check "make check" -- sh -c "echo run >>'${runs_file}'"
	[ "$STATUS" -eq 0 ] || fail "env state replay: first environment expected exit 0, got $STATUS ($OUT)"

	ERUN_2696_KNOB=750 run_gate check "make check" -- sh -c "echo run >>'${runs_file}'"
	[ "$STATUS" -eq 0 ] || fail "env state replay: second environment expected exit 0, got $STATUS ($OUT)"

	runs=$(wc -l <"$runs_file" | tr -d ' ')
	[ "$runs" -eq 2 ] || fail "env state replay: a run under a different environment must actually execute rather than replaying the other environment's recorded pass, ran $runs of 2"
	case "$OUT" in
	*"refusing to replay"*) ;;
	*) fail "env state replay: a refused replay must say so on stderr rather than silently re-running, got: $OUT" ;;
	esac

	# Same tree and command, and now the same environment too: that one is a
	# genuine replay, and losing it would defeat the whole point of the cache.
	ERUN_2696_KNOB=750 run_gate check "make check" -- sh -c "echo run >>'${runs_file}'"
	[ "$STATUS" -eq 0 ] || fail "env state replay: identical re-run expected exit 0, got $STATUS ($OUT)"
	runs=$(wc -l <"$runs_file" | tr -d ' ')
	[ "$runs" -eq 2 ] || fail "env state replay: an identical command, tree and environment must still replay rather than re-execute, ran $runs of 2"
	case "$OUT" in
	*"replaying a cached PASS"*) ;;
	*) fail "env state replay: expected the replay notice for a fully identical run, got: $OUT" ;;
	esac
)

# --- a job already RUNNING under a different environment is the same hole on
# the live path: this invocation has nothing to do but attach to it (starting a
# second gate beside it is what the exclusive claim refuses), so the outcome it
# reports is that run's -- and saying nothing would let a caller record a
# verdict produced for a configuration it never asked about.
case_dir="${work_root}/env-state-running"
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
	export STUB_STATUS_LINE='running: make check [env 0000000000000000], pid 123'
	export STUB_START_STATUS=1
	export STUB_START_STDERR='job "check" is already running (pid 123); pass a different id or cancel it first'
	export STUB_AWAIT_STATUS=0
	export STUB_JOB_OUTPUT='resumed output'
	run_gate check "make check" -- make check-gate
	[ "$STATUS" -eq 0 ] || fail "env state running: expected exit 0, got $STATUS ($OUT)"
	case "$OUT" in
	*"resumed output"*) ;;
	*) fail "env state running: the running job must still be attached to, got: $OUT" ;;
	esac
	case "$OUT" in
	*"WARNING"*"started under environment"*) ;;
	*) fail "env state running: attaching to a run started under a different environment must say so, got: $OUT" ;;
	esac
	case "$OUT" in
	*"0000000000000000"*) ;;
	*) fail "env state running: the warning must name the environment that run actually ran under, got: $OUT" ;;
	esac
)

echo "ok: agent-gate.sh"
echo "ok: erun-ui/playwright/run.sh detachment wiring"
