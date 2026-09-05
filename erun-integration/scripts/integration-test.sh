#!/bin/bash
# Run the erun-integration suite with coverage instrumentation enabled and
# fail if total coverage of the production packages drops below the threshold.
#
# Usage:
#   ./scripts/integration-test.sh [-threshold=NN]
#   ./scripts/integration-test.sh --update-golden
#
# Gate mode (default) and golden-reseed mode are mutually exclusive. Reseeding
# is opt-in only via the --update-golden flag, which cannot be set from a
# parent `make`/environment invocation. If UPDATE_GOLDEN is set in the
# environment and --update-golden was not passed, the script refuses to run
# rather than silently comparing nothing while still reporting a green gate.
#
# Environment:
#   COVERAGE_THRESHOLD             default 75 (percent). See note below.
#   GOCOVERDIR                     override the directory used for raw
#                                  counter files; defaults to a fresh, unique
#                                  temp directory per invocation (see note
#                                  below on why not a fixed path).
#   INTEGRATION_TEST_PARALLELISM   override the `go test -parallel` value
#                                  outright, skipping the width calculation
#                                  below.
#
# Notes:
#   - The instrumented binary is rebuilt each run so signatures stay aligned
#     with whatever code is being tested. The build uses the same -coverpkg
#     selector as the binary helper in internal/erun, so the merged profile
#     reflects exactly the production packages we want to gate on.
#   - Most scenarios are independent: each gets its own tempdir-rooted
#     HOME/XDG/cwd (internal/env.New) and, where a scenario needs one, its own
#     dynamically-ported httptest server, so they run under `t.Parallel()`.
#     The exception is every scenario that binds a real, hardcoded TCP port
#     (the `skipIfPortsBusy`-guarded real-run scenarios in mcp_test.go,
#     open_test.go, app_test.go, whip_test.go, and
#     environment_half_scenarios_test.go, plus
#     job_off_environment_agent_test.go's single top-level test) — those stay
#     serial (no `t.Parallel()` call anywhere in their own top-level Test
#     function), so two scenarios can never collide on the same literal port.
#     Go's test driver never interleaves a non-parallel top-level test with
#     any other top-level test, so this is a hard guarantee, not a scheduling
#     accident: every serial top-level test runs to completion, one at a
#     time, in file order, before any parallel-marked top-level test's body
#     starts executing.
#   - `go test -parallel` defaults to GOMAXPROCS, which (like `nproc`) reads
#     the CPU affinity mask rather than the container's CPU quota — the same
#     blind spot scripts/parallel-gate.sh's own header documents for the
#     erun-devops test stage's sibling cgroup. On the pod this was measured
#     on, that blind spot is real: `nproc` reports 24, but cgroup cpu.max
#     quotes only 6. test_parallelism below reuses parallel-gate.sh's `width`
#     mode to read the real quota instead of trusting GOMAXPROCS.
#   - Unlike the shell-dispatched fleets `width` was built for (N independent
#     lint or helm-chart-test processes, each with its own roughly-fixed
#     memory cost), this suite's memory use does not scale linearly with
#     -parallel: five consecutive runs at the unthrottled GOMAXPROCS=24
#     default peaked at 4.0-4.5GiB RSS, and five more at the quota-derived
#     -parallel=6 peaked at 4.7-5.1GiB -- a wash, not a 4x drop, because a
#     compiled-once instrumented binary, Go's own test-cache/coverage
#     bookkeeping, and per-scenario tempdirs already alive from earlier
#     scenarios dominate over the marginal cost of one more concurrent
#     subprocess. Dividing an assumed per-job cost into the memory ceiling
#     would therefore invent a number this workload doesn't obey, so `width`
#     is called with no memory term (job-count cap and CPU quota only) and
#     the real ceiling is a documented fact instead: erun-devops/AGENTS.md's
#     Runtime Chart Rules names the erun-dind sidecar's memory limit as "up
#     to 20GiB" by default, comfortably above every measured peak here with
#     room to spare.
#   - The default threshold tracks what the suite actually reaches, minus a
#     small margin for cross-host variance. The historical gap families
#     (interactive prompts, subprocess launchers, port-forward workers, IDE
#     launchers, the shell loop, AWS error classifiers, config persistence)
#     are covered via trace lifts, the ERUN_FORCE_TTY seam, scripted stdin,
#     and real-run-via-stub scenarios. What remains uncovered is documented
#     in erun-integration/AGENTS.md "Known integration coverage gaps":
#     live-network code with no seam, desktop/MCP-only erun-common API,
#     second-sequential-prompt flows, host-OS-locked arms, and defensive
#     error branches — which caps the honest ceiling below 90%.
#     Raise the threshold in the same commit as the scenarios (or the
#     production trace lift) that earned the increase; never raise it past
#     measured reality minus margin, and never lower it without a tracked
#     discussion in the PR.
#   - The default coverage directory is unique per invocation rather than a
#     fixed path under the module, because the fixed path used to be wiped
#     by its own `rm -rf "$cover_dir"/*` cleanup step at the top of every
#     run: two invocations against the same checkout (an overlapping retry,
#     or a developer running this by hand while an already-detached
#     agent-gate.sh job is mid-flight) each clean the same directory at
#     startup, so the second invocation's cleanup can delete counter files
#     the first invocation's already-finished subprocess calls had already
#     written, before the first invocation's own merge step reads them. The
#     result looks identical to a clean, passing run — normal test duration,
#     every test green — except the merged total is missing whatever the
#     wipe deleted, which can be most of it depending on timing. Passing an
#     explicit GOCOVERDIR opts back into the old shared/reusable-path
#     behavior (e.g. to inspect counters after the run); only the default
#     changed.

