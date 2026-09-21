#!/usr/bin/env sh
# Reproduce the merge gate's Playwright failures off the gate.
#
# WHY THIS EXISTS
#
# A class of desktop Playwright spec failure reproduces only inside the merge
# gate, where each attempt costs a full ~25-minute build. The variable is not
# the CPU budget on its own: the suite measured GREEN (170 of 170) on four
# CPUs with two workers when it had the box to itself, both under
# `taskset -c 0-3` and under `docker run --cpus=4`. What the gate adds is
# company. `make check` runs `make -j${CHECK_GATE_PARALLELISM} check-gate`
# (eleven targets at the resolved width, which is the CPU budget itself), so for
# the whole length of the suite's two workers -- two Go backends and two
# headless Chromiums -- the same CPU budget is also feeding golangci-lint,
# the frontend gate, the chart tests and the integration suite. The suite's
# auto-retrying waits and its 30s per-test clock then expire on a machine
# that is at its quota rather than merely small.
#
# This script rebuilds that arrangement locally: the suite (or an
# area-scoped subset of it) inside a container capped at the gate's CPU
# budget, concurrently with the gate targets that actually overlap it, from
# the same Dockerfile stage's environment (PARALLEL_GATE_CPU_LIMIT,
# ERUN_PLAYWRIGHT_WORKERS, PLAYWRIGHT_TEST_AREAS). A run costs a fraction of
# a gate cycle and its verdict is a spec list, not a build.
#
# WHAT IT DOES NOT DO
#
# It does not change a spec, a budget, an expectation, or the worker count's
# rule: no retries are added and nothing about the suite's own timeouts is
# touched (root AGENTS.md "No flaky tests" -- raise a wait, never lower one,
# and never paper over a nondeterministic failure with a retry). `--control`
# runs the identical suite with no competing load, which is the A/B baseline
# that says whether a red belongs to contention or to the change under test.
#
# USAGE
#
#   scripts/repro-gate-contention.sh                 # contended gate-like run
#   scripts/repro-gate-contention.sh --control       # same suite, no load
#   scripts/repro-gate-contention.sh --areas smoke,orchestrator,titlebar
#   scripts/repro-gate-contention.sh --load lint --repeat 3
#
# Every attempt writes its full output to <log-dir>/<tag>.log and prints a
# one-screen verdict: exit code, wall time, and the failing spec names.

set -eu

repo_root=$(cd "$(dirname "$0")/.." && pwd)

cpus=4
areas=""
load="lint"
jobs=""
workers_override=""
image=""
tag=""
log_dir=""
timeout_s=2400
control=0
build_app=0
forward=""

usage() {
	cat <<'EOF' >&2
Usage: scripts/repro-gate-contention.sh [options] [-- <playwright args>]

  --cpus N        CPU budget for the container (default 4, the gate's
                  DIND_CPU_LIMIT default and the budget the failures were
                  reported at). Sets the container's --cpus, the gate's own
                  PARALLEL_GATE_CPU_LIMIT, ERUN_PLAYWRIGHT_WORKERS (N/2, the
                  Dockerfile's own rule) and make's -j width.
  --areas SEL     PLAYWRIGHT_TEST_AREAS for the run: unset/"all" is the full
                  suite, "smoke" or "smoke,<area>,..." is the gate's
                  area-scoped selection (erun-ui/playwright/AGENTS.md).
  --load TARGETS  Gate targets to run concurrently with the suite, as space-
                  separated make targets (default "lint"). Ignored with
                  --control. "lint helm-chart-tests" is a heavier overlap;
                  "check-gate" is the whole gate.
  --workers N     Override the worker count (default: --cpus / 2, the
                  Dockerfile rule).
  --jobs N        make -j width for those targets (default: --cpus, matching
                  CHECK_GATE_PARALLELISM at that budget).
  --image REF     Container image (default: newest local erun-devops image).
  --timeout S     Hard wall-clock bound for one attempt (default 2400).
  --tag NAME      Log/run label (default: control or contended + areas).
  --log-dir DIR   Where attempt logs go (default: <repo>/.cache/repro-gate).
  --control       Run the suite with no competing gate targets.
  --build-app     Force a headless erun-app rebuild on the host first.
  --repeat N      Run the attempt N times in a row, reporting each.
  -h, --help      This text.
EOF
}

repeat=1
while [ $# -gt 0 ]; do
	case "$1" in
	--cpus)
		cpus=$2
		shift 2
		;;
	--areas)
		areas=$2
		shift 2
		;;
	--load)
		load=$2
		shift 2
		;;
	--jobs)
		jobs=$2
		shift 2
		;;
	--workers)
		workers_override=$2
		shift 2
		;;
	--image)
		image=$2
		shift 2
		;;
	--timeout)
		timeout_s=$2
		shift 2
		;;
	--tag)
		tag=$2
		shift 2
		;;
	--log-dir)
		log_dir=$2
		shift 2
		;;
	--repeat)
		repeat=$2
		shift 2
		;;
	--control)
		control=1
		shift
		;;
	--build-app)
		build_app=1
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	--)
		shift
		forward="$*"
		break
		;;
	*)
		usage
		exit 2
		;;
	esac
