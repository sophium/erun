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
#   COVERAGE_THRESHOLD             override the default threshold (percent),
#                                  which is `coverage_measured` minus
#                                  `coverage_margin` below. See note below.
#   GOCOVERDIR                     override the root directory raw counter
#                                  files are collected under; defaults to a
#                                  fresh, unique temp directory per invocation
#                                  (see note below on why not a fixed path).
#                                  Every process that runs the instrumented
#                                  binary gets its own private subdirectory of
#                                  this root (see note below on why), so this
#                                  var never names where any one process
#                                  writes directly.
#   INTEGRATION_TEST_PARALLELISM   override the `go test -parallel` value
#                                  outright, skipping the width calculation
#                                  below.
#   INTEGRATION_TEST_TIMEOUT       override the `go test -timeout` value
#                                  outright. The gate supplies it from the
#                                  Makefile, which derives it from the same
#                                  CPU quota the width above is derived from;
#                                  the fallback below is for a run of this
#                                  script that did not come through the gate.
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
#   - Read under the gate, though, the real quota is the wrong number: this
#     suite is one of the Go test runners in check-gate's -j fan-out, and the
#     others take a share of that quota sized by GO_TEST_TARGET_COUNT. Sizing
#     this one against the whole quota would claim it a second time, on top of
#     the shares already demanded. So the gate hands down its per-target share
#     as GO_TEST_GOMAXPROCS (the same value the module targets use) and this
#     script uses it; the `width` fallback is for a standalone run, where no
#     sibling is competing and the whole quota really is free. Measured on the
#     6-CPU pod: standalone `width` gives 6, under the gate the share is 1.
#   - `go test` inherits a ten-minute default timeout when nothing passes
#     -timeout, and that default is fixed while this suite's duration is not:
#     it measured 4m7s on a quiet 12-CPU pod and crossed 10m on a contended
#     one, where the package was still making progress and dozens of
#     t.Parallel() scenarios sat in the parallelism barrier (erun#2631). The
#     run was failed by its own clock, not by its tree. So the suite passes an
#     explicit budget, derived from the same resolved CPU quota as the width
#     above and capped so it stays below the harness's own per-child backstop
#     (internal/harnessexec.HangNet, which is deliberately longer than the
#     package deadline so it can never fail a healthy child). The fallback
#     here is that cap: a run that did not come through the gate should not be
#     handed a tighter budget than the gate gives, and it cannot be handed a
#     looser one without breaking the invariant HangNet depends on.
#     The resolved budget is printed in the banner below, so a run that is
#     slow can be told from a run that is stuck without reading the job log's
#     goroutine dump.
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
#     small margin for cross-host variance. The margin is the explicit pair
#     `coverage_measured` / `coverage_margin` below, which the default is
#     derived from rather than a literal the two can drift away from: the pin
#     once sat on exactly the measured 75.1%, so the "margin" was the 0.0079
#     points one-decimal rounding left under it — about three of the 37,060
#     statements the profile covers, and only ~20 before the printed total
#     would change at all. The gate still passed, but a branch adding
#     uncovered production code arrived with no room to miss by, and main
#     itself was one printed tenth from failing every gate on every branch.
#     The measurement is not steady even run to run on one host: two
#     back-to-back runs of the unmodified suite on the same pod differed by
#     three statements (75.107933% and 75.099838%, both printing 75.1%), and
#     the second was already under the old pin in raw terms -- it passed only
#     because the printed one-decimal total is what gets compared.
#     A margin therefore has to be at least one printed tenth (0.1 points, 37
#     statements) to exist in the comparison at all, and 0.1 was already
#     measured too thin here: this pin once held a 0.1-point margin (75.8
#     against a measured 75.9) and was re-based to 0.3 for that reason. 0.3
#     also stays below the 0.4-point regression this gate has actually had to
#     catch, so a real coverage drop still fails instead of being absorbed.
#     Re-base: run the suite unmodified on main and read the total from
#     coverage/profile.txt, not from this script's own output (it prints only
#     the last 20 lines of the per-function listing). `coverage_measured` takes
#     that one-decimal value and the default follows it; `coverage_margin`
#     moves only toward its floor, because a rise in the measured value raises
#     the bar rather than buying back headroom.
#     The historical gap families
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
#   - Within one invocation, every process that runs the instrumented binary
#     gets its own private subdirectory of $cover_dir (internal/erun's
#     PrivateCoverDir), rather than all of them sharing $cover_dir directly.
#     Go's coverage runtime emits its meta-data file at process init using a
#     temp filename with only a nanosecond timestamp for uniqueness (no PID),
#     so with -parallel>1 several instrumented subprocesses can start close
#     enough together to compute the same temp name, race to rename it into
#     place, and have the loser's rename fail outright -- silently dropping
#     that process's coverage rather than failing its own test (measured at a
#     12.5% failure rate across 8 runs sharing one directory before this
#     fix). Giving every process its own directory makes the race impossible
#     rather than merely rare. The per-process directories are merged with
#     `go tool covdata merge` before the percentage is computed, and any
#     directory left empty (a process that should have emitted but didn't)
#     fails the gate outright rather than silently lowering the merged total.

set -euo pipefail

# The suite's measured total on current main, as the one-decimal value
# `go tool cover -func` reports it, and the margin the default keeps below it.
# The default is derived from the two instead of written out a third time, so
# a re-base cannot leave the pin and its stated margin disagreeing. See the
# note on the default threshold in the header.
coverage_measured=75.1
coverage_margin=0.3
threshold="${COVERAGE_THRESHOLD:-$(awk -v measured="$coverage_measured" -v margin="$coverage_margin" 'BEGIN { printf "%.1f", measured - margin }')}"
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

