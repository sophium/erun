#!/bin/sh
# Run a vitest workspace's suite as a gate step, telling a worker pool that
# faulted of its own accord apart from a test that failed.
#
# WHY THIS EXISTS
#
# `test-frontend` dispatches the two vitest workspaces (erun-ui/frontend and
# erun-console) as concurrent jobs inside `make check`, where they share the
# environment's CPU quota with the rest of check-gate's own `-j` fan-out. Under
# that contention a forked worker can be starved past vitest's worker-start
# timeout, and the pool then reports:
#
#   Error: [vitest-pool]: Failed to start forks worker for test files <path>.
#   Caused by: Error: [vitest-pool-runner]: Timeout waiting for worker to respond
#
# ...and exits 1. That is the same exit status, and so the same
# `test-frontend failed in: erun-console-test` line in the gate's report, as a
# genuine assertion failure -- but it says nothing at all about the code under
# test: no test ran. The file named in the message differs between occurrences,
# which is the signature of the environment rather than of any one spec. Each
# occurrence costs a full build and takes a branch out of the merge queue for a
# reason unrelated to its change.
#
# WHAT IT DOES
#
# The two outcomes are distinguishable, so this distinguishes them instead of
# catching broadly. A pool-start failure is a vitest-owned string -- the
# `[vitest-pool]`/`[vitest-pool-runner]` prefixes are emitted by vitest's own
# pool, never by a test -- and it is only ever an environment fault when the
# run also produced no test-level failure: vitest prints `Test Files <n> failed`
# and `Tests <n> failed` for an assertion, a thrown error or a collection
# failure, so their absence is a positive statement that nothing ran and failed.
# A run carrying both a pool failure and a test failure is a test failure. The
# class is wider than the start timeout quoted above: it is every way the pool
# itself faults, which `pool_start_failure` below enumerates. So:
#
#   * test-level failure present, with or without a pool failure ->
#     the red belongs to the branch. No retry; the run's own output and exit
#     status are passed through untouched.
#   * pool-start failure only -> retried, bounded by --attempts. The second
#     attempt's own verdict is what counts, so a real failure there is reported
#     as one and the branch is blamed.
#   * pool-start failure on every attempt -> the gate still goes red, because
#     nothing was verified and a green gate must never mean "the suite did not
#     run", but it exits 2 behind a `vitest-gate: ENVIRONMENT FAULT` line saying
#     the red is not attributable to the change under test.
#
# The retry is bounded by the attempt budget and by nothing else -- no sleep, no
# loop-until-green. It does not need one: reaching the verdict at all means the
# pool already spent its start timeout (60s in vitest 4, and not configurable:
# 4.1.11 exposes no knob for it), so the second attempt begins behind that gap.
# Widening the timeout instead is not available even in principle.
#
# The suite is invoked exactly as `test-frontend` invoked it before this wrapper
# existed -- `yarn test -- <args>` in the workspace directory -- so the bound
# the Makefile names on the job line (`--maxWorkers=$(FRONTEND_VITEST_WORKERS)`)
# still reaches vitest through the wrapper's own word-split argument list.
#
# The gate is expected to stay red when this reports an environment fault. What
# changes is the diagnosis, not the verdict.
set -eu

# The marker and status a caller (or a log grep) branches on, kept here rather
# than inline so the gate's own test asserts the same literal that ships.
ENVIRONMENT_FAULT_MARKER='vitest-gate: ENVIRONMENT FAULT'
ENVIRONMENT_FAULT_STATUS=2

attempts=2
classify_log=''
vitest_args=''

usage() {
	cat <<'EOF' >&2
Usage: vitest-gate.sh [options] [--] [vitest args...]

Runs `yarn test` in the current directory, telling a vitest worker pool that
faulted of its own accord apart from a test that failed.

  --attempts N     Total attempts allowed for the pool-start class (default 2,
                   i.e. one retry). A test-level failure is never retried.
  --classify LOG   Print WORKER_START or TEST_FAILURE for an already-captured
                   vitest log and exit 0; run nothing.
  -h, --help       This text.

Exit status: 0 passed; otherwise the runner's own status for a test-level
failure, or 2 for a pool that faulted on every attempt.

Vitest args are forwarded by word splitting, so they must not themselves
contain quoted whitespace; every caller here passes plain flags.
EOF
}

