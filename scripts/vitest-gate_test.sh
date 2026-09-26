#!/bin/sh
# Tests for vitest-gate.sh, the wrapper that tells a vitest pool which faulted
# on its own apart from a test that failed.
#
# The failure this exists to catch is load-dependent -- a forked worker
# starved past vitest's own start timeout by the CPU contention inside
# `make check` -- so it is reproduced here on purpose rather than waited for:
# the pool fixtures below are the real lines vitest 4.1.11 emits for that
# state, captured from a run in which a worker was made unable to answer
# `started` (the box-drawing separators and stack frames are dropped; every
# line the classifier reads is verbatim). The stub runner replays them.
#
# The pool does not only fail at start, and the starved-worker route is not the
# only one that reaches the class. `pool-spawn-error.log` is a real run in which
# a worker spawned and then died before it could run anything -- induced on
# purpose by giving `test.execArgv` a flag node rejects, which is deterministic,
# so the route is held here rather than waited for -- and the terminate,
# cancelling and stopped-runner fixtures are the pool's other own-fault
# messages. Each of those was read as TEST_FAILURE before the class was widened
# past the start path, which is the defect these cases pin.
#
# The cases that matter most are the ones crossing the two: a real test failure
# in the SAME run as a pool fault -- which vitest really produces, a pool that
# loses one worker while another runs the suite to a genuine failure -- and a
# collection failure that carries a recognised pool string while reporting no
# failed test. A broad "the log mentions a worker, so it is the environment"
# catch would wave both through. Each must be reported as a test failure, must
# not be retried, and must not be labelled an environment fault.
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

# --- a worker that SPAWNED and then died: not a start failure -----------
# The pool does not only fail at start. `runner.start()` rejecting is one route
# to a pool fault; a worker that started and then died takes another, and it
# never carries the `Failed to start` wrapper -- vitest's `onTaskError` rejects
# the queued task directly. Captured from a real vitest 4.1.11 run in which a
# worker was made unable to load at all (`test.execArgv` given a flag node does
# not accept), which is the reproduction this fixture exists to hold: that run
# produced no test result of any kind, so it is the environment.
cat >"$tmp/pool-spawn-error.log" <<'EOF'
 RUN  v4.1.11 /src/erun-console

/usr/local/bin/node: bad option: --this-is-not-a-real-node-flag

Vitest caught 1 unhandled error during the test run.
This might cause false positive tests. Resolve unhandled errors to make sure your tests are not affected.

Error: [vitest-pool]: Worker forks emitted error.
Caused by: Error: Worker exited unexpectedly

 Test Files  no tests
      Tests  no tests
     Errors  1 error

error Command failed with exit code 1.
EOF

# --- the teardown siblings, which the pool logs rather than rejects -----
# Captured from a real run with a teardown timeout short enough to trip; note
# the message carries no `Error: ` prefix on this route, which is why the
# classifier's own prefix is optional.
cat >"$tmp/pool-terminate-timeout.log" <<'EOF'
 RUN  v4.1.11 /src/erun-console

[vitest-pool]: Timeout terminating forks worker for test files /src/erun-console/src/identity/UsersPanel.test.tsx.

 Test Files  no tests
      Tests  no tests
     Errors  1 error
EOF

cat >"$tmp/pool-terminate-failed.log" <<'EOF'
 RUN  v4.1.11 /src/erun-console

Error: [vitest-pool]: Failed to terminate forks worker for test files /src/erun-console/src/identity/UsersPanel.test.tsx.

 Test Files  no tests
      Tests  no tests
     Errors  1 error
EOF

cat >"$tmp/pool-cancelling.log" <<'EOF'
 RUN  v4.1.11 /src/erun-console

Error: [vitest-pool]: Cannot run tasks while pool is cancelling

 Test Files  no tests
      Tests  no tests
     Errors  1 error
EOF

cat >"$tmp/pool-runner-stopped.log" <<'EOF'
 RUN  v4.1.11 /src/erun-console

Error: [vitest-pool-runner]: Cannot start a stopped runner

 Test Files  no tests
      Tests  no tests
     Errors  1 error
EOF

# --- the boundary: a pool-runner message that is NOT a pool fault -------
# `Pending methods while closing rpc` is an RPC still in flight at shutdown. It
# says nothing about whether the suite ran, so it is deliberately not in the
# class -- this case exists so a later widening has to argue past it rather than
# absorb it unnoticed. It falls to the safe direction.
cat >"$tmp/pool-runner-pending-rpc.log" <<'EOF'
 RUN  v4.1.11 /src/erun-console