cleanup_dirs=()
cleanup() {
    local d
    for d in ${cleanup_dirs[@]+"${cleanup_dirs[@]}"}; do
        rm -rf "$d"
    done
}
trap cleanup EXIT

if [[ -n "${GOCOVERDIR:-}" ]]; then
    cover_dir="$GOCOVERDIR"
    mkdir -p "$cover_dir"
    rm -rf "$cover_dir"/*
else
    cover_dir="$(mktemp -d "${TMPDIR:-/tmp}/erun-integration-cover.XXXXXX")"
    cleanup_dirs+=("$cover_dir")
fi

export GOCOVERDIR="$cover_dir"

test_output="$(mktemp "${TMPDIR:-/tmp}/erun-integration-test-output.XXXXXX")"
cleanup_dirs+=("$test_output")

test_parallelism="${INTEGRATION_TEST_PARALLELISM:-${GO_TEST_GOMAXPROCS:-$("$here/../scripts/parallel-gate.sh" width 32 "")}}"
# See the note on the default in the header: this fallback is the same cap the
# Makefile clamps its derived budget to, so no path through this script can
# exceed the deadline harnessexec.HangNet is sized against.
test_timeout="${INTEGRATION_TEST_TIMEOUT:-45m}"

if [[ "$update_golden" -eq 1 ]]; then
    echo ">> reseeding golden files (comparisons disabled, coverage gate skipped)"
    UPDATE_GOLDEN=1 go test -count=1 -parallel="$test_parallelism" -timeout="$test_timeout" ./...
    echo ">> golden files reseeded; inspect the testdata diff, then re-run without --update-golden to gate"
    exit 0
fi

"$here/../scripts/timed-step.sh" "running integration suite (cover dir: $cover_dir, parallel: $test_parallelism, timeout: $test_timeout)" \
    go test -count=1 -parallel="$test_parallelism" -timeout="$test_timeout" ./... 2>&1 | tee "$test_output"

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

# Every process that ran the instrumented binary wrote into its own private
# subdirectory of $cover_dir (see the note above on why). Enumerate them and
# fail loudly if any is empty rather than let a merge step quietly absorb a
# lost emit into a lower, but still plausible-looking, percentage.
proc_cover_dirs=()
while IFS= read -r d; do proc_cover_dirs+=("$d"); done < <(find "$cover_dir" -mindepth 1 -maxdepth 1 -type d | sort)
if [[ "${#proc_cover_dirs[@]}" -eq 0 ]]; then
    echo "!! no per-process coverage directories were created under $cover_dir; the suite's coverage wiring is broken" >&2
    exit 1
fi

empty_cover_dirs=()
nonempty_cover_dirs=()
for d in "${proc_cover_dirs[@]}"; do
    if [[ -n "$(find "$d" -mindepth 1 -maxdepth 1 -type f)" ]]; then
        nonempty_cover_dirs+=("$d")
    else
        empty_cover_dirs+=("$d")
    fi
done
if [[ "${#empty_cover_dirs[@]}" -gt 0 ]]; then
    echo "!! ${#empty_cover_dirs[@]} of ${#proc_cover_dirs[@]} per-process coverage directories have no data (a lost coverage emit):" >&2
    printf '  %s\n' "${empty_cover_dirs[@]}" >&2
    exit 1
fi

merged_dir="$(mktemp -d "${TMPDIR:-/tmp}/erun-integration-cover-merged.XXXXXX")"
cleanup_dirs+=("$merged_dir")
joined_cover_dirs="$(IFS=,; echo "${nonempty_cover_dirs[*]}")"

"$here/../scripts/timed-step.sh" "merging ${#nonempty_cover_dirs[@]} per-process coverage directories" \
    go tool covdata merge -i="$joined_cover_dirs" -o="$merged_dir"

"$here/../scripts/timed-step.sh" "merging coverage counters into $profile" \
    go tool covdata textfmt -i="$merged_dir" -o="$profile"

cover_func_started=$(date +%s)
# One `go tool cover -func` pass, reused for both the printed tail and the
# total: the profile covers every production package, so parsing it is
# expensive enough that doing it twice was measurably the largest single step
# in the in-build gate. The output is line-oriented and ends with the total,
# so tail -20 / tail -1 read the same buffer instead of re-deriving it.
cover_func_output=$(go tool cover -func="$profile")
# Self-timed like every other marker here: a marker printed before its
# work leaves the profiler deriving a span from the gap to the next one,
# which under `make -j` measures an elapsed window rather than work.
echo ">> coverage by function (last line is the total): [$(($(date +%s) - cover_func_started))s]"
printf '%s\n' "$cover_func_output" | tail -20

total_line=$(printf '%s\n' "$cover_func_output" | tail -1)
total_pct=$(awk '{ gsub(/%/, "", $NF); print $NF }' <<<"$total_line")

awk -v got="$total_pct" -v want="$threshold" '
    BEGIN {
        # Compare the rounded values, which is what the message shows: the
        # total is already the one-decimal figure `go tool cover -func`
        # reports, and a threshold that fails by less than that has no way to
        # say so without reporting a shortfall of 0.0.
        got = sprintf("%.1f", got) + 0
        want = sprintf("%.1f", want) + 0
        if (got < want) {
            printf("\n!! coverage %.1f%% is below threshold %.1f%% (short by %.1f)\n", got, want, want - got) > "/dev/stderr"
            exit 1
        }
        printf("\nok  coverage %.1f%% (>= %.1f%%)\n", got, want)
    }
'
