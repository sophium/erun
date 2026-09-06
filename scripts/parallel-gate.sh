#!/usr/bin/env sh
# Runs a list of independent shell commands with bounded concurrency,
# buffering each command's combined stdout/stderr so concurrent output never
# interleaves, then emits every buffered block in input order under its own
# marker line and aggregates every failure into one final report -- the same
# contract the serial `for m in ...; do ...; done` loops it replaces had:
# run everything, name every failure, exit non-zero iff anything failed.
#
# Each job is one line on stdin, tab-separated:
#   <short-name>\t<marker-text>\t<command>
# - short-name: bare identifier used in the aggregated failure line
# - marker-text: text printed after ">> " (kept identical to the prior
#   serial recipes' markers so log-grepping and tooling keep working)
# - command: run via `sh -c`
#
# Usage: parallel-gate.sh <max-parallel> <failure-message-prefix>
#
# Exit status is 0 iff every job exits 0. On any failure, prints
# "<failure-message-prefix> failed in:<space-separated short-names>" to
# stderr (matching the exact "lint failed in:..." text the lint recipe
# already produced) and exits 1.
#
# A second mode answers a different question -- not "run these jobs bounded
# by a width", but "what should that width even be":
#
#   Usage: parallel-gate.sh width <job-count> <mem-per-job-mib>
#
# Prints one integer: min(job-count, CPUs available to this environment,
# memory available / mem-per-job-mib). Kept in this script rather than a
# separate one because #1702 found two independent parallelism sizers in this
# repo (this file's Makefile callers, and erun-ui/playwright/playwright.config.ts)
# that disagreed about which resource is the ceiling; this is now the one
# shell-side answer, read the same cgroup files with the same fallbacks the
# TypeScript side already used for its memory ceiling. The TypeScript side
# doesn't need a matching CPU rewrite: it already gets the CPU quota (not the
# affinity mask) for free from Node's os.availableParallelism(), which is
# quota-aware via libuv -- see that file's own comment. `nproc` is not
# quota-aware (it reads sched_getaffinity), so the shell side has to read the
# quota itself.
#
# CPU: cgroup v2 cpu.max (quota/period), then cgroup v1
# cpu.cfs_quota_us/cpu.cfs_period_us, then `nproc`, then a constant. A quota
# of "max" (v2) or -1 (v1) means unlimited and falls through to the next
# source, same as an unreadable/absent file.
#
# Memory: cgroup v2 memory.max, then cgroup v1 memory/memory.limit_in_bytes,
# then unlimited (the memory term is dropped from the min() entirely, since
# there is nothing to divide by). A value of "max" (v2) or >= 2^53-1 (v1's
# practical unlimited sentinel, mirroring playwright.config.ts's
# `Number.MAX_SAFE_INTEGER` check -- the same value cannot be represented
# exactly as a JS number, so that is the largest limit either side can treat
# as real) means unlimited.
#
# PARALLEL_GATE_CGROUP_ROOT overrides the cgroup root (default
# /sys/fs/cgroup) so tests can point at a synthetic tree instead of faking
# /sys/fs/cgroup itself.
#
# PARALLEL_GATE_MEMORY_LIMIT_MIB overrides the memory ceiling outright,
# skipping the cgroup reads above. This exists because a BuildKit `RUN` step
# (the erun-devops image's own test stage, which is where this script sizes
# LINT_PARALLELISM/HELM_CHART_TEST_PARALLELISM for the in-build `make check`
# gate) runs in a cgroup that is a SIBLING of the pod's own limited cgroup,
# not a descendant of it -- cgroup v2's memory.max reads "max" (unlimited)
# there regardless of what the erun-dind sidecar's chart-declared memory
# limit actually is, so the cgroup-based reads above are structurally blind
# in exactly the context this script exists to size. There is no cgroup file
# in that context that reflects the intended ceiling; the erun-devops
# Dockerfile threads the sidecar's own configured limit in through this
# variable instead so the width calculation still has a real number to divide
# by (see DIND_MEMORY_LIMIT_MIB in that Dockerfile).
#
# PARALLEL_GATE_CPU_LIMIT is the same override for the CPU term, for the
# identical reason: cpu.max/cpu.cfs_quota_us also read unlimited in that
# sibling cgroup, so cpu_quota() falls through to `nproc`, which reports the
# host node's real core count (sched_getaffinity, not the sidecar's cgroup
# quota) -- oversized for an environment that does not own that many cores
# exclusively (erun#2081). The erun-devops Dockerfile threads the sidecar's
# own configured CPU limit in through this variable the same way it does for
# memory (see DIND_CPU_LIMIT in that Dockerfile).
#
# A third mode answers a narrower question than either of the above -- not a
# job-fan-out width, just the resolved CPU quota itself:
#
#   Usage: parallel-gate.sh cpu-quota
#
# Prints cpu_quota()'s result on its own, so a caller that needs the raw
# number (the Makefile's LINT_TIMEOUT scaling, see erun#2266) can reuse the
# exact same override chain -- PARALLEL_GATE_CPU_LIMIT, then cgroup v2, then
# cgroup v1, then `nproc`, then the constant fallback -- instead of
# re-implementing cgroup reads a second time.
cgroup_root="${PARALLEL_GATE_CGROUP_ROOT:-/sys/fs/cgroup}"
# JS's Number.MAX_SAFE_INTEGER (2^53 - 1). cgroup v1's unlimited sentinel
# for memory.limit_in_bytes is ~2^63, far above this, and cannot itself be
# represented exactly as a JS number -- so this is the largest limit value
# playwright.config.ts's parallel memory check can treat as real, and this
# script matches it rather than trusting a v1 host's literal sentinel value.
max_safe_int=9007199254740991

