#!/bin/sh
# Tests for vitest-gate.sh, the wrapper that tells a vitest pool which could
# not start a worker apart from a test that failed.
#
# The failure this exists to catch is load-dependent -- a forked worker
# starved past vitest's own start timeout by the CPU contention inside
# `make check` -- so it is reproduced here on purpose rather than waited for:
# the pool-start fixtures below are the real lines vitest 4.1.11 emits for
# that state, captured from a run in which a worker was made unable to answer
# `started` (the box-drawing separators and stack frames are dropped; every
# line the classifier reads is verbatim). The stub runner replays them.
#
# The case that matters most is the third one down: a pool-start failure and a
# real test failure in the SAME run. That is the state a broad "the log
# mentions a worker, so it is the environment" catch would wave through, and
# it is a state vitest really produces -- a pool that loses one worker while
# another runs the suite to a genuine failure. It must be reported as a test
# failure, must not be retried, and must not be labelled an environment fault.
#
# Run directly, and wired into test-frontend: a classifier that stops
# distinguishing correctly must fail the gate rather than quietly start
# absorbing reds.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
wrapper="${script_dir}/vitest-gate.sh"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

failures=0

fail() {
	printf 'FAIL: %s\n' "$1" >&2
	failures=$((failures + 1))
}

pass() {
	printf 'ok: %s\n' "$1"
}

# --- the pool-start class, with nothing having run ----------------------
cat >"$tmp/pool-no-tests.log" <<'EOF'
 RUN  v4.1.11 /src/erun-console

Vitest caught 1 unhandled error during the test run.
This might cause false positive tests. Resolve unhandled errors to make sure your tests are not affected.

Error: [vitest-pool]: Failed to start forks worker for test files /src/erun-console/src/identity/UsersPanel.test.tsx.
Caused by: Error: [vitest-pool-runner]: Timeout waiting for worker to respond

 Test Files  no tests
      Tests  no tests
     Errors  1 error

error Command failed with exit code 1.
EOF

# --- the pool-start class with every test that did run passing ----------
# The pool replaces a worker it lost, so the files that were scheduled onto it
# are re-run elsewhere: the suite comes back entirely green *and* carries an
# unhandled pool error. This is the shape the reported failure most often
# takes, and it is still nothing but the environment.
cat >"$tmp/pool-all-passed.log" <<'EOF'
 RUN  v4.1.11 /src/erun-console

Error: [vitest-pool]: Failed to start forks worker for test files /src/erun-console/src/mcp/MCPAccessPanel.test.tsx.
Caused by: Error: [vitest-pool-runner]: Timeout waiting for worker to respond

 Test Files  37 passed (37)
      Tests  291 passed (291)
     Errors  1 error

error Command failed with exit code 1.
EOF

# --- BOTH classes in one run: a pool fault beside a real failure --------
cat >"$tmp/pool-plus-real.log" <<'EOF'
 RUN  v4.1.11 /src/erun-console

 FAIL  src/shell/AppShell.test.tsx > AppShell > renders the sidebar
AssertionError: expected 2 to be 3 // Object.is equality

Error: [vitest-pool]: Failed to start forks worker for test files /src/erun-console/src/identity/UsersPanel.test.tsx.
Caused by: Error: [vitest-pool-runner]: Timeout waiting for worker to respond

 Test Files  1 failed | 37 passed (38)
      Tests  1 failed | 291 passed (292)
     Errors  1 error

error Command failed with exit code 1.
EOF

# --- a real failure, no pool fault anywhere -----------------------------
cat >"$tmp/real-failure.log" <<'EOF'
 RUN  v4.1.11 /src/erun-console

 FAIL  src/shell/AppShell.test.tsx > AppShell > renders the sidebar
AssertionError: expected 2 to be 3 // Object.is equality

 Test Files  1 failed (1)
      Tests  1 failed (1)

error Command failed with exit code 1.
EOF

# --- a green run --------------------------------------------------------
cat >"$tmp/all-passed.log" <<'EOF'
 RUN  v4.1.11 /src/erun-console

 Test Files  37 passed (37)
      Tests  291 passed (291)
EOF

classify_case() {
	# classify_case <label> <log> <expected>
	got=$("${wrapper}" --classify "$2")
	if [ "${got}" = "$3" ]; then
		pass "$1"
	else
		fail "$1: expected ${3}, got ${got}"
	fi
}

classify_case 'a pool that could not start, with nothing run, is the environment' \
	"$tmp/pool-no-tests.log" WORKER_START
classify_case 'a pool that could not start beside an all-passing suite is still the environment' \
	"$tmp/pool-all-passed.log" WORKER_START
classify_case 'pool failure beside a real test failure is reported as a test failure' \
	"$tmp/pool-plus-real.log" TEST_FAILURE
classify_case 'a real test failure on its own is a test failure' \
	"$tmp/real-failure.log" TEST_FAILURE
classify_case 'a green run is never the pool-start class' \
	"$tmp/all-passed.log" TEST_FAILURE

# --- the wrapper end to end, against a stub runner ----------------------
scenarios="$tmp/scenarios"
stub_dir="$tmp/stub"
runs_file="$tmp/runs"
args_file="$tmp/args"
mkdir -p "$scenarios" "$stub_dir"

# Stands in for `yarn`: replays the next scenario's captured vitest output and
# exit status, counting its own invocations so a retry is observable, and
# recording the arguments so the forwarded worker bound can be asserted.
cat >"$stub_dir/yarn" <<'STUB'
#!/bin/sh
printf 'run\n' >>"$VITEST_GATE_TEST_RUNS"
n=$(wc -l <"$VITEST_GATE_TEST_RUNS" | tr -d '[:space:]')
: >"$VITEST_GATE_TEST_ARGS"
for a in "$@"; do printf '%s\n' "$a" >>"$VITEST_GATE_TEST_ARGS"; done
if [ -f "$VITEST_GATE_TEST_SCENARIOS/$n.log" ]; then
	cat "$VITEST_GATE_TEST_SCENARIOS/$n.log"
