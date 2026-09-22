#!/bin/sh

# Tests for parallel-gate.sh, both of its modes:
#
# - `width`: the fan-out-sizing helper #1702 added so the Makefile's gate
#   widths stop ignoring memory the way #1701 left them. Covers every fallback
#   branch (cgroup v2, cgroup v1, unlimited quota, no cgroup at all, and no
#   `nproc` at all) plus the memory term actually lowering the width below
#   job-count/cpu when the environment is small. `cpu-quota` shares that
#   override chain.
# - the runner: the bounded-concurrency fan-out that `check-gate`'s lint,
#   test-frontend and helm-chart-tests targets all run their scripts through,
#   and that #1690 made aggregate and output-buffered rather than fail-fast
#   and interleaved. The runner section at the end of this file pins that
#   behaviour; without it the suite could pass while the runner regressed.
#
# Run directly (not wired into `make check`), same reasoning as
# agent-gate_test.sh: this drives PARALLEL_GATE_CGROUP_ROOT against synthetic
# cgroup trees and asserts process/exit behaviour against the real gate.

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
# memory on the same environment (the Makefile's lint/test-frontend/
# helm-chart-tests targets each reserve room for one another, since
# check-gate's own -j fan-out can run any of the three at once). Reuse the
# "mem-binds" 2GiB shape: with no reservation, 2GiB/700MiB/job = 2; reserving
# 1024MiB leaves ~1GiB, which still divides to 1 (floored, never 0).
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

# --- runner mode. These are the behaviours whose regression would change or
# hide a gate verdict: a failing task's exit status dropped, a task skipped
# after an earlier failure, a failure left unnamed, a block emitted in
# completion order instead of input order, output interleaved across
# concurrent tasks, a freed slot left unfilled. #1690 made the reporting
# aggregate and the output buffered; both are pinned here.
#
# The gate is always invoked directly, never under an outer `timeout`: a run
# clipped by one reports a failure that is not the behaviour under test, and
# tells the next reader nothing about the runner. (agent-gate_test.sh covers
# the outer-timeout case for its own gate, which distinguishes one.)

runner_dir="${work_root}/runner"
mkdir -p "$runner_dir"

# task <name> <marker> <command> -- one runner input line.
task() {
	printf '%s\t%s\t%s\n' "$1" "$2" "$3"
}

# run_gate <width> <prefix> <out-file> <err-file> <task-line>... -- sets `rc`
# to the gate's exit status. Callers assert on `rc` rather than letting
# `set -e` abort the suite, since a non-zero status is the expected result in
# some of these cases.
run_gate() {
	rg_width="$1"
	rg_prefix="$2"
	rg_out="$3"
	rg_err="$4"
	shift 4
	rc=0
	printf '%s\n' "$@" | "$gate" "$rg_width" "$rg_prefix" >"$rg_out" 2>"$rg_err" || rc=$?
}

# marker_names <out-file>: the ">> " marker lines in emission order, with the
# trailing "[<secs>s]" measured-duration suffix stripped -- the suffix varies
# per run, the names and their order must not.
marker_names() {
	sed -n 's/^>> \(.*\) \[[0-9][0-9]*s\]$/\1/p' "$1"
}

# bodies <out-file>: every non-marker line, in emission order.
bodies() {
	grep -v '^>> ' "$1" || true
}

# --- aggregate failure reporting, exit status, and full execution. Five tasks
# at width 3, two of them failing. Every task must run -- a regression to the
# fail-fast `|| exit 1` that #1690 replaced would stop at bravo and leave
# charlie, delta and echo5 unstarted -- both failures must be named on the
# single "failed in:" line, and the status must be non-zero.
agg_out="${runner_dir}/aggregate.out"
agg_err="${runner_dir}/aggregate.err"
run_gate 3 synthetic-gate "$agg_out" "$agg_err" \
	"$(task alpha alpha 'echo alpha-ok')" \
	"$(task bravo bravo 'echo bravo-FAILS; exit 3')" \
	"$(task charlie charlie 'echo charlie-ok')" \
	"$(task delta delta 'echo delta-FAILS; exit 7')" \
	"$(task echo5 echo5 'echo echo5-ok')"

[ "$rc" -eq 1 ] || fail "runner aggregate: expected exit 1 when any task fails, got $rc"

want_names=$(printf 'alpha\nbravo\ncharlie\ndelta\necho5')
got_names=$(marker_names "$agg_out")
[ "$got_names" = "$want_names" ] ||
	fail "runner aggregate: expected every task's block in input order, got: $(printf '%s' "$got_names" | tr '\n' ' ')"

got_bodies=$(bodies "$agg_out")
for want in alpha-ok bravo-FAILS charlie-ok delta-FAILS echo5-ok; do
	case "$got_bodies" in
	*"$want"*) ;;
	*) fail "runner aggregate: '$want' missing -- a task after a failure did not run: $got_bodies" ;;
	esac
done

# Both failures named on one line, and nothing else on stderr.
want_err="synthetic-gate failed in: bravo delta"
got_err=$(cat "$agg_err")
[ "$got_err" = "$want_err" ] ||
	fail "runner aggregate: expected stderr '$want_err', got '$got_err'"

