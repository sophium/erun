#!/bin/sh

# Tests for .githooks/pre-commit's frontend path: the hook regenerates
# erun-ui/frontend's generated Wails bindings (wailsjs/) before it lints that
# workspace, and skips that lint -- loudly -- rather than running it against
# bindings it could not regenerate.
#
# The reproduction this pins: erun-ui/frontend imports wailsjs/, which is
# gitignored, so a checkout whose copy predates a newly added `App` method
# lints against a binding that does not resolve. @typescript-eslint reads the
# unresolvable call as `any` and reports no-unsafe-call/no-unsafe-return inside
# source the commit never touched, and the hook vetoes the commit on it --
# including the squash `erun exec gate-merge` commits through this hook, where
# a blocked gate with no gated HEAD reads as a defect in the branch under gate.
# The Makefile's test-frontend and the image test stage both regenerate ahead
# of their own frontend lints; this hook is the surface that did not.
#
# The real generator needs a wails toolchain and the real lint needs a yarn
# install, so both are stood in for by scripts fast enough to run in a gate:
# the stub generator publishes the binding the stub lint resolves, exactly as
# the real pair does, and the stub lint fails when that binding is missing or
# stale -- the same two states the report describes. The seam the defect lives
# on is the hook's ordering, and that ordering is what the cases below assert.
#
# Wired into `make fast-check`, the gate root AGENTS.md requires before every
# push (unlike its siblings agent-gate_test.sh, parallel-gate_test.sh and
# timed-step_test.sh, which are run directly). It needs only sh and git: it
# fixtures its own throwaway repositories rather than reading this one.

set -eu

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
hook="${repo_root}/.githooks/pre-commit"

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t pre-commit-hook-test)"
trap 'rm -rf "${work_root}"' EXIT INT TERM

failures=0

pass() {
	printf 'ok: %s\n' "$1"
}

fail() {
	printf 'FAIL: %s\n' "$1" >&2
	failures=$((failures + 1))
}

# new_fixture <name> <generator-exit-status> <bindings: stale|none>
#
# Prints the path of a throwaway repository holding a copy of the hook under
# test, both stand-ins, and both workspaces' node_modules/ directories -- their
# presence is what decides whether the hook lints a workspace at all.
#
# The generator stand-in publishes the binding (LoadRuntimeRunState, the method
# the report's worktree was missing) and exits with the status the case asked
# for; the yarn stand-in resolves that binding the way type-aware eslint does,
# failing on the unresolvable call when it is missing or stale. Both record
# every call in ${HOOK_LOG}, which is the ordering the cases assert on.
new_fixture() {
	fixture_name="$1"
	generator_status="$2"
	bindings="$3"
	dir="${work_root}/${fixture_name}"
	mkdir -p "${dir}/.githooks" "${dir}/erun-ui/frontend/node_modules" \
		"${dir}/erun-ui/playwright/node_modules" "${dir}/bin"
	git -C "${dir}" init -q .

	cp "${hook}" "${dir}/.githooks/pre-commit"
	printf '%s\n' "${generator_status}" > "${dir}/erun-ui/generator-status"

	if [ "${bindings}" = "stale" ]; then
		mkdir -p "${dir}/erun-ui/frontend/wailsjs/go/main"
		cat > "${dir}/erun-ui/frontend/wailsjs/go/main/App.d.ts" <<'EOF'
export function LoadRuntimeActivity(arg1: main.uiSelection): Promise<main.uiRuntimeActivity>;
EOF
	fi

	cat > "${dir}/erun-ui/generate-wailsjs.sh" <<'EOF'
#!/bin/sh
printf 'generate\n' >> "${HOOK_LOG}"
mkdir -p "$(dirname "$0")/frontend/wailsjs/go/main"
printf 'export function LoadRuntimeRunState(arg1: main.uiSelection): Promise<main.uiRuntimeRunState>;\n' \
	> "$(dirname "$0")/frontend/wailsjs/go/main/App.d.ts"
exit "$(cat "$(dirname "$0")/generator-status")"
EOF
	chmod +x "${dir}/erun-ui/generate-wailsjs.sh"

	# Only the workspace that imports the bindings resolves them, so the
	# marker -- not the yarn stand-in -- decides which workspace depends on
	# wailsjs/ being current.
	: > "${dir}/erun-ui/frontend/imports-wailsjs"
	cat > "${dir}/bin/yarn" <<'EOF'
#!/bin/sh
printf 'yarn %s\n' "$*" >> "${HOOK_LOG}"
if [ -f imports-wailsjs ] && ! grep -q 'LoadRuntimeRunState' wailsjs/go/main/App.d.ts 2>/dev/null; then
	printf '  106:9  error  Unsafe call of a type that could not be resolved  @typescript-eslint/no-unsafe-call\n' >&2
	exit 1
fi
EOF
	chmod +x "${dir}/bin/yarn"

	printf '%s\n' "${dir}"
}