set -euo pipefail

threshold="${COVERAGE_THRESHOLD:-75.1}"
update_golden=0
while [[ $# -gt 0 ]]; do
    case "$1" in
        -threshold=*) threshold="${1#-threshold=}" ;;
        --threshold=*) threshold="${1#--threshold=}" ;;
        --update-golden) update_golden=1 ;;
        *) echo "unknown arg: $1" >&2; exit 2 ;;
    esac
    shift
done

if [[ -n "${UPDATE_GOLDEN:-}" && "$update_golden" -eq 0 ]]; then
    echo "!! UPDATE_GOLDEN is set in the environment, but this script only reseeds" >&2
    echo "!! goldens via the explicit --update-golden flag. Gate mode and golden-" >&2
    echo "!! reseed mode are mutually exclusive: an inherited UPDATE_GOLDEN would" >&2
    echo "!! make every golden.Equal comparison a silent no-op write instead of a" >&2
    echo "!! check, so the gate refuses to run rather than report a false green." >&2
    echo "!!" >&2
    echo "!! Unset UPDATE_GOLDEN to run the gate, or pass --update-golden explicitly" >&2
    echo "!! to reseed testdata (this skips the coverage gate)." >&2
    exit 2
fi

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$here"

profile="$here/coverage/profile.txt"
mkdir -p "$(dirname "$profile")"

cover_dir_is_temp=0
if [[ -n "${GOCOVERDIR:-}" ]]; then
    cover_dir="$GOCOVERDIR"
    mkdir -p "$cover_dir"
    rm -rf "$cover_dir"/*
else
    cover_dir="$(mktemp -d "${TMPDIR:-/tmp}/erun-integration-cover.XXXXXX")"
    cover_dir_is_temp=1
fi

export GOCOVERDIR="$cover_dir"

test_output="$(mktemp "${TMPDIR:-/tmp}/erun-integration-test-output.XXXXXX")"
cleanup() {
    rm -f "$test_output"
    if [[ "$cover_dir_is_temp" -eq 1 ]]; then
        rm -rf "$cover_dir"
    fi
}
trap cleanup EXIT

test_parallelism="${INTEGRATION_TEST_PARALLELISM:-$("$here/../scripts/parallel-gate.sh" width 32 "")}"

if [[ "$update_golden" -eq 1 ]]; then
    echo ">> reseeding golden files (comparisons disabled, coverage gate skipped)"
    UPDATE_GOLDEN=1 go test -count=1 -parallel="$test_parallelism" ./...
    echo ">> golden files reseeded; inspect the testdata diff, then re-run without --update-golden to gate"
    exit 0
fi

echo ">> running integration suite (cover dir: $cover_dir, parallel: $test_parallelism)"
go test -count=1 -parallel="$test_parallelism" ./... 2>&1 | tee "$test_output"

# A coverage meta-data emit failure (concurrent invocations racing a
# write-then-rename into a shared GOCOVERDIR) prints this line to the losing
# invocation's own stdout/stderr without failing the scenario that was
# running at the time. Left undetected, that invocation's counters never
# land and the merged total below silently under-reports coverage instead of
# the gate ever seeing why. Fail loudly here instead of computing a total
# that quietly omitted data.
if grep -q "coverage meta-data emit failed" "$test_output"; then
    echo "!! a coverage meta-data emit failed during the run (see above) -- that" >&2
    echo "!! invocation's counters never landed, so the merged total below would" >&2
    echo "!! silently under-report coverage rather than reflect what actually ran." >&2
    echo "!! Refusing to report a total; re-run the suite." >&2
    exit 1
fi

echo ">> merging coverage counters into $profile"
go tool covdata textfmt -i="$cover_dir" -o="$profile"

echo ">> coverage by function (last line is the total):"
go tool cover -func="$profile" | tail -20

total_line=$(go tool cover -func="$profile" | tail -1)
total_pct=$(awk '{ gsub(/%/, "", $NF); print $NF }' <<<"$total_line")

awk -v got="$total_pct" -v want="$threshold" '
    BEGIN {
        if (got + 0 < want + 0) {
            printf("\n!! coverage %.1f%% is below threshold %.1f%%\n", got + 0, want + 0) > "/dev/stderr"
            exit 1
        }
        printf("\nok  coverage %.1f%% (>= %.1f%%)\n", got + 0, want + 0)
    }
'
