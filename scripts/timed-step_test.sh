#!/bin/sh

# Tests for timed-step.sh, the helper that makes a phase marker report its own
# duration instead of leaving the build profiler to derive one from the gap to
# the next marker -- a gap that means nothing under `make -j` (#2341).
#
# The contract under test is erun-common/build_progress_phases.go's
# buildProgressSelfTimedPattern: `^(.*) \[([0-9]+)s\]$`. A marker that does not
# match it is treated as an elapsed window, so the exact output shape matters
# more than it looks.
#
# Run directly (not wired into `make check`), same reasoning as
# agent-gate_test.sh and parallel-gate_test.sh.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
timed="${script_dir}/timed-step.sh"

failures=0

fail() {
    printf 'FAIL: %s\n' "$1" >&2
    failures=$((failures + 1))
}

pass() {
    printf 'ok: %s\n' "$1"
}

# 1. The marker matches the profiler's self-timed pattern exactly.
out="$("${timed}" 'running integration suite' true)"
if printf '%s\n' "${out}" | grep -qE '^>> running integration suite \[[0-9]+s\]$'; then
    pass 'emits a marker in the self-timed form the profiler prefers'
else
    fail "marker did not match the self-timed pattern: ${out}"
fi

# 2. A marker naming a duration it did not take would be worse than none, so
#    the reported figure has to track real elapsed time.
out="$("${timed}" 'slow step' sleep 2)"
secs="$(printf '%s\n' "${out}" | sed -n 's/^>> slow step \[\([0-9]*\)s\]$/\1/p')"
if [ "${secs:-0}" -ge 2 ]; then
    pass 'reports the real elapsed duration'
else
    fail "expected >= 2s for a 2s step, got '${secs}'"
fi

# 3. The step's own output still streams; the marker is additional, not a
#    replacement for it.
out="$("${timed}" 'noisy step' sh -c 'echo inner-output')"
if printf '%s\n' "${out}" | grep -q '^inner-output$'; then
    pass "passes through the step's own output"
else
    fail "step output was swallowed: ${out}"
fi

# 4. A failing step must still fail the build. Timing a step is not a licence
#    to swallow its verdict.
set +e
out="$("${timed}" 'failing step' sh -c 'exit 3' 2>&1)"
status=$?
set -e
if [ "${status}" -eq 3 ]; then
    pass "preserves the step's exit status"
else
    fail "expected exit 3, got ${status}"
fi

# 5. ...and still reports its cost, since a step that burned time before
#    failing is exactly the one worth seeing in the profile.
if printf '%s\n' "${out}" | grep -qE '^>> failing step \[[0-9]+s\]$'; then
    pass 'reports a failed step rather than dropping it from the profile'
else
    fail "failed step reported no marker: ${out}"
fi

# 6. Called wrong, it says so instead of silently timing nothing.
set +e
"${timed}" 'only-a-marker' >/dev/null 2>&1
status=$?
set -e
if [ "${status}" -eq 2 ]; then
    pass 'rejects a call with no command to run'
else
    fail "expected usage exit 2, got ${status}"
fi

if [ "${failures}" -ne 0 ]; then
    printf '\n%s test(s) failed\n' "${failures}" >&2
    exit 1
fi
printf '\nall timed-step.sh tests passed\n'
