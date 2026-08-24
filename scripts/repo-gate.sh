#!/usr/bin/env bash
#
# The whole-repo gate for erun#1290: every check a PR merge or a main commit
# must pass, run once, against whatever tree is currently checked out.
#
# This script does not know about pull requests or "the merge result" — it
# just checks the working tree it is run from. scripts/merge-gate.sh builds
# the merge result in a scratch worktree and calls this script against it;
# running this script directly against a checked-out main is the post-merge
# gate. Keeping the two gates as one script is deliberate: a check that only
# ran pre-merge and a check that only ran post-merge could drift apart, and
# drift is exactly what let #1282 and #1288 slip through in the first place.
#
# Every step below runs even if an earlier one fails, so one pass reports
# every red section instead of stopping at the first. The exit code is
# nonzero iff any section failed.
#
# Deliberately NOT run here: the full erun-ui Playwright spec suite (slow —
# minutes, not seconds — and this repo has no hosted CI to absorb that cost
# invisibly) and the erun-ui/playwright opt-in k3d e2e mode (needs a real
# cluster). See root AGENTS.md "CI gate" section for the reasoning and what
# runs instead (playwright's own `tsc --noEmit`, `lint`, `format:check`).
set -uo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "$0")" && pwd -P)"
ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd -P)"
cd "$ROOT"

# Section results, in run order, as "name:status" (status is PASS or FAIL).
RESULTS=()

# run <label> <cmd...> — runs a command, records PASS/FAIL, always continues.
run() {
	local label="$1"
	shift
	echo "=================================================================="
	echo ">> ${label}"
	echo "=================================================================="
	if "$@"; then
		RESULTS+=("${label}:PASS")
	else
		RESULTS+=("${label}:FAIL")
		echo "!! ${label} FAILED" >&2
	fi
}

YARN_BIN="${YARN_BIN:-yarn}"

# ---------------------------------------------------------------------------
# Yarn workspaces (erun-kit, erun-ui/frontend, erun-console).
#
# Trap #2: node_modules/erun-kit is a yarn workspace symlink created by a
# ROOT yarn install. A per-package `yarn install` in erun-ui/frontend or
# erun-console never creates it, and every `from 'erun-kit'` import then
# fails to resolve. Install once, at the workspace root, before touching any
# of the three packages.
# ---------------------------------------------------------------------------
run "root yarn install (workspace symlinks)" "$YARN_BIN" install --frozen-lockfile

yarn_workspace_gate() {
	local dir="$1"
	local label="$2"
	(
		cd "$ROOT/$dir" || exit 1
		script_exists() { node -e "process.exit(require('./package.json').scripts && require('./package.json').scripts['$1'] ? 0 : 1)"; }
		for s in typecheck lint format:check test build; do
			if script_exists "$s"; then
				echo "-- $label: yarn $s --"
				"$YARN_BIN" run "$s" || exit 1
			fi
		done
	)
}

# Trap #1: erun-ui/frontend/wailsjs/ is gitignored and build-generated.
# PRESENT IS NOT FRESH — a stale or cached copy produces phantom TS2307s and
# cascading lint errors that look exactly like real breaks (a real incident:
# "StartAISession: expected 4 arguments, but got 5" was chased as a genuine
# regression before it turned out to be stale bindings). Always regenerate,
# never reuse whatever happens to be on disk, and never cache this directory
# in a CI system that adopts this script.
regenerate_wailsjs() {
	rm -rf "$ROOT/erun-ui/frontend/wailsjs"
	local wails_bin
	wails_bin="$(go env GOPATH)/bin/wails"
	if [ ! -x "$wails_bin" ]; then
		local wails_version
		wails_version="$(cd "$ROOT/erun-ui" && go list -m -f '{{.Version}}' github.com/wailsapp/wails/v2)"
		GOBIN="$(go env GOPATH)/bin" go install "github.com/wailsapp/wails/v2/cmd/wails@${wails_version}"
	fi
	(cd "$ROOT/erun-ui" && "$wails_bin" generate module)
}
run "erun-ui: regenerate wailsjs bindings" regenerate_wailsjs

run "erun-kit gate" yarn_workspace_gate erun-kit "erun-kit"
run "erun-ui/frontend gate" yarn_workspace_gate erun-ui/frontend "erun-ui/frontend"
run "erun-console gate" yarn_workspace_gate erun-console "erun-console"