# --- the same path with nothing failing must report success and stay silent
# on stderr. Width 2 with three tasks also runs the refill path below its
# batch size.
ok_out="${runner_dir}/all-pass.out"
ok_err="${runner_dir}/all-pass.err"
run_gate 2 some-gate "$ok_out" "$ok_err" \
	"$(task one one 'echo one-ok')" \
	"$(task two two 'echo two-ok')" \
	"$(task three three 'echo three-ok')"

[ "$rc" -eq 0 ] || fail "runner all-pass: expected exit 0 when every task passes, got $rc"
[ ! -s "$ok_err" ] || fail "runner all-pass: expected empty stderr, got: $(cat "$ok_err")"
[ "$(marker_names "$ok_out")" = "$(printf 'one\ntwo\nthree')" ] ||
	fail "runner all-pass: expected three blocks in input order, got: $(marker_names "$ok_out" | tr '\n' ' ')"

# --- output atomicity and emission order under concurrency. Three tasks at
# width 3, each emitting 40 lines; the first waits for both of the others to
# finish before emitting anything, so it is the LAST to complete while being
# the FIRST in the list -- an input-ordered replay and a completion-ordered one
# cannot be confused for each other. Each task's output must land as one
# unbroken block, in input order, under its own marker. Unbuffered concurrent
# writes fragment these blocks into interleaved runs -- the unreadability
# #1690 set out to remove.
atomic_dir="${runner_dir}/atomic"
atomic_out="${runner_dir}/atomic.out"
mkdir -p "$atomic_dir"
# alpha's block is emitted only once both writers are done; the wait is
# bounded so a regression fails this suite instead of hanging it.
atomic_ready="[ -e \"$atomic_dir/bravo-done\" ] && [ -e \"$atomic_dir/charlie-done\" ]"
alpha_cmd="i=0; while [ \"\$i\" -lt 100 ]; do if $atomic_ready; then break; fi; sleep 0.1; i=\$((i+1)); done; $atomic_ready || { echo alpha-starved; exit 1; }; n=0; while [ \"\$n\" -lt 40 ]; do echo alpha-line; n=\$((n+1)); done"
bravo_cmd="n=0; while [ \"\$n\" -lt 40 ]; do echo bravo-line; n=\$((n+1)); done; : > \"$atomic_dir/bravo-done\""
charlie_cmd="n=0; while [ \"\$n\" -lt 40 ]; do echo charlie-line; n=\$((n+1)); done; : > \"$atomic_dir/charlie-done\""
run_gate 3 atomic "$atomic_out" "${runner_dir}/atomic.err" \
	"$(task alpha alpha "$alpha_cmd")" \
	"$(task bravo bravo "$bravo_cmd")" \
	"$(task charlie charlie "$charlie_cmd")"

[ "$rc" -eq 0 ] ||
	fail "runner atomicity: expected exit 0, got $rc (stderr: $(cat "${runner_dir}/atomic.err"))"
want_body=$(for name in alpha bravo charlie; do n=0; while [ "$n" -lt 40 ]; do echo "${name}-line"; n=$((n+1)); done; done)
got_body=$(bodies "$atomic_out")
[ "$got_body" = "$want_body" ] ||
	fail "runner atomicity: expected one unbroken 40-line block per task in input order; runs were: $(printf '%s\n' "$got_body" | uniq -c | tr '\n' ';')"

bad_markers=$(grep '^>> ' "$atomic_out" | grep -v ' \[[0-9][0-9]*s\]$' || true)
[ -z "$bad_markers" ] ||
	fail "runner atomicity: marker line missing its measured '[<secs>s]' duration: $bad_markers"

# --- slot refilling. A freed slot must be refilled as soon as ANY running
# task finishes, not once the whole batch drains: with a batch drain one slow
# task idles every free slot behind it. Three tasks at width 2, where alpha
# waits on a marker file that only charlie creates and charlie is third in the
# list -- it can only run once bravo's slot is refilled. Under a batch-drain
# regression alpha waits out its bounded deadline and the gate fails; with
# refilling it proceeds immediately. The wait is bounded so a regression fails
# this suite instead of hanging it.
refill_marker="${runner_dir}/refill-marker"
rm -f "$refill_marker"
refill_out="${runner_dir}/refill.out"
refill_err="${runner_dir}/refill.err"
run_gate 2 refill "$refill_out" "$refill_err" \
	"$(task alpha alpha "i=0; while [ \"\$i\" -lt 50 ]; do [ -e \"$refill_marker\" ] && exit 0; sleep 0.1; i=\$((i+1)); done; echo alpha-timed-out; exit 1")" \
	"$(task bravo bravo 'echo bravo-ok')" \
	"$(task charlie charlie "echo charlie-ok; : > \"$refill_marker\"")"

[ "$rc" -eq 0 ] ||
	fail "runner refill: expected exit 0 -- charlie's slot is only freed by refilling bravo's, and alpha waits on charlie (stderr: $(cat "$refill_err"))"
got_refill=$(bodies "$refill_out")
case "$got_refill" in
*alpha-timed-out*) fail "runner refill: alpha timed out waiting for charlie -- the runner drained a whole batch instead of refilling a freed slot" ;;
esac
case "$got_refill" in
*charlie-ok*) ;;
*) fail "runner refill: charlie never ran: $got_refill" ;;
esac

echo "ok: parallel-gate.sh runner"