done

[ -n "$jobs" ] || jobs=$cpus
[ -n "$log_dir" ] || log_dir="$repo_root/.cache/repro-gate"
[ -n "$tag" ] || {
	if [ "$control" -eq 1 ]; then
		tag=control
	else
		tag=contended
	fi
	if [ -n "$areas" ] && [ "$areas" != "all" ]; then
		tag="$tag-$(printf '%s' "$areas" | tr ',' '-')"
	fi
}

# The gate's test stage is the only stage that carries this suite's toolchain
# (Playwright plus the Wails/webkit CGO deps), so the default image is a
# locally built erun-devops image rather than the release tag: a release tag
# without the test stage would have to download and install half of it.
if [ -z "$image" ]; then
	image=$(docker images --format '{{.Repository}}:{{.Tag}}' |
		grep '^ghcr.io/sophium/erun-devops:' | head -n1)
	if [ -z "$image" ]; then
		printf 'repro-gate-contention: no local erun-devops image; pass --image REF\n' >&2
		exit 1
	fi
fi

mkdir -p "$log_dir"

# The suite must run against a binary built from the tree under test. In the
# gate that is run.sh's own job before the specs start; here the build
# happens on the host (the toolchain and its caches are the same, and the
# binary is identical either way) so the attempt's container spends its whole
# budget on the suite, which is the part the gate gets wrong.
app_bin="$repo_root/erun-ui/bin/erun-app"
# Same source set run.sh's own staleness check uses (a rebase or a branch
# switch re-stamps every file without changing what the binary was built
# from, and a suite run against a binary from another tree attributes that
# mismatch to whatever spec fails first).
binary_is_stale() {
	[ -x "$app_bin" ] || return 0
	for src in "$repo_root/erun-ui" "$repo_root/erun-ui/headlessserver" \
		"$repo_root/erun-common" "$repo_root/erun-ui/frontend/src"; do
		[ -d "$src" ] || continue
		if [ -n "$(find "$src" -name '*.go' -newer "$app_bin" -print -quit 2>/dev/null)" ]; then
			return 0
		fi
	done
	return 1
}
if [ "$build_app" -eq 1 ] || binary_is_stale; then
	printf '>> building headless erun-app on the host\n' >&2
	(cd "$repo_root/erun-ui" && ./build.sh --skip-lint bin/erun-app) >"$log_dir/$tag-build.log" 2>&1 ||
		{
			printf 'repro-gate-contention: erun-app build failed; see %s\n' "$log_dir/$tag-build.log" >&2
			exit 1
		}
fi

# Both modes run the suite through run.sh (erun-ui/playwright/AGENTS.md's only
# supported entry point), with --skip-build because the binary above is the
# one this attempt means to test -- run.sh would otherwise rebuild it inside
# the container, and its staleness check is a host-mtime comparison the
# container's view of /src does not reproduce.
suite_cmd="cd /src/erun-ui/playwright && ./run.sh --skip-app-gates --skip-build"
if [ -n "$forward" ]; then
	suite_cmd="$suite_cmd -- $forward"
fi

