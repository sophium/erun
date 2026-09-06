#!/bin/sh

# Tests for parallel-gate.sh's `width` mode: the fan-out-sizing helper #1702
# added so the Makefile's gate widths stop ignoring memory the way #1701 left
# them. Covers every fallback branch (cgroup v2, cgroup v1, unlimited quota,
# no cgroup at all, and no `nproc` at all) plus the memory term actually
# lowering the width below job-count/cpu when the environment is small.
#
# Run directly (not wired into `make check`), same reasoning as
# agent-gate_test.sh: this drives PARALLEL_GATE_CGROUP_ROOT against synthetic
# cgroup trees, not the real job-running mode.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
gate="${script_dir}/parallel-gate.sh"

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t parallel-gate-test)"
trap 'rm -rf "${work_root}"' EXIT INT TERM

fail() {
	echo "FAIL: $1" >&2
	exit 1
}

assert_width() {
	label="$1"
	expected="$2"
	shift 2
	got=$("$gate" width "$@")
	[ "$got" = "$expected" ] || fail "$label: expected width $expected, got $got (args: $*)"
}

# --- cgroup v2 present: cpu.max quota/period and memory.max both read. A
# 4-core/8GiB environment (the "ux" env from #1701/#1702) must land on 4 for
# both the lint (6 modules, 700MiB/job) and helm-chart-tests (8 scripts,
# 163MiB/job) shapes, exactly as before this change.
case_dir="${work_root}/v2"
mkdir -p "$case_dir"
echo "400000 100000" >"${case_dir}/cpu.max"
echo "$((8 * 1024 * 1024 * 1024))" >"${case_dir}/memory.max"
PARALLEL_GATE_CGROUP_ROOT="$case_dir" assert_width "v2 lint shape" 4 6 700
PARALLEL_GATE_CGROUP_ROOT="$case_dir" assert_width "v2 helm shape" 4 8 163

# --- cgroup v1 present (no v2 files at all): cpu.cfs_quota_us/period_us and
# memory/memory.limit_in_bytes, same 4-core/8GiB shape, must agree with v2.
case_dir="${work_root}/v1"
mkdir -p "${case_dir}/cpu" "${case_dir}/memory"
echo "400000" >"${case_dir}/cpu/cpu.cfs_quota_us"
echo "100000" >"${case_dir}/cpu/cpu.cfs_period_us"
echo "$((8 * 1024 * 1024 * 1024))" >"${case_dir}/memory/memory.limit_in_bytes"
PARALLEL_GATE_CGROUP_ROOT="$case_dir" assert_width "v1 lint shape" 4 6 700
PARALLEL_GATE_CGROUP_ROOT="$case_dir" assert_width "v1 helm shape" 4 8 163

# --- v1's practical "unlimited" sentinel for memory (~2^63, far past
# Number.MAX_SAFE_INTEGER) must be treated as unlimited, not as a real limit
# that happens to be huge -- the memory term must drop out entirely, leaving
# CPU/job-count to decide the width.
case_dir="${work_root}/v1-huge-mem"
mkdir -p "${case_dir}/cpu" "${case_dir}/memory"
echo "1200000" >"${case_dir}/cpu/cpu.cfs_quota_us"
echo "100000" >"${case_dir}/cpu/cpu.cfs_period_us"
echo "9223372036854771712" >"${case_dir}/memory/memory.limit_in_bytes"
PARALLEL_GATE_CGROUP_ROOT="$case_dir" assert_width "v1 huge sentinel memory is unlimited" 6 6 700

# --- v2 quota "max" (unlimited) and memory.max "max" (unlimited): both terms
# must fall through past the cgroup reads. CPU falls to `nproc`; with the
# real PATH that is whatever this host has, so pin the expectation via a
# stubbed `nproc` instead of trusting the live host's core count.
case_dir="${work_root}/v2-unlimited"
mkdir -p "$case_dir"
echo "max 100000" >"${case_dir}/cpu.max"
echo "max" >"${case_dir}/memory.max"
stub_bin="${work_root}/stub-nproc-12"
mkdir -p "$stub_bin"
cat >"${stub_bin}/nproc" <<'EOF'
#!/bin/sh
echo 12
EOF
chmod +x "${stub_bin}/nproc"
got=$(PATH="${stub_bin}:$PATH" PARALLEL_GATE_CGROUP_ROOT="$case_dir" "$gate" width 6 700)
[ "$got" = 6 ] || fail "v2 unlimited quota+memory: expected job-count cap 6 (cpu falls to stubbed nproc=12), got $got"

