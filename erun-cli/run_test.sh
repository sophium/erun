#!/bin/sh

# Tests for erun-cli/run.sh's build decisions.
#
# The wrapper exists to rebuild from a working checkout, but it used to decide
# what to build from the subcommand name alone, before it knew what the
# invocation was -- so `erun app --help` paid for the whole Wails/yarn/tsc/eslint
# desktop build to print a paragraph of static text, and every other `--help`
# paid for a compile. Help and completion are answered from the binary already
# on disk; everything else still rebuilds.
#
# Run directly (not wired into `make check`, same reasoning as
# scripts/agent-gate_test.sh): a stub `go` on PATH and a stub
# erun-ui/build.sh record which build the wrapper started, and the stub binary
# they materialize reports what the wrapper finally exec'd -- so this asserts
# the wrapper's own decisions with no compiler or desktop toolchain involved.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
wrapper="${script_dir}/run.sh"

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t erun-run-test)"
trap 'rm -rf "${work_root}"' EXIT INT TERM

# The wrapper honors this to move the binaries it builds out of the checkout; an
# ambient value would send every case's build somewhere this test does not read.
unset ERUN_DEV_BIN_DIR

fail() {
	printf 'FAIL: %s\n' "$1" >&2
	exit 1
}

expect_empty() {
	if [ -s "$1" ]; then
		fail "$2 (recorded: $(tr '\n' ';' <"$1"))"
	fi
}

expect_nonempty() {
	if [ ! -s "$1" ]; then
		fail "$2"
	fi
}

# new_case stages a throwaway checkout under ${work_root}/<name> holding the
# real run.sh, the recording stubs, and the per-case records:
#   go.log        - one line per `go build` the wrapper started
#   ui-build.log  - one line per erun-ui/build.sh the wrapper ran
#   dispatch-stub - the "binary" both a stub `go` and a pre-seeded bin/erun
#                   stand in with; it prints the argv the wrapper exec'd it with
case_dir=
new_case() {
	case_dir="${work_root}/$1"
	mkdir -p "${case_dir}/erun-cli/bin" "${case_dir}/erun-ui" "${case_dir}/stub-bin"
	cp "${wrapper}" "${case_dir}/erun-cli/run.sh"
	: >"${case_dir}/go.log"
	: >"${case_dir}/ui-build.log"

	cat >"${case_dir}/dispatch-stub" <<'STUB'
#!/bin/sh
printf 'dispatched: %s\n' "$*"
STUB
	chmod +x "${case_dir}/dispatch-stub"

	cat >"${case_dir}/erun-ui/build.sh" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >>"${case_dir}/ui-build.log"
exit 0
EOF
	chmod +x "${case_dir}/erun-ui/build.sh"

	# `go` records the invocation, then materializes the -o target so the
	# wrapper's own existence check sees the build it asked for.
	cat >"${case_dir}/stub-bin/go" <<EOF
#!/bin/sh
printf 'go %s\n' "\$*" >>"${case_dir}/go.log"
out=
prev=
for a in "\$@"; do
	if [ "\$prev" = "-o" ]; then
		out="\$a"
	fi
	prev="\$a"
done
if [ -n "\$out" ]; then
	cp "${case_dir}/dispatch-stub" "\$out"
	chmod +x "\$out"
fi
exit 0
EOF
	chmod +x "${case_dir}/stub-bin/go"
}

# prebuild_cli seeds the wrapper's default bin/erun with the dispatch stub, the
# state a developer's checkout is in after any earlier invocation.
prebuild_cli() {
	cp "${case_dir}/dispatch-stub" "${case_dir}/erun-cli/bin/erun"
	chmod +x "${case_dir}/erun-cli/bin/erun"
}

# run_wrapper runs the wrapper for this case with the stub toolchain, printing
# only its stdout; the wrapper's own rebuild progress goes to stderr.
run_wrapper() {
	(
		cd "${work_root}"
		PATH="${case_dir}/stub-bin:${PATH}" sh "${case_dir}/erun-cli/run.sh" "$@"
	) 2>"${case_dir}/wrapper-stderr.txt"
}

# `erun app --help` must be answered by the binary on disk: no CLI compile, and
# above all no desktop build for a request that can never launch the desktop.
new_case app_help_uses_existing_binary
prebuild_cli
out=$(run_wrapper app --help)
[ "${out}" = "dispatched: app --help" ] || fail "app --help dispatched ${out}, want the existing binary run with app --help"
expect_empty "${case_dir}/go.log" "app --help rebuilt the CLI"
expect_empty "${case_dir}/ui-build.log" "app --help ran the desktop build"

# With nothing built yet there is nothing to answer with, so the CLI is built --
# but the desktop still is not, because help cannot reach it.
new_case app_help_without_binary
out=$(run_wrapper app --help)
[ "${out}" = "dispatched: app --help" ] || fail "app --help (no binary) dispatched ${out}"
expect_nonempty "${case_dir}/go.log" "app --help with no binary never built the CLI"
expect_empty "${case_dir}/ui-build.log" "app --help without a binary ran the desktop build"

# The help command and the completion protocol ask the same static question.
new_case help_command
prebuild_cli
out=$(run_wrapper help app)
[ "${out}" = "dispatched: help app" ] || fail "help app dispatched ${out}"
expect_empty "${case_dir}/go.log" "help app rebuilt the CLI"

new_case completion
prebuild_cli
out=$(run_wrapper __complete app "")
[ "${out}" = "dispatched: __complete app " ] || fail "__complete dispatched ${out}"
expect_empty "${case_dir}/go.log" "__complete rebuilt the CLI"

# The same request on any other subcommand is not special-cased either.
new_case other_help
prebuild_cli
out=$(run_wrapper doctor --help)
[ "${out}" = "dispatched: doctor --help" ] || fail "doctor --help dispatched ${out}"
expect_empty "${case_dir}/go.log" "doctor --help rebuilt the CLI"

# Everything else keeps rebuilding, and a real `app` launch keeps rebuilding the
# desktop beside it -- the guard that the fast path did not become the default.
new_case app_launch_still_builds_both
out=$(run_wrapper app)
[ "${out}" = "dispatched: app" ] || fail "app dispatched ${out}"
expect_nonempty "${case_dir}/go.log" "app no longer rebuilds the CLI"
expect_nonempty "${case_dir}/ui-build.log" "app no longer rebuilds the desktop"

new_case work_command_still_rebuilds
prebuild_cli
out=$(run_wrapper version)
[ "${out}" = "dispatched: version" ] || fail "version dispatched ${out}"
expect_nonempty "${case_dir}/go.log" "version no longer rebuilds the CLI"

printf 'ok\n'