is_positive_int() {
	case "$1" in
	'' | *[!0-9]*) return 1 ;;
	*) [ "$1" -gt 0 ] ;;
	esac
}

cpu_quota() {
	if is_positive_int "${PARALLEL_GATE_CPU_LIMIT:-}"; then
		echo "$PARALLEL_GATE_CPU_LIMIT"
		return
	fi
	if [ -r "$cgroup_root/cpu.max" ]; then
		read -r quota period <"$cgroup_root/cpu.max" 2>/dev/null || quota=""
		if [ "${quota:-}" != "max" ] && is_positive_int "${quota:-}" && is_positive_int "${period:-}"; then
			echo $((quota / period))
			return
		fi
	fi
	if [ -r "$cgroup_root/cpu/cpu.cfs_quota_us" ] && [ -r "$cgroup_root/cpu/cpu.cfs_period_us" ]; then
		quota=$(cat "$cgroup_root/cpu/cpu.cfs_quota_us" 2>/dev/null) || quota=""
		period=$(cat "$cgroup_root/cpu/cpu.cfs_period_us" 2>/dev/null) || period=""
		if is_positive_int "$quota" && is_positive_int "$period"; then
			echo $((quota / period))
			return
		fi
	fi
	n=$(nproc 2>/dev/null) || n=""
	if is_positive_int "$n"; then
		echo "$n"
		return
	fi
	echo 4
}

# mem_limit_mib prints the memory ceiling in MiB, or nothing when
# unlimited/unreadable -- an empty result means "drop the memory term",
# not "zero memory available".
mem_limit_mib() {
	if is_positive_int "${PARALLEL_GATE_MEMORY_LIMIT_MIB:-}"; then
		echo "$PARALLEL_GATE_MEMORY_LIMIT_MIB"
		return
	fi
	if [ -r "$cgroup_root/memory.max" ]; then
		val=$(cat "$cgroup_root/memory.max" 2>/dev/null) || val=""
		if [ "$val" != "max" ] && is_positive_int "$val" && [ "$val" -lt "$max_safe_int" ]; then
			echo $((val / 1024 / 1024))
			return
		fi
	fi
	if [ -r "$cgroup_root/memory/memory.limit_in_bytes" ]; then
		val=$(cat "$cgroup_root/memory/memory.limit_in_bytes" 2>/dev/null) || val=""
		if is_positive_int "$val" && [ "$val" -lt "$max_safe_int" ]; then
			echo $((val / 1024 / 1024))
			return
		fi
	fi
}

if [ "${1:-}" = "width" ]; then
	set -eu
	job_count=$2
	mem_per_job_mib=$3

	width=$job_count
	cpu=$(cpu_quota)
	if is_positive_int "$cpu" && [ "$cpu" -lt "$width" ]; then
		width=$cpu
	fi
	mem_mib=$(mem_limit_mib)
	if [ -n "$mem_mib" ] && is_positive_int "$mem_per_job_mib"; then
		by_mem=$((mem_mib / mem_per_job_mib))
		[ "$by_mem" -ge 1 ] || by_mem=1
		if [ "$by_mem" -lt "$width" ]; then
			width=$by_mem
		fi
	fi
	echo "$width"
	exit 0
fi

if [ "${1:-}" = "cpu-quota" ]; then
	set -eu
	cpu_quota
	exit 0
fi

set -eu

max_parallel=$1
prefix=$2

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

tab=$(printf '\t')
i=0
running=0
while IFS="$tab" read -r short_name marker cmd; do
	i=$((i + 1))
	printf '%s' "$short_name" > "$tmp/$i.name"
	printf '%s' "$marker" > "$tmp/$i.marker"
	(
		if sh -c "$cmd" > "$tmp/$i.out" 2>&1; then
			echo 0 > "$tmp/$i.rc"
		else
			echo 1 > "$tmp/$i.rc"
		fi
	) &
	running=$((running + 1))
	if [ "$running" -ge "$max_parallel" ]; then
		wait
		running=0
	fi
done
wait

total=$i
failed=""
j=1
while [ "$j" -le "$total" ]; do
	echo ">> $(cat "$tmp/$j.marker")"
	cat "$tmp/$j.out"
	if [ "$(cat "$tmp/$j.rc")" != "0" ]; then
		failed="$failed $(cat "$tmp/$j.name")"
	fi
	j=$((j + 1))
done

if [ -n "$failed" ]; then
	echo "$prefix failed in:$failed" >&2
	exit 1
fi