run_attempt() {
	n=$1
	attempt_tag=$tag
	[ "$repeat" -gt 1 ] && attempt_tag="$tag-$n"
	log="$log_dir/$attempt_tag.log"
	started=$(date +%s)

	# The Dockerfile's own worker rule for this budget: half the CPU budget,
	# floored at one, capped at four. --workers pins it instead, which is the
	# only way to change the budget without also changing the suite's
	# concurrency -- the two effects the boot-wait failures could come from.
	if [ -z "$workers_override" ]; then
		workers=$((cpus / 2))
		[ "$workers" -ge 1 ] || workers=1
		[ "$workers" -le 4 ] || workers=4
	else
		workers=$workers_override
	fi

	# A wall-clock bound is only useful if it also takes the container with
	# it: killing `docker run`'s client leaves the run itself alive, holding
	# the CPU budget every later attempt needs. Hence a name to reap.
	name="repro-gate-$attempt_tag-$$"

	repro_load=""
	if [ "$control" -eq 0 ]; then
		repro_load=$load
	fi

	# The container mirrors the image's own test stage: the gate's CPU budget
	# as PARALLEL_GATE_CPU_LIMIT (so every width the Makefile sizes resolves
	# the way it does in a build), the same workers rule the Dockerfile
	# applies to that budget, and the same cache mounts, so a warm run
	# measures contention rather than downloads. HOME is left at the image's
	# default; the suite's config root is mktemp-ed under the container's own
	# /tmp, so no attempt can read another's state or the host's ~/.erun.
	# The load runs for exactly as long as the suite does. In the gate those
	# targets and the suite overlap for the suite's whole length, and the
	# gate's own long pole is golangci-lint -- measured at this budget, six
	# modules' worth does not finish inside 15 minutes, which is why `make`
	# waiting on both would report the load's runtime rather than the
	# suite's. Backgrounding it and letting the container's exit bound it
	# reproduces the overlap without inheriting a lint-length attempt.
	inner="load_log=/tmp/repro-gate-load.log
throttle_before=\$(awk '/nr_throttled|throttled_usec/ {printf \"%s \", \$2}' /sys/fs/cgroup/cpu.stat 2>/dev/null)
if [ -n \"\$REPRO_LOAD\" ]; then
	make -j$jobs \$REPRO_LOAD >\"\$load_log\" 2>&1 &
	load_pid=\$!
fi
suite_rc=0
$suite_cmd || suite_rc=\$?
throttle_after=\$(awk '/nr_throttled|throttled_usec/ {printf \"%s \", \$2}' /sys/fs/cgroup/cpu.stat 2>/dev/null)
echo \">> cgroup cpu.stat (nr_throttled throttled_usec) before: \$throttle_before after: \$throttle_after\"
if [ -n \"\$REPRO_LOAD\" ]; then
	if kill -0 \$load_pid 2>/dev/null; then
		echo \">> load [\$REPRO_LOAD] still running when the suite exited; tail:\"
		tail -n 5 \"\$load_log\"
	else
		wait \$load_pid 2>/dev/null || true
		echo \">> load [\$REPRO_LOAD] finished before the suite; tail:\"
		tail -n 5 \"\$load_log\"
	fi
fi
exit \$suite_rc"
	# ERUN_PLAYWRIGHT_ARTIFACTS_DIR points the suite's artifact root
	# (erun-ui/playwright/fixtures/artifacts.ts: Playwright's output dir, the
	# HTML report, and every frame a spec captures) at a container-local path.
	# This is the binding that makes the plugin worth having: the worktree is
	# bind-mounted at /src and the container runs as root, so anything the
	# suite writes at its default path comes back owned by uid 0 inside the
	# environment's own tree -- unremovable there, and fatal to every later run
	# in that environment, not just to this attempt. Container-local means the
	# artifacts leave with the container, which costs nothing here: this
	# script's surfaces are the verdict it prints and the full attempt log it
	# keeps, and it already forwards anything after `--` to playwright for a
	# run that wants a trace instead. The Dockerfile's own test stage declares
	# the same value, so this mirrors the stage rather than inventing a second
	# arrangement.
	#
	# --user root, not the image's default erun user: the Dockerfile's test
	# stage runs as root with HOME=/root (its own comment), and the gate's
	# cache env (GOLANGCI_LINT_CACHE, ~/.cache/ms-playwright) is keyed to
	# that. Running as the image's default user instead makes every
	# golangci-lint invocation die on an unwritable /root/.cache --
	# silently, since only its own log says so -- and the "contended"
	# attempt then measures an idle box. That failure mode looks exactly
	# like a clean reproduction, so it is worth being explicit about.
	# shellcheck disable=SC2086
	timeout "$timeout_s" docker run --rm --name "$name" \
		--user "${container_user:-root}" \
		--cpus="$cpus" \
		-v "$repo_root:/src" \
		-w /src \
		-v /home/erun/.cache/go-build:/root/.cache/go-build \
		-v /home/erun/go/pkg/mod:/go/pkg/mod \
		-v /home/erun/.cache/golangci-lint:/root/.cache/golangci-lint \
		-v /home/erun/.cache/ms-playwright:/ms-playwright \
		-e PLAYWRIGHT_BROWSERS_PATH=/ms-playwright \
		-e GOLANGCI_LINT_CACHE=/root/.cache/golangci-lint \
		-e PARALLEL_GATE_CPU_LIMIT="$cpus" \
		-e ERUN_PLAYWRIGHT_WORKERS="$workers" \
		-e PLAYWRIGHT_TEST_AREAS="$areas" \
		-e ERUN_PLAYWRIGHT_ARTIFACTS_DIR=/tmp/erun-playwright-artifacts \
		-e "REPRO_LOAD=$repro_load" \
		"$image" \
		sh -c "$inner" \
		>"$log" 2>&1
	rc=$?
	elapsed=$(($(date +%s) - started))
	# `timeout` fires at rc 124 and leaves the container behind; anything the
	# container still holds has to go before the next attempt starts.
	if [ "$rc" -eq 124 ]; then
		docker rm -f "$name" >/dev/null 2>&1 || true
	fi

	printf '\n== %s: exit %s, %ss ==\n' "$attempt_tag" "$rc" "$elapsed"
	if [ "$rc" -eq 124 ] || [ "$elapsed" -ge "$timeout_s" ]; then
		printf '   (hit the wall-clock bound of %ss; the verdict covers only what finished)\n' "$timeout_s"
	fi
	# Playwright's list reporter numbers every failure as `<n>) [chromium] ›
	# <file>:<line> › <suite> › <title>`, one per failure, so the failing spec
	# names come straight off the run's own output rather than a second pass.
	if grep -qE '^[[:space:]]+[0-9]+\) \[chromium\]' "$log"; then
		printf '   failing specs:\n'
		sed -nE 's/^[[:space:]]+[0-9]+\) \[chromium\] › ([^ ]+).*/\1/p' "$log" | sort -u | sed 's/^/     - /'
	else
		printf '   failing specs: none reported\n'
	fi
	grep -E '^[[:space:]]+(Slow test file|Total:|[0-9]+ (passed|failed|flaky))' "$log" | tail -5 | sed 's/^/   /'
	# A load that never ran reports the same spec list as one that saturated
	# the box, so its evidence is part of the verdict rather than a footnote.
	# The cgroup counters are the load's real evidence: they are numeric, they
	# are read inside the container, and they survive a live load -- `make`
	# replays a target's output when its batch finishes (parallel-gate.sh), so
	# the load's own log is 0 bytes for the whole length of a contended run and
	# reads identically to a load that never started.
	if [ "$control" -eq 0 ]; then
		cpu_stat=$(grep -m1 '^>> cgroup cpu\.stat' "$log" || true)
		if [ -n "$cpu_stat" ]; then
			printf '   %s\n' "$cpu_stat"
		fi
		printf '   load [%s]:\n' "$load"
		evidence=$(sed -n '/^>> load \[/,$p' "$log" | tail -n 8)
		printf '%s\n' "$evidence" | sed 's/^/     /'
		beyond_header=$(printf '%s\n' "$evidence" | grep -cv '^>> load \[' || true)
		if [ "$beyond_header" -eq 0 ]; then
			printf '     (no output yet: a target'"'"'s log is replayed when its batch ends)\n'
		fi
	fi
	printf '   log: %s\n' "$log"
	return "$rc"
}

overall=0
n=1
while [ "$n" -le "$repeat" ]; do
	run_attempt "$n" || overall=$?
	n=$((n + 1))
done
exit "$overall"