# pool_start_failure <log>: true when vitest's own pool reports a fault of its
# own, meaning no test result was produced. Every shape carries a
# `[vitest-pool]`/`[vitest-pool-runner]` prefix, which vitest's own pool emits
# and a test does not.
#
# The set is the whole message vocabulary of that pool in vitest 4.1.11, because
# the pool reaches this state by several independent routes and recognising only
# one of them blames the branch for the others. `Failed to start <pool> worker
# for test files <files>.` is a rejected `runner.start()` -- a spawn error -- and
# `Timeout starting <pool> runner.` is its start timeout, but a worker that
# started and then died does not pass through `start()` at all: `onTaskError`
# rejects the queued task directly with `Worker <pool> emitted error.`, so it
# never carries the `Failed to start` wrapper. Cancellation (`Cannot run tasks
# while pool is cancelling`) and teardown (`Timeout terminating` and `Failed to
# terminate`, which the pool logs rather than rejects) are the remaining
# siblings. The `[vitest-pool-runner]` messages are accepted on their own so a
# vitest that reports the inner cause without the pool's wrapper is recognised.
#
# `Pending methods while closing rpc` (`[vitest-pool-runner]`) is deliberately
# not here: it is an RPC still in flight at shutdown, which says nothing about
# whether the suite ran, so admitting it would widen the class past "no test
# result was produced" for no coverage.
pool_start_failure() {
	grep -qE '^[[:space:]]*(Error: )?\[vitest-pool\]: (Failed to start .* worker for test files .*\.|Timeout starting .* runner\.|Worker .* emitted error\.|Timeout terminating .* worker for test files .*\.|Failed to terminate .* worker for test files .*\.|Cannot run tasks while pool is cancelling)[[:space:]]*$' "$1" ||
		grep -qE '^[[:space:]]*(Error: )?\[vitest-pool-runner\]: (Timeout waiting for worker to respond|Cannot start a stopped runner)[[:space:]]*$' "$1"
}

# test_level_failure <log>: true when the run reports a test file or a test that
# itself failed. vitest prints these summary lines for every class of genuine
# failure and prints them last, so their absence is what lets the pool-start
# class above be read as "nothing ran" rather than "nothing failed".
test_level_failure() {
	grep -qE '^[[:space:]]*(Test Files|Tests)[[:space:]]+[0-9]+ failed' "$1" ||
		grep -qE '^[[:space:]]*FAIL[[:space:]]' "$1"
}

# Every shape this cannot read falls to TEST_FAILURE, which is the direction
# that keeps the red attached to the branch. The pool-start class has to be
# both positively recognised and positively free of any test-level failure;
# there is no input for which a failed run is quietly called the environment.
classify() {
	if pool_start_failure "$1" && ! test_level_failure "$1"; then
		echo WORKER_START
	else
		echo TEST_FAILURE
	fi
}

run_suite() {
	if [ -n "$vitest_args" ]; then
		# shellcheck disable=SC2086
		yarn test -- $vitest_args >"$1" 2>&1
	else
		yarn test >"$1" 2>&1
	fi
}

report_environment_fault() {
	printf '\n'
	printf '>> %s\n' "$ENVIRONMENT_FAULT_MARKER"
	printf '>> vitest-gate: the pool reported a fault of its own on all %s attempt(s), and no test failed.\n' "$2"
	printf '>> vitest-gate: vitest reported:\n'
	sed -nE 's/.*(\[vitest-pool(-runner)?\]: .*)/>> vitest-gate:   \1/p' "$1"
	printf '>> vitest-gate: no test result was produced, so this red says nothing about the change under\n'
	printf '>> vitest-gate: test and is not attributable to the branch. The gate is still red because\n'
	printf '>> vitest-gate: nothing was verified -- re-run before reading it as a failure.\n'
	printf '>> vitest-gate: this is a diagnosis, not a clearance. A worker that never starts also reaches\n'
	printf '>> vitest-gate: this message when something outside the tests is broken in a way that survives\n'
	printf '>> vitest-gate: a quiet host -- a pool option in a config this branch changed, say. If it does\n'
	printf '>> vitest-gate: reproduce off the gate, the change is the cause and the pool is where to look.\n'
}

while [ $# -gt 0 ]; do
	case "$1" in
	--attempts)
		[ $# -ge 2 ] || {
			usage
			exit 2
		}
		attempts=$2
		shift 2
		;;
	--classify)
		[ $# -ge 2 ] || {
			usage
			exit 2
		}
		classify_log=$2
		shift 2
		;;
	--)
		shift
		break
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		break
		;;
	esac
done
vitest_args="$*"

if [ -n "$classify_log" ]; then
	if [ ! -r "$classify_log" ]; then
		printf 'vitest-gate: cannot read %s\n' "$classify_log" >&2
		exit 2
	fi
	classify "$classify_log"
	exit 0
fi

case "$attempts" in
'' | *[!0-9]*)
	printf 'vitest-gate: --attempts must be a positive integer\n' >&2
	exit 2
	;;
esac
if [ "$attempts" -lt 1 ]; then
	printf 'vitest-gate: --attempts must be at least 1\n' >&2
	exit 2
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
# Signals exit through the EXIT trap rather than continuing past it, so a
# cancelled gate step still cleans up and still reports as cancelled.
trap 'exit 130' INT
trap 'exit 143' TERM

attempt=1
while :; do
	rc=0
	log="$work/attempt-$attempt.log"
	run_suite "$log" || rc=$?
	cat "$log"
	if [ "$rc" -eq 0 ]; then
		exit 0
	fi
	if [ "$(classify "$log")" != "WORKER_START" ]; then
		printf '>> vitest-gate: test-level failure reported; passing the run through as it stands (attempt %s of %s)\n' "$attempt" "$attempts"
		exit "$rc"
	fi
	if [ "$attempt" -ge "$attempts" ]; then
		report_environment_fault "$log" "$attempts"
		exit "$ENVIRONMENT_FAULT_STATUS"
	fi
	printf '>> vitest-gate: the pool faulted of its own accord and no test failed -- that is the environment, not the change under test. Retrying (attempt %s of %s).\n' "$((attempt + 1))" "$attempts"
	attempt=$((attempt + 1))
done
