#!/bin/sh
# Runs one gate module's golangci-lint and decides the gate's verdict from
# golangci-lint's own report rather than from golangci-lint's exit status.
#
# Usage: lint-module.sh <gomaxprocs> <timeout> <module-dir>
#
# Why the exit status cannot be the verdict. golangci-lint prints its whole
# report -- the findings, or "0 issues." -- and only afterwards decides how to
# exit, and that decision consults the run's own deadline *before* it consults
# the report: when --timeout expires the run exits as a timeout even though
# the analysis finished and printed a complete, clean result. The exit status
# therefore cannot tell a lint that found nothing from a lint that found
# something, and the gate asks the first question. It is not hypothetical:
# erun-backend-api's analysis completed with "0 issues." and was reported as a
# lint failure at 912s and again at 1296s against the 15m floor this target
# passes, under check-gate's fan-out.
#
# What still fails, and has to. A run that never printed a report -- a package
# load that exceeded the deadline, a linter that errored, a module whose
# config is broken -- has no verdict to read, and passing it would report a
# green gate for a module nothing analyzed. Those stay red: the report line
# has to be there, and no error may accompany it.

set -u

gomaxprocs=$1
timeout=$2
module=$3

report=$(mktemp)
trap 'rm -f "$report"' EXIT INT TERM

# --show-stats is pinned rather than left to the default: the verdict below
# reads the report line it controls, so a module that turned it off in its own
# .golangci.yml would otherwise put this script back to reading exit statuses.
(
	cd "$module" &&
		GOMAXPROCS="$gomaxprocs" exec golangci-lint run \
			--allow-parallel-runners \
			--show-stats \
			--timeout "$timeout" \
			./...
) >"$report" 2>&1
status=$?

# The gate log still carries golangci-lint's own output in full, including on
# the path below that does not fail the target.
cat "$report"

if [ "$status" -eq 0 ]; then
	exit 0
fi

# "0 issues." is printed only once the analysis has run to completion with
# every linter returning: a lint that failed to produce a result errors out
# before any report is printed. So a complete, clean report whose only
# accompanying complaint is the deadline means the lint did its job and the
# clock, not the lint, is what failed.
#
# The error count is what keeps this from widening: a report line on its own
# is not enough, because a run that logged its own analysis error and still
# printed "0 issues." would otherwise pass on the strength of the report line
# alone -- and its zero is not trustworthy.
if grep -qxF '0 issues.' "$report" &&
	grep -qF 'Timeout exceeded' "$report" &&
	[ "$(grep -c '^level=error' "$report")" -eq 1 ]; then
	echo "lint-module.sh: $module reported 0 issues and then exceeded its ${timeout} deadline;" >&2
	echo "lint-module.sh: the analysis completed and found nothing, so the deadline is not a finding and the gate is not failed on it." >&2
	exit 0
fi

exit "$status"