# ---------------------------------------------------------------------------
# erun-ui/playwright: its own yarn project, its own node_modules/tsconfig,
# NOT covered by the root workspace install above and NOT covered by any of
# the three workspace gates. This is the check that catches a spec calling a
# renamed label (#1288) or a page-object method a component rename dropped.
#
# Deliberately not running `playwright test` here (see file header) — only
# the package's own typecheck/lint/format, which need no built binary and no
# browser.
# ---------------------------------------------------------------------------
playwright_static_gate() {
	(
		cd "$ROOT/erun-ui/playwright" || exit 1
		"$YARN_BIN" install --frozen-lockfile || exit 1
		"$YARN_BIN" run typecheck || exit 1
		"$YARN_BIN" run lint || exit 1
		"$YARN_BIN" run format:check || exit 1
	)
}
run "erun-ui/playwright: tsc --noEmit, lint, format:check" playwright_static_gate

# ---------------------------------------------------------------------------
# Go modules: go test ./... and golangci-lint run ./... for each.
#
# Trap #4: erun-mcp's TestReleaseToolPreview needs `docker` on PATH. This
# script assumes it runs inside an erun-devops-based environment (this repo's
# own runtime image, or a developer/agent pod built from it) — every such
# environment ships docker, kubectl, helm, and terraform already (that is the
# whole point of erun's own build system replacing hosted CI, see root
# AGENTS.md "no hosted CI"). Docker is therefore expected on PATH and this
# test is expected to run for real, not skip. If this script is ever run
# somewhere without docker, that one test fails loudly rather than silently
# skipping — which is the correct behavior: a passing gate should mean the
# tool was actually exercised.
# ---------------------------------------------------------------------------
# golangci-lint for all six modules the issue names is exactly the root
# Makefile's existing LINT_MODULES set — reuse it rather than re-deriving the
# list here, so the two can never drift.
run "make lint (golangci-lint x6)" make -C "$ROOT" lint

GO_MODULES=(erun-cli erun-common erun-mcp erun-backend/erun-backend-api)

go_module_test_gate() {
	(cd "$ROOT/$1" && go test ./...)
}
for m in "${GO_MODULES[@]}"; do
	run "go test: $m" go_module_test_gate "$m"
done

# erun-ui's own go.work only unions itself with erun-common (it must not
# import erun-cli), and its Go tests need the bare-toolchain build only (no
# desktop/webkit tags) — reuse the root Makefile's existing recipe (the
# -count=1 there is load-bearing, see Makefile comment) instead of
# reimplementing it.
run "go test: erun-ui" make -C "$ROOT" test-erun-ui

# erun-integration: go test ./... plus the mandatory coverage-gated --dry-run
# suite (root AGENTS.md "Integration Test Gate (Mandatory)") in one step —
# reuse the root Makefile target rather than re-deriving the
# build/coverage-merge steps here.
run "go test: erun-integration (make integration-test, coverage-gated)" make -C "$ROOT" integration-test

# ---------------------------------------------------------------------------
# Terraform modules under erun-devops/terraform-erun.
# ---------------------------------------------------------------------------
run "terraform: fmt -check -recursive" terraform -chdir="$ROOT/erun-devops/terraform-erun" fmt -check -recursive

terraform_module_gate() {
	local dir="$1"
	(
		cd "$dir" || exit 1
		terraform init -backend=false -input=false || exit 1
		terraform validate || exit 1
		if [ -d tests ]; then
			terraform test || exit 1
		fi
	)
}
for d in "$ROOT"/erun-devops/terraform-erun/modules/*/; do
	name="$(basename "$d")"
	run "terraform module: $name" terraform_module_gate "$d"
done

# ---------------------------------------------------------------------------
# Chart test scripts. Shell tests run directly (they shell out to `helm`),
# never through `make check` — see erun-devops/AGENTS.md.
# ---------------------------------------------------------------------------
for t in "$ROOT"/erun-devops/k8s/*_test.sh; do
	name="$(basename "$t")"
	run "chart test: $name" "$t"
done

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo "=================================================================="
echo "repo-gate summary"
echo "=================================================================="
failed=0
for r in "${RESULTS[@]}"; do
	label="${r%%:*}"
	status="${r##*:}"
	printf '%-70s %s\n' "$label" "$status"
	[ "$status" = "FAIL" ] && failed=1
done

exit "$failed"
