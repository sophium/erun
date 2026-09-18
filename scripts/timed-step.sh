#!/usr/bin/env sh
# Run one step and report how long it actually took, in the form erun's build
# profiler reads.
#
# A phase marker printed *before* its work carries no duration, so the profiler
# derives one from the gap to the next marker
# (erun-common/build_progress_phases.go). Under `make -j` that gap is an
# elapsed window shared with everything else running at the time, not work
# done -- which is why such rows are labelled "(elapsed window, concurrent)"
# rather than presented as costs. A marker carrying its own `[Ns]` is preferred
# over that derivation, so a step that measures itself is the only one whose
# reported cost is real.
#
# The marker is therefore printed on completion, not on entry. The step's own
# output still streams while it runs, so nothing is hidden in the meantime.
set -eu

if [ "$#" -lt 2 ]; then
    echo 'usage: timed-step.sh <marker> <command> [args...]' >&2
    exit 2
fi

marker=$1
shift

started=$(date +%s)
status=0
"$@" || status=$?
# Reported even when the step failed: a step that burned four minutes before
# failing is exactly the one worth seeing in the profile.
printf '>> %s [%ss]\n' "$marker" "$(($(date +%s) - started))"
exit "$status"