# --- no cgroup files at all (bare empty root): both terms fall through to
# non-cgroup sources, same as the "unlimited" case above.
case_dir="${work_root}/empty"
mkdir -p "$case_dir"
got=$(PATH="${stub_bin}:$PATH" PARALLEL_GATE_CGROUP_ROOT="$case_dir" "$gate" width 6 700)
[ "$got" = 6 ] || fail "no cgroup at all: expected job-count cap 6 (cpu falls to stubbed nproc=12), got $got"

# --- nproc itself unavailable/failing: CPU falls to the final constant (4).
stub_bin_fail="${work_root}/stub-nproc-fail"
mkdir -p "$stub_bin_fail"
cat >"${stub_bin_fail}/nproc" <<'EOF'
#!/bin/sh
exit 1
EOF
chmod +x "${stub_bin_fail}/nproc"
got=$(PATH="${stub_bin_fail}:$PATH" PARALLEL_GATE_CGROUP_ROOT="${work_root}/empty" "$gate" width 6 700)
[ "$got" = 4 ] || fail "nproc unavailable: expected the constant fallback 4, got $got"

# --- memory term actually binds: a small memory ceiling must pull the width
# below what CPU/job-count alone would allow, proving the memory term isn't
# just plumbed through inert.
case_dir="${work_root}/mem-binds"
mkdir -p "$case_dir"
echo "1200000 100000" >"${case_dir}/cpu.max"
echo "$((2 * 1024 * 1024 * 1024))" >"${case_dir}/memory.max"
# 12 CPUs, 6 job-count cap, but 2GiB / 700MiB/job = 2 -- memory must win.
PARALLEL_GATE_CGROUP_ROOT="$case_dir" assert_width "memory term binds below cpu/job-count" 2 6 700

# --- job-count is always a hard ceiling, even with abundant CPU and memory.
case_dir="${work_root}/plenty"
mkdir -p "$case_dir"
echo "1200000 100000" >"${case_dir}/cpu.max"
echo "$((64 * 1024 * 1024 * 1024))" >"${case_dir}/memory.max"
PARALLEL_GATE_CGROUP_ROOT="$case_dir" assert_width "job-count caps an abundant environment" 3 3 700

# --- the optional 4th arg (reserved-mem-mib) subtracts a flat amount from
# the read memory ceiling before dividing by mem-per-job-mib, for a caller
# sizing a job batch that runs concurrently with something else also using
# memory on the same environment (check-gate-early's erun-app pre-build,
# which is not itself one of the width-bounded jobs). Reuse the "mem-binds"
# 2GiB shape: with no reservation, 2GiB/700MiB/job = 2; reserving 1024MiB
# leaves ~1GiB, which still divides to 1 (floored, never 0).
case_dir="${work_root}/mem-binds"
PARALLEL_GATE_CGROUP_ROOT="$case_dir" assert_width "reserved-mem-mib lowers the width" 1 6 700 1024

# --- reserving more than the entire ceiling floors at 1, never 0 or negative
# -- a job list must still make forward progress.
PARALLEL_GATE_CGROUP_ROOT="$case_dir" assert_width "reserved-mem-mib exceeding the ceiling floors at 1" 1 6 700 4096

# --- omitting reserved-mem-mib entirely (existing 3-arg callers) behaves
# exactly as before this parameter was added.
PARALLEL_GATE_CGROUP_ROOT="$case_dir" assert_width "omitted reserved-mem-mib defaults to 0" 2 6 700

