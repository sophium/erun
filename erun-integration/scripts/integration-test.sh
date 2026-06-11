#!/bin/bash
# Run the erun-integration suite with coverage instrumentation enabled and
# fail if total coverage of the production packages drops below the threshold.
#
# Usage:
#   ./scripts/integration-test.sh [-threshold=NN]
#
# Environment:
#   COVERAGE_THRESHOLD   default 73 (percent). See note below.
#   GOCOVERDIR           override the directory used for raw counter files;
#                        defaults to ./coverage/raw under the script.
#   UPDATE_GOLDEN=1      regenerate golden output files instead of comparing.
#
# Notes:
#   - The instrumented binary is rebuilt each run so signatures stay aligned
#     with whatever code is being tested. The build uses the same -coverpkg
#     selector as the binary helper in internal/erun, so the merged profile
#     reflects exactly the production packages we want to gate on.
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

set -euo pipefail

threshold="${COVERAGE_THRESHOLD:-73}"
while [[ $# -gt 0 ]]; do
    case "$1" in
        -threshold=*) threshold="${1#-threshold=}" ;;
        --threshold=*) threshold="${1#--threshold=}" ;;
        *) echo "unknown arg: $1" >&2; exit 2 ;;
    esac
    shift
done

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$here"

cover_dir="${GOCOVERDIR:-$here/coverage/raw}"
profile="$here/coverage/profile.txt"
mkdir -p "$cover_dir"
mkdir -p "$(dirname "$profile")"
rm -rf "$cover_dir"/*

export GOCOVERDIR="$cover_dir"

echo ">> running integration suite (cover dir: $cover_dir)"
go test -count=1 ./...

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