# run_hook <fixture> <path>...
#
# Stages each path as new source and runs the hook from the fixture root, the
# way `git commit` would. Returns the hook's exit status; its stdout, stderr
# and the stand-ins' call log are left behind as stdout, stderr and hook.log.
run_hook() {
	run_dir="$1"
	shift
	for run_path in "$@"; do
		mkdir -p "${run_dir}/$(dirname "${run_path}")"
		printf 'export const staged = 1;\n' > "${run_dir}/${run_path}"
		git -C "${run_dir}" add "${run_path}"
	done
	: > "${run_dir}/hook.log"
	set +e
	(
		cd "${run_dir}"
		HOOK_LOG="${run_dir}/hook.log" PATH="${run_dir}/bin:${PATH}" \
			sh .githooks/pre-commit >"${run_dir}/stdout" 2>"${run_dir}/stderr"
	)
	run_status=$?
	set -e
	return "${run_status}"
}

hook_status() {
	if run_hook "$@"; then
		printf '0'
	else
		printf '%s' "$?"
	fi
}

# 1. The reported state: bindings present but stale, predating the App method
#    the frontend source calls. Before the fix the hook lints against them,
#    the unresolvable call is reported as an error, and the commit is vetoed.
dir="$(new_fixture stale-bindings 0 stale)"
status="$(hook_status "${dir}" erun-ui/frontend/src/app/api/environmentApi.ts)"
if [ "${status}" = "0" ]; then
	pass 'a stale binding no longer vetoes a frontend commit'
else
	fail "the hook aborted (exit ${status}) on a frontend change whose bindings were stale: $(cat "${dir}/stderr")"
fi
first="$(sed -n '1p' "${dir}/hook.log")"
second="$(sed -n '2p' "${dir}/hook.log")"
if [ "${first}" = "generate" ] && [ "${second#yarn }" != "${second}" ]; then
	pass 'the bindings are regenerated before the lint runs'
else
	fail "expected the bindings to be regenerated before the lint; the hook ran [$(tr '\n' ';' < "${dir}/hook.log")]"
fi

# 2. A fresh checkout, where the whole wailsjs/ tree is absent -- the same
#    failure, reached without ever having generated the bindings by hand.
dir="$(new_fixture absent-bindings 0 none)"
status="$(hook_status "${dir}" erun-ui/frontend/src/app/api/environmentApi.ts)"
if [ "${status}" = "0" ] && [ "$(sed -n '1p' "${dir}/hook.log")" = "generate" ]; then
	pass 'a checkout with no bindings at all is regenerated before its lint'
else
	fail "the hook did not regenerate absent bindings (exit ${status}): $(cat "${dir}/stderr")"
fi

# 3. When the bindings cannot be regenerated -- no wails toolchain, no Go, a
#    generator that fails -- the frontend lint is skipped with a note instead
#    of run against bindings that cannot be trusted. The commit is not vetoed
#    for it: the check gate regenerates the bindings for its own run either
#    way, and a gate-merge has nobody to hand-fix a blocked gate.
dir="$(new_fixture generator-fails 1 none)"
status="$(hook_status "${dir}" erun-ui/frontend/src/app/api/environmentApi.ts)"
if [ "${status}" = "0" ]; then
	pass 'a generator failure does not veto the commit'
else
	fail "the hook aborted (exit ${status}) because it could not regenerate the bindings: $(cat "${dir}/stderr")"
fi
if [ -z "$(grep '^yarn ' "${dir}/hook.log" || true)" ]; then
	pass 'the frontend lint is skipped rather than run against untrusted bindings'
else
	fail 'the hook linted the frontend against bindings it could not regenerate'
fi
if grep -q 'skipping its' "${dir}/stderr"; then
	pass 'the skip says so instead of passing silently'
else
	fail "the skip was silent: $(cat "${dir}/stderr")"
fi

# 4. Playwright does not read wailsjs/, so a Playwright-only change lints
#    without paying for a regeneration it cannot use.
dir="$(new_fixture playwright-only 0 none)"
status="$(hook_status "${dir}" erun-ui/playwright/tests/probe.spec.ts)"
if [ "${status}" = "0" ] && [ "$(cat "${dir}/hook.log")" = "yarn --silent lint" ]; then
	pass 'a Playwright-only change lints without regenerating bindings'
else
	fail "a Playwright-only change ran [$(tr '\n' ';' < "${dir}/hook.log")] (exit ${status})"
fi

# 5. A commit that touches none of the linted sources runs nothing at all --
#    the regeneration belongs to the frontend lint, not to every commit.
dir="$(new_fixture unrelated 0 none)"
status="$(hook_status "${dir}" erun-docs/sidebars.ts)"
if [ "${status}" = "0" ] && [ ! -s "${dir}/hook.log" ]; then
	pass 'a change outside the linted sources runs neither generator nor lint'
else
	fail "an unrelated change ran [$(tr '\n' ';' < "${dir}/hook.log")] (exit ${status})"
fi

if [ "${failures}" -ne 0 ]; then
	printf '%s case(s) failed\n' "${failures}" >&2
	exit 1
fi

printf 'pre-commit hook tests passed\n'
