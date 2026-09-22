#!/bin/sh

# Tests integration-test.sh's own control flow against a stubbed `go`, without
# running the real suite or building the real binary:
#
#   - its detection of a lost coverage emit: when a coverage meta-data emit
#     fails (concurrent invocations racing a write-then-rename into a shared
#     GOCOVERDIR), the losing invocation prints the failure to its own output
#     without failing the scenario that was running at the time -- so the
#     merged total downstream would otherwise silently under-report coverage
#     instead of the gate ever seeing why.
#   - its threshold comparison and the message that reports it, including the
#     default the script derives from `coverage_measured` minus
#     `coverage_margin`. The margin is there so cross-host variance does not
#     fail the gate, so the cases below pin both that it exists at the default
#     and that a total below the threshold is reported with the measured
#     value, the threshold, and the shortfall between them.
#
# Run directly (not wired into `make check`, same reasoning as
# scripts/agent-gate_test.sh): a stub `go` on PATH stands in for the real
# toolchain so this asserts the script's own control flow, not real coverage
# instrumentation.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
gate="${script_dir}/integration-test.sh"

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t integration-test-test)"
trap 'rm -rf "${work_root}"' EXIT INT TERM

fail() {
	echo "FAIL: $1" >&2
	exit 1
}

# stub_go writes a fake `go` on PATH that answers `go test`, `go tool covdata
# textfmt`, and `go tool cover -func` without touching the real toolchain or
# building the real binary. STUB_EMIT_FAILED=1 makes the `go test` call print
# the same "coverage meta-data emit failed" line a real race produces;
# STUB_TOTAL_PCT (default 80.0) controls what `go tool cover -func` reports as
# the total.
#
# The gate reads coverage per process: each instrumented subprocess writes into
# its own private subdirectory of GOCOVERDIR, and the gate refuses to report a
# total when that directory is missing or empty. The stub therefore has to
# stand in for that shape too -- a clean run leaves one populated per-process
# directory, and the emit-failure run leaves the directory it created empty,
# exactly as a losing invocation that never landed its meta-data does.
stub_go() {
	bin_dir="$1"
	mkdir -p "$bin_dir"
	cat >"${bin_dir}/go" <<'EOF'
#!/bin/sh
case "$1" in
test)
	if [ -n "${GOCOVERDIR:-}" ]; then
		mkdir -p "${GOCOVERDIR}/proc.$$"
	fi
	if [ "${STUB_EMIT_FAILED:-0}" = "1" ]; then
		echo "some_test_test.go output"
		echo "coverage meta-data emit failed: rename /tmp/x/covmeta.abc /tmp/x/covmeta.abc.tmp2: no such file or directory"
	else
		printf 'mode: set\n' >"${GOCOVERDIR}/proc.$$/covcounters.stub"
	fi
	echo "ok  	github.com/sophium/erun/erun-integration	1.234s"
	exit 0
	;;
tool)
	case "$2" in
	covdata)
		out=""
		for a in "$@"; do
			case "$a" in
			-o=*) out="${a#-o=}" ;;
			esac
		done
		printf 'mode: set\n' >"$out"
		exit 0
		;;
	cover)
		echo "github.com/sophium/erun/foo.go:1:  Foo   100.0%"
		echo "total:                       (statements)      ${STUB_TOTAL_PCT:-80.0}%"
		exit 0
		;;
	esac
	exit 1
	;;
esac
exit 1
EOF
	chmod +x "${bin_dir}/go"
}

# Case 1: an emit failure during the run must fail the gate loudly, before
# ever computing (and reporting) a total.
case1_dir="${work_root}/case1"
mkdir -p "${case1_dir}/bin"
stub_go "${case1_dir}/bin"
set +e
(cd "${case1_dir}" && PATH="${case1_dir}/bin:$PATH" STUB_EMIT_FAILED=1 "$gate") >"${case1_dir}/out.txt" 2>&1
status=$?
set -e
[ "$status" -ne 0 ] || fail "case1: expected non-zero exit when a coverage emit failed, got 0: $(cat "${case1_dir}/out.txt")"
grep -q "a coverage meta-data emit failed during the run" "${case1_dir}/out.txt" ||
	fail "case1: expected the loud-failure message in output; got: $(cat "${case1_dir}/out.txt")"
if grep -q "ok  coverage" "${case1_dir}/out.txt"; then
	fail "case1: must not report a coverage total once an emit failed: $(cat "${case1_dir}/out.txt")"
fi