# --- PARALLEL_GATE_MEMORY_LIMIT_MIB overrides the cgroup read outright, for
# a BuildKit RUN step where memory.max reads "max" (unlimited) even though
# the sidecar's chart-declared limit is real -- see the script's own header
# comment for why the cgroup read cannot see that number in this context.
# Reuse the "v2-unlimited" cgroup shape (memory.max="max") to prove the
# override, not the cgroup file, is what wins.
case_dir="${work_root}/v2-unlimited"
got=$(PATH="${stub_bin}:$PATH" PARALLEL_GATE_CGROUP_ROOT="$case_dir" PARALLEL_GATE_MEMORY_LIMIT_MIB=1400 "$gate" width 6 700)
[ "$got" = 2 ] || fail "memory override binds despite unlimited memory.max: expected 2 (1400MiB/700MiB per job), got $got"

# --- the override is ignored when it is not a positive integer, falling
# back to the cgroup read (or further, per the existing fallback chain).
got=$(PATH="${stub_bin}:$PATH" PARALLEL_GATE_CGROUP_ROOT="$case_dir" PARALLEL_GATE_MEMORY_LIMIT_MIB=bogus "$gate" width 6 700)
[ "$got" = 6 ] || fail "non-numeric memory override is ignored: expected job-count cap 6, got $got"

# --- PARALLEL_GATE_CPU_LIMIT overrides the CPU quota read outright, for the
# same BuildKit RUN step shape as the memory override above: cpu.max also
# reads "max" (unlimited) there, so cpu_quota() would otherwise fall through
# to `nproc` -- the host node's real core count, not the dind sidecar's
# configured limit (erun#2081). Reuse the "v2-unlimited" cgroup shape
# (cpu.max="max") to prove the override, not the cgroup file or `nproc`, is
# what wins.
case_dir="${work_root}/v2-unlimited"
got=$(PATH="${stub_bin}:$PATH" PARALLEL_GATE_CGROUP_ROOT="$case_dir" PARALLEL_GATE_CPU_LIMIT=2 "$gate" width 6 700)
[ "$got" = 2 ] || fail "cpu override binds despite unlimited cpu.max: expected 2, got $got"

# --- the CPU override is ignored when it is not a positive integer, falling
# back to the cgroup read (or further, per the existing fallback chain, which
# lands on the stubbed nproc=12 here, capped by the job-count of 6).
got=$(PATH="${stub_bin}:$PATH" PARALLEL_GATE_CGROUP_ROOT="$case_dir" PARALLEL_GATE_CPU_LIMIT=bogus "$gate" width 6 700)
[ "$got" = 6 ] || fail "non-numeric cpu override is ignored: expected job-count cap 6, got $got"

# --- `cpu-quota` mode (erun#2266) prints cpu_quota()'s result standalone, so
# the Makefile's LINT_TIMEOUT scaling can reuse the same override chain
# `width` already exercises above instead of re-deriving it. Cover the
# override and the cgroup-v2 read; the rest of the fallback chain (cgroup v1,
# nproc, constant) is already proven against the same cpu_quota() function by
# the `width` assertions above.
assert_cpu_quota() {
	label="$1"
	expected="$2"
	shift 2
	got=$("$gate" cpu-quota)
	[ "$got" = "$expected" ] || fail "$label: expected cpu-quota $expected, got $got"
}

case_dir="${work_root}/v2"
PARALLEL_GATE_CGROUP_ROOT="$case_dir" assert_cpu_quota "cpu-quota reads cgroup v2 cpu.max" 4

got=$(PARALLEL_GATE_CPU_LIMIT=4 "$gate" cpu-quota)
[ "$got" = 4 ] || fail "cpu-quota honors PARALLEL_GATE_CPU_LIMIT=4: got $got"
got=$(PARALLEL_GATE_CPU_LIMIT=8 "$gate" cpu-quota)
[ "$got" = 8 ] || fail "cpu-quota honors PARALLEL_GATE_CPU_LIMIT=8: got $got"
got=$(PARALLEL_GATE_CPU_LIMIT=24 "$gate" cpu-quota)
[ "$got" = 24 ] || fail "cpu-quota honors PARALLEL_GATE_CPU_LIMIT=24: got $got"

echo "ok: parallel-gate.sh width"
echo "ok: parallel-gate.sh cpu-quota"