Error: [vitest-pool-runner]: Pending methods while closing rpc

 Test Files  no tests
      Tests  no tests
     Errors  1 error
EOF

# --- the two safety cases the widening had to survive -------------------
# 1. A real test that PRINTS the pool string it knows the classifier looks for,
#    and then fails on its own assertion. The string is recognised; the run is
#    still a test failure, because a test-level failure vetoes the class.
cat >"$tmp/spoofed-pool-string.log" <<'EOF'
 RUN  v4.1.11 /src/erun-console

Error: [vitest-pool]: Worker forks emitted error.

 FAIL  src/shell/AppShell.test.tsx > AppShell > renders the sidebar
AssertionError: expected 2 to be 3 // Object.is equality

 Test Files  1 failed (1)
      Tests  1 failed (1)

error Command failed with exit code 1.
EOF

# 2. A collection failure -- `Test Files 1 failed`, and NO "failed" on the
#    `Tests` line because no test ran -- carrying a pool string from the widened
#    set. This is the crossing the two-condition rule exists for: the widened
#    pattern IS present, and the run is a test failure anyway. Captured from a
#    real collection failure (a test file importing a module that does not
#    exist), with the pool line from the real spawn-error run above.
cat >"$tmp/pool-plus-collection.log" <<'EOF'
 RUN  v4.1.11 /src/erun-console

Error: [vitest-pool]: Worker forks emitted error.

 FAIL  src/broken.test.tsx [ src/broken.test.tsx ]
Error: Cannot find module './does-not-exist.js' imported from /src/erun-console/src/broken.test.tsx

 Test Files  1 failed | 1 passed (2)
      Tests  1 passed (1)

error Command failed with exit code 1.
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

# THE REPRODUCTION: a worker that spawned and died is the environment. Before
# the pool's non-start routes were recognised this was read as TEST_FAILURE --
# a real environment fault blamed on the branch, which is the defect.
classify_case 'a worker that spawned and then died is the environment' \
	"$tmp/pool-spawn-error.log" WORKER_START
classify_case 'a worker that timed out terminating is the environment' \
	"$tmp/pool-terminate-timeout.log" WORKER_START
classify_case 'a worker that could not be terminated is the environment' \
	"$tmp/pool-terminate-failed.log" WORKER_START
classify_case 'a pool cancelled mid-run is the environment' \
	"$tmp/pool-cancelling.log" WORKER_START
classify_case 'a runner that was already stopped is the environment' \
	"$tmp/pool-runner-stopped.log" WORKER_START
classify_case 'a pool-runner message that is not a pool fault stays a test failure' \
	"$tmp/pool-runner-pending-rpc.log" TEST_FAILURE

# ...and the safety property, re-taken against the widened set.
classify_case 'a test spoofing the widened pool string is still a test failure' \
	"$tmp/spoofed-pool-string.log" TEST_FAILURE
classify_case 'a widened pool string beside a collection failure is still a test failure' \
	"$tmp/pool-plus-collection.log" TEST_FAILURE

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

# 11. The reproduction end to end, on the route that is not a start failure:
#     a worker that spawned and died retries once, and a run that never produced
#     a test result ends as a named environment fault rather than as a silent red
#     attributed to the branch.
rm -f "$scenarios"/*.log "$scenarios"/*.status
scenario 1 1 "$tmp/pool-spawn-error.log"
scenario 2 1 "$tmp/pool-spawn-error.log"
run_wrapper --maxWorkers=7
if [ "$status" -eq 2 ] && [ "$runs" -eq 2 ]; then
	pass 'a worker that spawned and died retries to the budget and exits 2'
else
	fail "expected the spawn-error class to retry and exit 2; got status ${status}, ${runs} run(s)"
fi
if grep -q 'vitest-gate: ENVIRONMENT FAULT' "$tmp/out"; then
	pass 'a worker that spawned and died is named as the environment, not the branch'
else
	fail 'a spawn-error pool fault was left to read as a test failure'
fi

if [ "${failures}" -ne 0 ]; then
	printf '\n%s test(s) failed\n' "${failures}" >&2
	exit 1
fi
printf '\nall vitest-gate.sh tests passed\n'
