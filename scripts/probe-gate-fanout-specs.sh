#!/usr/bin/env bash
# One arm of the gate-fan-out A/B, on an emulated 4-CPU gate box.
#
# Runs the same gate-shaped `make -j4` fan-out `make check` runs, narrowed to
# the targets that matter for this question, with test-playwright in the set
# and pointed at the two specs under investigation. Records per-spec outcome,
# per-target wall clock (timestamped markers), wall clock, CPU-seconds, pod
# CPU pressure stall and peak runnable threads.
#
# The budget is emulated on two axes because neither one alone is enough:
#   taskset -c 0-3                 gives the arm 4 real cores of affinity
#   PARALLEL_GATE_CPU_LIMIT=4      makes parallel-gate.sh's cpu_quota() (which
#                                  reads the cgroup, not affinity -- this pod's
#                                  quota is 12) report the 4-CPU gate box, so
#                                  CHECK_GATE_PARALLELISM and every fan-out
#                                  width resolve as they do on that box
#
# AGENT_GATE_DETACHED=1 keeps run.sh from re-exec'ing itself through
# scripts/agent-gate.sh inside this pod; a detached job would escape the
# taskset mask and invalidate the arrangement.
#
# Usage: probe-gate-fanout-specs.sh <fix|ctrl> <run-tag> [cap-seconds]
set -u

arm=$1
tag=$2
cap=${3:-900}

ROOT=/home/erun/git/erun
cd "$ROOT" || exit 2

SPEC_ORCH="tests/areas/orchestrator/orchestrator-directories.spec.ts"
SPEC_REV="tests/areas/review/review-directory-target.spec.ts"

# Gate-shaped target set: the three fan-outs the fix narrows (lint,
# test-frontend, helm-chart-tests) plus the flat Go module targets that keep
# the box loaded while test-playwright's specs execute -- without those, the
# fan-outs finish before test-playwright's dependency chain lets it start and
# the specs run uncontended, which measures nothing.
TARGETS="test-erun-ui test-erun-common test-erun-backend-api test-erun-mcp lint test-frontend helm-chart-tests test-playwright"

case "$arm" in
fix)
	# What `check` passes post-fix on a 4-CPU box: r = 4-1 = 3 (capped at
	# cpu-1 = 3), share = 4/4 = 1.
	armvars="CHECK_GATE_FANOUT_RESERVED_CPUS=3 CHECK_GATE_TARGET_CPU_SHARE=1"
	;;
ctrl)
	# Pre-fix `check` passes no CPU reservation at all; the fan-outs size
	# themselves against the whole 4-CPU quota (width 4 each).
	armvars=""
	;;
*)
	echo "arm must be fix|ctrl" >&2
	exit 2
	;;
esac

log="/tmp/probe/arm-$arm-$tag.log"
cmdlog="/tmp/probe/arm-$arm-$tag.cmd.log"
: >"$log"

read_usec() { awk '/^usage_usec/{print $2}' /sys/fs/cgroup/cpu.stat; }
read_psi() { awk -F'avg10=' '/^some/{split($2,a," "); print a[1]}' /sys/fs/cgroup/cpu.pressure; }
runnable() { awk '$3=="R"{n++} END{print n+0}' /proc/[0-9]*/task/*/stat 2>/dev/null; true; }

start_usec=$(read_usec); start_ts=$(date +%s%N)
psi_sum=0; psi_n=0; psi_max=0; run_max=0

# shellcheck disable=SC2086
env AGENT_GATE_DETACHED=1 \
	PARALLEL_GATE_CPU_LIMIT=4 \
	PLAYWRIGHT_TEST_AREAS=all \
	PROBE_PLAYWRIGHT_TARGETS="$SPEC_ORCH $SPEC_REV" \
	taskset -c 0-3 timeout "$cap" make -j4 $armvars $TARGETS 2>&1 \
	| perl -pe 'BEGIN { use Time::HiRes (); $s = Time::HiRes::time() } s/^/sprintf("[%7.1f] ", Time::HiRes::time() - $s)/e' \
	>"$cmdlog" &
pid=$!
while kill -0 "$pid" 2>/dev/null; do
	p=$(read_psi); r=$(runnable)
	psi_sum=$(awk -v s="$psi_sum" -v v="$p" 'BEGIN{print s+v}'); psi_n=$((psi_n+1))
	psi_max=$(awk -v a="$psi_max" -v b="$p" 'BEGIN{print (b>a)?b:a}')
	[ "$r" -gt "$run_max" ] && run_max=$r
	sleep 1
done
wait "$pid"; rc=$?
end_ts=$(date +%s%N); end_usec=$(read_usec)

wall=$(awk -v a="$start_ts" -v b="$end_ts" 'BEGIN{printf "%.1f", (b-a)/1e9}')
cpus=$(awk -v a="$start_usec" -v b="$end_usec" -v w="$wall" 'BEGIN{printf "%.2f", (b-a)/1e6/w}')
mean_psi=$(awk -v s="$psi_sum" -v n="$psi_n" 'BEGIN{if(n)printf "%.1f", s/n; else print "n/a"}')

{
	echo "===== ARM: $arm  run=$tag  (4-CPU budget, wall-cap ${cap}s) ====="
	echo "  tree               : $(git rev-parse HEAD)  $(git rev-parse --abbrev-ref HEAD)"
	echo "  make vars          : ${armvars:-<none>}"
	echo "  exit status        : $rc  (124 = hit the cap, so NOT a verdict)"
	echo "  wall clock         : ${wall}s"
	echo "  CPU-seconds used   : $(awk -v a="$start_usec" -v b="$end_usec" 'BEGIN{printf "%.1f", (b-a)/1e6}')"
	echo "  mean CPU-equivs    : ${cpus}   (of 4 available)"
	echo "  cpu PSI some avg10 : mean ${mean_psi}%  peak ${psi_max}%"
	echo "  pod runnable peak  : ${run_max} threads"
	echo "  fan-out widths resolved:"
	grep -oE "golangci-lint [^ ]+ \[[0-9]+s\]|running integration suite \([^)]*\)" "$cmdlog" | head -3 | sed 's/^/    /'
	echo "  per-target markers :"
	grep -E '^\[' "$cmdlog" | grep -E '>> .*\[[0-9]+s\]' | sed -E 's/^(\[[^]]*\]) >> /\1 /' | tail -25
	echo "  --- playwright: two specs under investigation ---"
	grep -E "${SPEC_ORCH##*/}|${SPEC_REV##*/}" "$cmdlog" | grep -E '›|✓|✘|×|failed|passed' | sed 's/^/    /'
	echo "  --- playwright: full summary ---"
	grep -E '[0-9]+ (passed|failed|flaky|skipped|did not run)' "$cmdlog" | tail -4 | sed 's/^/    /'
	echo "  --- playwright: every non-pass line in the run ---"
	grep -E '✘|×|failed|Error:|expect\(' "$cmdlog" | head -20 | sed 's/^/    /'
} | tee -a "$log"