# Case 2: a clean run (no emit failure) still reports the merged total
# normally -- the new check must not break the golden path.
case2_dir="${work_root}/case2"
mkdir -p "${case2_dir}/bin"
stub_go "${case2_dir}/bin"
set +e
(cd "${case2_dir}" && PATH="${case2_dir}/bin:$PATH" STUB_TOTAL_PCT=80.0 COVERAGE_THRESHOLD=75.1 "$gate") >"${case2_dir}/out.txt" 2>&1
status=$?
set -e
[ "$status" -eq 0 ] || fail "case2: expected zero exit for a clean run, got $status: $(cat "${case2_dir}/out.txt")"
grep -q "ok  coverage 80.0% (>= 75.1%)" "${case2_dir}/out.txt" ||
	fail "case2: expected the normal coverage report; got: $(cat "${case2_dir}/out.txt")"

# Case 3: a total below the threshold must fail with a message naming the
# measured value, the threshold, and the gap between them. These are the
# numbers a real branch produced -- measured 74.7 against the then-pinned
# 75.1 -- and the reader of a failed gate should not have to re-derive
# whether the run was close.
case3_dir="${work_root}/case3"
mkdir -p "${case3_dir}/bin"
stub_go "${case3_dir}/bin"
set +e
(cd "${case3_dir}" && PATH="${case3_dir}/bin:$PATH" STUB_TOTAL_PCT=74.7 COVERAGE_THRESHOLD=75.1 "$gate") >"${case3_dir}/out.txt" 2>&1
status=$?
set -e
[ "$status" -ne 0 ] || fail "case3: expected a non-zero exit below the threshold, got 0: $(cat "${case3_dir}/out.txt")"
grep -qF "!! coverage 74.7% is below threshold 75.1% (short by 0.4)" "${case3_dir}/out.txt" ||
	fail "case3: expected the failure to name the measured total, the threshold, and the shortfall; got: $(cat "${case3_dir}/out.txt")"
if grep -q "ok  coverage" "${case3_dir}/out.txt"; then
	fail "case3: must not also report a passing coverage line: $(cat "${case3_dir}/out.txt")"
fi

# Case 4: the comparison is inclusive -- a total exactly on the threshold
# passes. It is the boundary a miss is measured from, so an off-by-one turn
# there would move every verdict without changing anything else.
case4_dir="${work_root}/case4"
mkdir -p "${case4_dir}/bin"
stub_go "${case4_dir}/bin"
set +e
(cd "${case4_dir}" && PATH="${case4_dir}/bin:$PATH" STUB_TOTAL_PCT=74.8 COVERAGE_THRESHOLD=74.8 "$gate") >"${case4_dir}/out.txt" 2>&1
status=$?
set -e
[ "$status" -eq 0 ] || fail "case4: expected a zero exit exactly on the threshold, got $status: $(cat "${case4_dir}/out.txt")"
grep -q "ok  coverage 74.8% (>= 74.8%)" "${case4_dir}/out.txt" ||
	fail "case4: expected the boundary to report a pass; got: $(cat "${case4_dir}/out.txt")"

# Case 5: with no COVERAGE_THRESHOLD override, the default really is
# `coverage_measured` minus `coverage_margin` -- and really is below the
# measured total. The two are read back out of the gate script rather than
# repeated here, so a re-base that moved the default onto the measured value
# fails this case instead of quietly leaving the gate with no headroom.
measured="$(sed -n 's/^coverage_measured=\([0-9.]*\)$/\1/p' "$gate")"
margin="$(sed -n 's/^coverage_margin=\([0-9.]*\)$/\1/p' "$gate")"
[ -n "$measured" ] && [ -n "$margin" ] ||
	fail "the gate no longer names coverage_measured and coverage_margin"
default="$(awk -v m="$measured" -v k="$margin" 'BEGIN { printf "%.1f", m - k }')"
awk -v k="$margin" 'BEGIN { exit !(k > 0) }' ||
	fail "the gate's coverage_margin is not positive: ${margin}"
awk -v m="$measured" -v d="$default" 'BEGIN { exit !(d < m) }' ||
	fail "the gate's default threshold ${default} is not below the measured ${measured}"
case5_dir="${work_root}/case5"
mkdir -p "${case5_dir}/bin"
stub_go "${case5_dir}/bin"
set +e
(cd "${case5_dir}" && PATH="${case5_dir}/bin:$PATH" STUB_TOTAL_PCT="$default" "$gate") >"${case5_dir}/out.txt" 2>&1
status=$?
set -e
[ "$status" -eq 0 ] || fail "case5: expected the derived default to pass at ${default}%, got $status: $(cat "${case5_dir}/out.txt")"
grep -q "ok  coverage ${default}% (>= ${default}%)" "${case5_dir}/out.txt" ||
	fail "case5: expected the default threshold to print as ${default}%; got: $(cat "${case5_dir}/out.txt")"

echo "PASS"
