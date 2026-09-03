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
prev=""
for a in "$@"; do
	if [ "$prev" = "--id" ]; then
		job_id="$a"
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
	printf 'exited %s: stateful job' "$code" >"${STUB_STORE_DIR}/${job_id}.status"
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