fi
if [ -f "$VITEST_GATE_TEST_SCENARIOS/$n.status" ]; then
	exit "$(cat "$VITEST_GATE_TEST_SCENARIOS/$n.status")"
fi
exit 1
STUB
chmod +x "$stub_dir/yarn"

scenario() {
	# scenario <attempt> <status> <log-file>
	printf '%s' "$2" >"$scenarios/$1.status"
	cp "$3" "$scenarios/$1.log"
}

run_wrapper() {
	: >"$runs_file"
	status=0
	VITEST_GATE_TEST_RUNS="$runs_file" \
		VITEST_GATE_TEST_ARGS="$args_file" \
		VITEST_GATE_TEST_SCENARIOS="$scenarios" \
		PATH="$stub_dir:$PATH" \
		sh "$wrapper" "$@" >"$tmp/out" 2>&1 || status=$?
	runs=$(wc -l <"$runs_file" | tr -d '[:space:]')
}

# 6. A pool that cannot start once and can the second time recovers, and the
#    forwarded worker bound reaches the runner.
rm -f "$scenarios"/*.log "$scenarios"/*.status
scenario 1 1 "$tmp/pool-no-tests.log"
scenario 2 0 "$tmp/all-passed.log"
run_wrapper --maxWorkers=7
if [ "$status" -eq 0 ] && [ "$runs" -eq 2 ]; then
	pass 'retries the pool-start class once and takes the second attempt as the verdict'
else
	fail "expected a retry to recover (status 0, 2 runs); got status ${status}, ${runs} run(s)"
fi
if [ "$(sed -n '3p' "$args_file")" = '--maxWorkers=7' ]; then
	pass 'forwards the Makefile-named worker bound through to the runner'
else
	fail "the worker bound did not reach the runner; args were: $(tr '\n' ' ' <"$args_file")"
fi

# 7. A pool that never starts is an environment fault -- bounded, named, and
#    still red because nothing was verified.
rm -f "$scenarios"/*.log "$scenarios"/*.status
scenario 1 1 "$tmp/pool-no-tests.log"
scenario 2 1 "$tmp/pool-no-tests.log"
run_wrapper --maxWorkers=7
if [ "$status" -eq 2 ] && [ "$runs" -eq 2 ]; then
	pass 'a pool that never starts retries only as far as the attempt budget and exits 2'
else
	fail "expected the retry to be bounded at 2 runs and exit 2; got status ${status}, ${runs} run(s)"
fi
if grep -q 'vitest-gate: ENVIRONMENT FAULT' "$tmp/out"; then
	pass 'names the environment fault rather than leaving it to read as a test failure'
else
	fail 'no environment-fault marker on a run where no worker ever started'
fi

# 8. The bound is a budget, not a loop.
rm -f "$scenarios"/*.log "$scenarios"/*.status
scenario 1 1 "$tmp/pool-no-tests.log"
run_wrapper --attempts 1 --maxWorkers=7
if [ "$status" -eq 2 ] && [ "$runs" -eq 1 ]; then
	pass '--attempts 1 runs once and reports the environment fault without retrying'
else
	fail "expected 1 run and exit 2 with --attempts 1; got status ${status}, ${runs} run(s)"
fi

# 9. THE ONE THAT PROVES NOTHING IS MASKED: a real failing test still fails
#    the gate, is still attributed to the branch, and is never retried.
rm -f "$scenarios"/*.log "$scenarios"/*.status
scenario 1 1 "$tmp/real-failure.log"
scenario 2 0 "$tmp/all-passed.log"
run_wrapper --maxWorkers=7
if [ "$status" -eq 1 ] && [ "$runs" -eq 1 ]; then
	pass 'a real failing test is not retried and keeps the runner exit status'
else
	fail "a real failure was retried or its status changed: status ${status}, ${runs} run(s)"
fi
if grep -q 'FAIL  src/shell/AppShell.test.tsx' "$tmp/out"; then
	pass "a real failing test's own output is passed through untouched"
else
	fail 'the failing test output was swallowed'
fi
if grep -q 'vitest-gate: ENVIRONMENT FAULT' "$tmp/out"; then
	fail 'a real failing test was labelled an environment fault'
else
	pass 'a real failing test is never labelled an environment fault'
fi

# 10. ...and the same holds when a pool fault sits beside that real failure,
#     which is the run the environment class would otherwise absorb.
rm -f "$scenarios"/*.log "$scenarios"/*.status
scenario 1 1 "$tmp/pool-plus-real.log"
scenario 2 0 "$tmp/all-passed.log"
run_wrapper --maxWorkers=7
if [ "$status" -eq 1 ] && [ "$runs" -eq 1 ]; then
	pass 'a pool failure beside a real failure is not retried and keeps the exit status'
else
	fail "the mixture was retried or its status changed: status ${status}, ${runs} run(s)"
fi
if grep -q 'Test Files  1 failed | 37 passed (38)' "$tmp/out" && ! grep -q 'vitest-gate: ENVIRONMENT FAULT' "$tmp/out"; then
	pass 'a pool failure beside a real failure is blamed on the branch, not the environment'
else
	fail 'a real failure sharing a run with a pool fault was reported as the environment'
fi

if [ "${failures}" -ne 0 ]; then
	printf '\n%s test(s) failed\n' "${failures}" >&2
	exit 1
fi
printf '\nall vitest-gate.sh tests passed\n'
