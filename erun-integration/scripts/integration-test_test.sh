#!/bin/sh

# Tests integration-test.sh's own detection of a lost coverage emit: when a
# coverage meta-data emit fails (concurrent invocations racing a
# write-then-rename into a shared GOCOVERDIR), the losing invocation prints
# the failure to its own output without failing the scenario that was
# running at the time -- so the merged total downstream would otherwise
# silently under-report coverage instead of the gate ever seeing why. This
# exercises that detection against a stubbed `go`, without running the real
# suite or building the real binary.
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
stub_go() {
	bin_dir="$1"
	mkdir -p "$bin_dir"
	cat >"${bin_dir}/go" <<'EOF'
#!/bin/sh
case "$1" in
test)
	if [ "${STUB_EMIT_FAILED:-0}" = "1" ]; then
		echo "some_test_test.go output"
		echo "coverage meta-data emit failed: rename /tmp/x/covmeta.abc /tmp/x/covmeta.abc.tmp2: no such file or directory"
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

echo "PASS"
