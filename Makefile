.PHONY: integration-test integration-test-gate lint test-erun-ui test-erun-backend-api test-erun-mcp test-erun-dns01-webhook test-frontend test-playwright test-erun-ui-windows-build helm-chart-tests test-postgres-restart test-retention test-retention-grants test-schema-drift test-console-nginx check check-gate fast-check

# Go modules linted by the in-build gate: erun-common, erun-cli, erun-mcp,
# erun-integration, erun-backend/erun-backend-api, and erun-ui. Every entry
# must also be COPYd into the erun-devops image test stage's context
# (erun-devops/docker/erun-devops/Dockerfile) — a module the stage does not COPY
# makes `cd $m` fail and breaks every build (this already happened once with
# erun-backend-api), so add the COPY in the same change as the entry.
#
# erun-ui was excluded on the assumption that its Wails/CGO/webkit toolchain
# cannot build in the test stage's bare `golang` image. Checked empirically
# rather than assumed: Wails' native webview bindings sit behind the
# `desktop,production,webkit2_41` build tags erun-ui/build.sh passes only when
# building the real app binary, and neither `go build ./...` nor
# `golangci-lint run ./...` request those tags, so both run clean with no
# CGO/webkit headers installed. The result: 9 erun-ui tests sat red on `main`
# across multiple releases (a real-`ConfigStore` regression from the
# environmentIsConfigured guard, invisible to every gate because nothing here
# ran them) and were fixed only incidentally by someone who happened to run
# them for an unrelated reason. `go test ./...` is gated the same way lint is,
# via test-erun-ui below rather than folded into `integration-test`: erun-ui's
# own go.work only unions itself with erun-common (deliberately — it must not
# import erun-cli), so its tests are never reachable from erun-integration's
# `go test ./...`, and a contributor's habitual "run the tests" from erun-cli
# or erun-common misses erun-ui for the same reason. The test stage now
# carries node/yarn (added for test-frontend below), which also runs
# erun-ui/frontend's own frontend gate (`yarn typecheck && yarn lint &&
# yarn format:check && yarn build && yarn test`; see test-frontend below) —
# it stayed out of `make check` for a while on the theory that
# `erun-ui/build.sh` already covers it, but that meant `main` could sit red in
# that suite with a truthfully-green `make check` beside it. `erun-ui/build.sh`
# still runs the same gate too, redundantly, as the fast local signal for
# anyone iterating on the desktop build directly. The
# Playwright suite (needs a built app) stays out of the per-commit gate
# entirely either way, run on its own schedule instead.
#
# erun-backend-db has no Go module at all (Atlas migrations + SQL only), so
# there is nothing here for golangci-lint to run against.
# An outer bound on a single module's lint, passed explicitly because the
# default is short enough that the largest module (erun-backend-api, which
# carries the AWS SDK) exceeds it on a modest build host -- and golangci-lint
# reports that as a failure *after* printing "0 issues", so a clean analysis
# reads as a red gate and a release aborts for no finding at all. Modules with
# their own .golangci.yml set a shorter run.timeout; this is the ceiling, not a
# replacement for it.
#
# 15m was calibrated against an uncapped build container, which took 22 of a
# 24-core node before the build-container CPU cap (#2255/#2257) started
# holding it to its declared cpu= (commonly 4). Once every module actually
# got only that many cores, the fixed 15m stopped fitting: every module
# reported "0 issues" and then hit the timeout anyway, turning an
# environmental CPU shortage into a false-red gate (erun#2266). Scale the
# timeout inversely with the same resolved CPU quota LINT_PARALLELISM already
# reads below (scripts/parallel-gate.sh's cpu-quota mode, which honors the
# erun-devops Dockerfile's PARALLEL_GATE_CPU_LIMIT=$DIND_CPU_LIMIT override
# the same way LINT_PARALLELISM's width calculation does), floored at the
# original 15m so an environment at or above the 22-core reference never gets
# less time than before. At the reference DIND_CPU_LIMIT default of 4 this
# resolves to 82m, comfortably past the ~24.5m (1468s) a starved run was
# observed to take in erun#2266 before failing. LINT_TIMEOUT's `?=` keeps the
# existing manual override: an explicit `LINT_TIMEOUT=<duration> make check`
# (or an env var of the same name) still wins over this computed default.
LINT_TIMEOUT_REFERENCE_CPU := 22
LINT_TIMEOUT_BASE_MINUTES := 15
LINT_TIMEOUT ?= $(shell cpu=$$(./scripts/parallel-gate.sh cpu-quota); \
	m=$$(( $(LINT_TIMEOUT_BASE_MINUTES) * $(LINT_TIMEOUT_REFERENCE_CPU) / cpu )); \
	[ "$$m" -ge $(LINT_TIMEOUT_BASE_MINUTES) ] || m=$(LINT_TIMEOUT_BASE_MINUTES); \
	echo "$${m}m")

LINT_MODULES := erun-common erun-cli erun-mcp erun-integration erun-backend/erun-backend-api erun-ui

# Bound on how many golangci-lint invocations run at once. Each invocation is
# itself internally parallel (cgroup-aware GOMAXPROCS), so running every
# LINT_MODULES entry at once already oversubscribes the machine -- measured on
# a 12-core pod (warm cache, one file touched per module to force real
# analysis rather than a full cache hit) to still win on wall-clock, because a
# single module's own analysis has serial phases that leave cores idle: p1
# (serial) 13.5s, p2 10.3s, p3 9.3s, p4 9.6s, p6 (all modules at once) 8.7s
# wall, peak memory climbing from 2.5GiB at p1 to 4.1GiB at p6 (see erun#1690).
# Sized by scripts/parallel-gate.sh's `width` mode off the environment's real
# CPU quota (cgroup cpu.max, not the `nproc` affinity mask -- see #1702) and a
# per-job memory cost, not `nproc` alone: a flat width sized for a 12-core pod
# would oversubscribe a smaller one, and #1702 found this recipe was the one
# gate width in the repo not accounting for the memory it demonstrably scales
# with. Capped at the module count: more workers than modules cannot help.
# LINT_JOB_MEMORY_MIB is the measured p6 peak (4.1GiB) averaged across its 6
# concurrent processes (~700MiB/job) -- conservative, since golangci-lint's
# own fixed overhead (most of the 2.5GiB seen at p1) doesn't actually scale
# per added job, but there is no measured marginal-cost figure to use instead.
LINT_JOB_MEMORY_MIB := 700
LINT_PARALLELISM ?= $(shell ./scripts/parallel-gate.sh width $(words $(LINT_MODULES)) $(LINT_JOB_MEMORY_MIB))

# Run golangci-lint across the gated modules concurrently (bounded by
# LINT_PARALLELISM), each against its own .golangci.yml (erun-integration has
# none, so it uses the default linters). Every module's combined stdout/stderr
# is buffered by scripts/parallel-gate.sh and emitted atomically under its
# ">> golangci-lint <module>" marker, in LINT_MODULES order, so concurrent
# runs never shred each other's diagnostics. Runs every module even when an
# earlier one has findings, then fails once at the end (with every failing
# module named on the "lint failed in:" line), so one red module never hides
# the rest -- reporting a subset of findings as if it were the whole answer is
# worse than reporting nothing. golangci-lint must be on PATH (the image test
# stage installs it; locally, install the version pinned in the repo-root
# GOLANGCI_LINT_VERSION file), and it must actually be that pinned version: a
# newer install's vendored analyzers can flag things the pinned one does not
# (and vice versa), so `make lint` and the image build would otherwise
# silently disagree. That version check runs once, up front, outside the
# fan-out, so a missing/mismatched tool fails the whole target before any
# golangci-lint process starts. `--allow-parallel-runners` is required here:
# golangci-lint acquires a file lock around its (shared, ~/.cache/golangci-lint)
# result cache by default and refuses a second concurrent instance ("parallel
# golangci-lint is running"); the flag opts into golangci-lint's own supported
# concurrent-runner mode, which is safe against the shared cache because cache
# entries are keyed by file content hash, not writer identity.
lint:
	@pin=$$(tr -d '\n' < GOLANGCI_LINT_VERSION); \
	pin_num=$${pin#v}; \
	installed=$$(golangci-lint --version 2>&1); \
	case "$$installed" in \
		*"version $$pin_num "*) ;; \
		*) echo "error: golangci-lint version mismatch: pinned $$pin, found: $$installed" >&2; \
		   echo "install the pinned version: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$$pin" >&2; \
		   exit 1 ;; \
	esac
	@for m in $(LINT_MODULES); do \
		printf '%s\t%s\t%s\n' "$$m" "golangci-lint $$m" "cd $$m && golangci-lint run --allow-parallel-runners --timeout $(LINT_TIMEOUT) ./..."; \
	done | ./scripts/parallel-gate.sh $(LINT_PARALLELISM) lint

# erun-ui's own Go tests. See the LINT_MODULES comment above for why this is
# a separate step rather than folded into integration-test or a contributor's
# ordinary `go test ./...`.
#
# -count=1 is load-bearing, not belt-and-braces. The build-stamp guard here
# reads build.sh, build.ps1 and the package-manager formulae with os.ReadFile at
# run time, and Go's test cache keys on compiled inputs only -- it does not
# track a file a test opens itself. So editing one of those scripts leaves a
# recorded PASS that is no longer true, and the gate reports `(cached)` while
# the thing it guards is broken. That is exactly how a release-breaking
# regression reached main once.
#
# -race is load-bearing too: this module's terminal/orchestrator session
# lifecycle spawns goroutines that share managedTerminal/App state with their
# spawner, and a plain `go test ./...` cannot see a data race even when one is
# live on every run -- five shipped here undetected until someone ran -race by
# hand as extra diligence. Measured locally: ~15s -> ~18s (roughly +15-20%
# wall time) for this module's suite; pay it here rather than let this class
# of bug go dark again.
test-erun-ui:
	@echo ">> go test erun-ui"
	@(cd erun-ui && go test -race -count=1 ./...)

# erun-backend-api's own Go tests. LINT_MODULES above already gives this
# module golangci-lint, but nothing ran `go test ./...` for it: its Dockerfile
# carries no test stage (unlike erun-devops, which runs `make check` itself
# in-build), so this module's tests were otherwise only a manual per-module
# instruction. A release-time -ldflags target silently broken by a Go
# package-main linker quirk shipped a wrong value to production undetected
# for exactly that reason -- the regression test written for it would never
# have run automatically without this. Opt-in ERUN_E2E_* suites skip cleanly
# with no database configured, so this stays fast with no external
# dependency.
#
# -count=1 is load-bearing, not belt-and-braces (the same reasoning as
# test-erun-ui's own -count=1 above): this module's Dockerfile RUN mounts a
# persistent BuildKit cache over /root/.cache/go-build (see the erun-devops
# Dockerfile), so Go's test cache survives across separate `erun build`
# invocations of different commits, not just within one. Three tests here
# read something at runtime that Go's cache cannot see as an input --
# tenant_scope_test.go's TestContextOnlyRepositoryMethodsAreClassified
# os.ReadDir-and-parses its own package directory, noexec_test.go's
# TestBackendRunsNoExternalBinaries filepath.WalkDir-and-parses this whole
# module's source tree (TestBannedCommonFuncsStillExist also filepath.Globs
# erun-common/*.go, outside this module entirely), and
# buildinfo_ldflags_test.go's TestDockerfileLdflagsActuallyStampsTheBinary
# os.ReadFiles the erun-devops Dockerfile -- so editing any of those without
# touching this module's own source would replay a stale cached "ok" and miss
# the regression, exactly the failure mode -X's own history bullet above
# describes.
test-erun-backend-api:
	@echo ">> go test erun-backend-api"
	@(cd erun-backend/erun-backend-api && go test -count=1 ./...)

# erun-mcp's own Go tests. LINT_MODULES above already gives this module
# golangci-lint, but nothing ran `go test ./...` for it: erun-mcp is unioned
# into erun-cli/go.work and erun-integration/go.work, but a `use` directive in
# a go.work only resolves local module dependencies for the build — it does
# not make a sibling module's packages match another module's own `./...`, so
# neither erun-cli's nor erun-integration's `go test ./...` ever runs a single
# erun-mcp test. 282 test cases (including subtests) sat green on every
# contributor's own machine and reachable by nobody's gate.
#
# -count=1 is load-bearing, not belt-and-braces, same reasoning as
# test-erun-backend-api's own -count=1 above: mcp_overview_doc_test.go's
# TestMCPOverviewDocumentsEveryTool os.ReadFiles
# erun-docs/docs/mcp/overview.md at run time -- a file in a different
# top-level module this test has no source dependency on -- so editing that
# doc without touching erun-mcp's own source would replay a stale cached "ok"
# under this module's own persistent BuildKit go-build cache mount and miss a
# drifted tool index.
test-erun-mcp:
	@echo ">> go test erun-mcp"
	@(cd erun-mcp && go test -count=1 ./...)

# erun-devops/dns01-webhook's own Go tests. This module has no entry in
# LINT_MODULES and no test stage of its own -- its Dockerfile only builds the
# binary (see erun-devops/AGENTS.md's Build Workflow section) -- so its real
# regression coverage (including a deleted-token-secret cleanup fix) sat
# reachable only by a contributor running `go test` from the module by hand.
test-erun-dns01-webhook:
	@echo ">> go test erun-devops/dns01-webhook"
	@(cd erun-devops/dns01-webhook && go test ./...)

# All three Yarn-workspace members: the shared frontend kit (erun-kit), the
# desktop frontend (erun-ui/frontend), and the hosted console (erun-console).
# Each runs its own full gate here now. Closes two gaps: the console's own
# gates (`erun-console/AGENTS.md`) previously ran "by hand or not at all", so
# the module drifted from the desktop's design system by default; and
# erun-kit's and erun-ui/frontend's own test suites sat red on `main` while
# `make check` reported green, because this target ran their typecheck/lint/
# format/build but not their `yarn test`, and for erun-ui/frontend didn't run
# its gate here at all (only `erun-ui/build.sh` did). erun-ui/build.sh still
# runs the same gate too, redundantly, as a fast local signal for anyone
# iterating on the desktop build directly.
#
# `yarn shadcn:check` (erun-kit) is deliberately excluded here: it fetches
# component definitions from ui.shadcn.com on every invocation, a third-party
# dependency `yarn install`'s package-registry fetch does not already impose
# on this gate. Run it locally before a PR that touches a shadcn primitive
# (see erun-kit/AGENTS.md); it is not re-verified on every image build.
#
# The issue-reference gate runs here rather than as a per-package ESLint
# rule: scripts/check-issue-references.mjs is the TypeScript-side twin of
# erun-integration/issue_reference_test.go (see that file's header), and one
# script scanning all three source roots in a single pass gives it the same
# repo-wide shrink-only baseline the Go gate uses, which a rule duplicated
# across three separate flat configs could not do on its own. It needs only
# the `typescript` package this step's own `yarn install` already resolves,
# not each package's full type-aware lint setup.
#
# erun-ui/frontend imports generated Wails bindings (wailsjs/) that are
# gitignored/dockerignored like any other generated artifact (dist,
# node_modules), so they are absent both from a fresh checkout and from the
# erun-devops image test stage's build context -- erun-ui/generate-wailsjs.sh
# (the same generator erun-ui/build.sh calls) regenerates them here before
# erun-ui/frontend's own gate runs. Skipping this step is exactly the #1857
# failure mode: a check that passes in a checkout with wailsjs/ already
# generated by hand and fails in the image build that starts from a clean
# context. Generation moved ahead of the three workspaces' own gates (it used
# to sit between erun-kit's and erun-ui/frontend's) because the three
# workspace gates below now run concurrently and wailsjs must exist before
# any of them starts.
#
# The three workspaces' own five-step gates (typecheck/lint/format/build/
# test) are independent of each other -- erun-kit's package.json points
# "main"/"types" at its own ./src/index.ts, so erun-ui/frontend and
# erun-console resolve it as workspace source, never a prebuilt erun-kit
# `dist`, and none of the three write into a path another one reads -- so
# they run concurrently via the same scripts/parallel-gate.sh idiom `lint`
# and `helm-chart-tests` already use, bounded by FRONTEND_GATE_PARALLELISM.
# Measured on a 24-core/27GiB pod: the three gates run serially in ~230s and
# concurrently in ~180s (real timed run, not estimated); FRONTEND_GATE_JOB_MEMORY_MIB
# is the measured net memory the three concurrent processes added over an
# idle baseline (~1.9GiB), divided across the 3 jobs and rounded up -- same
# "measure, don't fabricate a slope" reasoning HELM_CHART_TEST_JOB_MEMORY_MIB's
# comment gives.
FRONTEND_GATE_JOB_MEMORY_MIB := 650
FRONTEND_GATE_PARALLELISM ?= $(shell ./scripts/parallel-gate.sh width 3 $(FRONTEND_GATE_JOB_MEMORY_MIB))

# eslint/prettier's own --cache, one shared root so the erun-devops image test
# stage can mount it with a single BuildKit cache mount
# (erun-devops/docker/erun-devops/Dockerfile) covering all three workspaces.
# $(CURDIR) is the repo root whether this runs locally or inside that stage
# (WORKDIR /src there), so no path needs threading in from the Dockerfile.
# --cache-strategy content (not the metadata/mtime default) is load-bearing,
# not a style choice: every COPY in that Dockerfile stamps a fresh mtime on
# every file on every build, which makes the metadata strategy a permanent
# cache miss under Docker -- verified empirically (eslint: 20s cold either
# way, but 2.5s warm with content vs 20s "warm" with metadata after
# simulating a COPY's mtime reset). Both tools key their cache on file
# content plus their own config/version, so a real source or config change
# still re-lints/re-formats that file; this only skips files nothing about.
FRONTEND_LINT_CACHE_DIR := $(CURDIR)/.cache/frontend-lint

test-frontend:
	@echo ">> yarn install (root workspace: erun-kit, erun-console, erun-ui/frontend)"
	@yarn install --frozen-lockfile
	@echo ">> issue-reference gate (erun-kit, erun-ui/frontend, erun-console)"
	@node --test scripts/check-issue-references.test.mjs
	@node scripts/check-issue-references.mjs erun-kit/src erun-ui/frontend/src erun-console/src
	@echo ">> generating erun-ui/frontend wailsjs bindings"
	@./erun-ui/generate-wailsjs.sh
	@( \
		printf 'erun-kit\terun-kit gates\tcd erun-kit && yarn typecheck && yarn lint -- --cache --cache-strategy content --cache-location $(FRONTEND_LINT_CACHE_DIR)/eslint/erun-kit/ && yarn format:check -- --cache --cache-strategy content --cache-location $(FRONTEND_LINT_CACHE_DIR)/prettier/erun-kit.json && yarn build && yarn test\n'; \
		printf 'erun-ui-frontend\terun-ui/frontend gates\tcd erun-ui/frontend && yarn typecheck && yarn lint -- --cache --cache-strategy content --cache-location $(FRONTEND_LINT_CACHE_DIR)/eslint/erun-ui-frontend/ && yarn format:check -- --cache --cache-strategy content --cache-location $(FRONTEND_LINT_CACHE_DIR)/prettier/erun-ui-frontend.json && yarn build && yarn test\n'; \
		printf 'erun-console\terun-console gates\tcd erun-console && yarn typecheck && yarn lint -- --cache --cache-strategy content --cache-location $(FRONTEND_LINT_CACHE_DIR)/eslint/erun-console/ && yarn format:check -- --cache --cache-strategy content --cache-location $(FRONTEND_LINT_CACHE_DIR)/prettier/erun-console.json && yarn build && yarn test\n' \
	) | ./scripts/parallel-gate.sh $(FRONTEND_GATE_PARALLELISM) test-frontend

# Builds a headless erun-app (desktop tags) and runs the mandatory
# erun-ui/playwright suite against it, inside the same erun-devops test stage
# that already runs the rest of `make check` (the toolchain -- Wails/
# webkit CGO deps plus Playwright's Chromium runtime libraries -- is now
# installed in that stage's Dockerfile, verified empirically to install
# cleanly and run headless with zero display there, same as the final image
# already did for in-pod contribute-mode builds). Delegates to run.sh, which
# owns the build-if-needed/install-deps-if-needed logic (erun-ui/playwright/
# AGENTS.md's "Headless Launch") and self-detaches through agent-gate.sh when
# run inside an agent pod; inside this Dockerfile stage neither applies
# (ERUN_ENV_TYPE is unset during a docker build), so it just runs in place.
#
# Now a check-gate prerequisite (see check-gate's own comment for the
# evidence). It was not always: a real run against main once found 27
# failing specs, with two full runs on the same commit producing different
# failure sets (27 vs 24) -- the suite was not deterministic under parallel
# load. #1937's fixture-isolation fix (the shared seeded-baseline-row cache
# leak) resolved that, re-verified by repeated full-suite runs with zero
# failures before this target joined check-gate. Run this by hand, or via
# `erun exec job` in an agent env, when iterating on a fix -- it no longer
# needs `--skip-lint`/manual wiring to get signal, but a full run still
# costs ~20 minutes.
#
# `erun build` narrows what this target actually runs: it resolves a
# PLAYWRIGHT_TEST_AREAS build-arg (applyPlaywrightAreaBuildArgs in
# erun-common/build_playwright_areas.go) from the Playwright spec-file diff
# against the merge base and threads it into this Dockerfile's RUN step as
# an env var of the same name, which run.sh reads directly (see its own
# header comment) -- no argument passing needed here since Make already
# exports the recipe's environment into everything it execs. See
# erun-ui/playwright/AGENTS.md's "Area-scoped gate selection" section for the
# area taxonomy and the selection rule.
#
# A real prerequisite, not just prose: run.sh builds the production-tagged
# erun-app, which needs both erun-ui/frontend/dist (the go:embed in
# assets_production.go) and the regenerated erun-ui/frontend/wailsjs/
# bindings -- test-frontend produces both. Under check-gate's `-j` fan-out
# (see check-gate's own comment) this is what stops test-playwright from
# starting against a half-written frontend build; every other check-gate
# prerequisite is independent and may run alongside either of these two.
test-playwright: test-frontend

test-playwright:
	@echo ">> erun-ui/playwright suite (desktop tags)"
	@(cd erun-ui/playwright && ./run.sh)

# Cross-compiles erun-app for Windows to prove the one other platform erun-ui
# ships to (Scoop, built from source at install time) still compiles and
# links. No CGO needed: unlike the darwin backend (real cgo + Objective-C,
# see the macOS bullet in erun-ui/AGENTS.md's "End-to-end UI tests" section),
# Wails' windows backend (`go-webview2`) drives WebView2 over COM through
# plain `syscall` -- confirmed by grepping the module for `import "C"` --
# so a stock cross-compiling `go build` is sufficient and needs no toolchain
# this Dockerfile doesn't already have. Compile+link only: this never runs
# the resulting binary, so it proves nothing about WebView2 runtime
# behaviour, only that the Windows-only build-constrained source is not
# broken. Needs erun-ui/frontend/dist for the go:embed in
# assets_production.go -- a real prerequisite on test-frontend (which
# produces it), the same reasoning as test-playwright's own above.
test-erun-ui-windows-build: test-frontend

test-erun-ui-windows-build:
	@echo ">> erun-ui Windows cross-compile (desktop tags)"
	@(cd erun-ui && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
		go build -tags "desktop,production" -ldflags "-H windowsgui" \
		-o /tmp/erun-app-windows-cross-check.exe .)
	@rm -f /tmp/erun-app-windows-cross-check.exe

# Bound on how many chart-test scripts run at once. Each is a single `helm
# template` render (no cluster, no docker), so unlike lint this scales cleanly
# with width and memory stays flat: measured on a 12-core pod at p1 2.7s, p2
# 2.4s, p4 1.9s, p8 (every current script at once) 1.1s wall, ~1.25-1.3GiB
# peak memory across all widths (see erun#1690) -- i.e. no per-job slope to
# derive a cost from, unlike LINT_JOB_MEMORY_MIB above. Sized by
# scripts/parallel-gate.sh's `width` mode off the real CPU quota like
# LINT_PARALLELISM (see its comment for why that isn't `nproc`), capped at the
# actual script count so a growing k8s/ directory can't launch unboundedly
# many at once. HELM_CHART_TEST_JOB_MEMORY_MIB is the flat measured total
# (~1.3GiB) divided across the current script count -- deliberately not a
# fabricated per-job slope, since none was observed; the memory term is
# expected to stay non-binding here and CPU/script-count to decide the width.
HELM_CHART_TEST_JOB_MEMORY_MIB := 163
HELM_CHART_TEST_PARALLELISM ?= $(shell ./scripts/parallel-gate.sh width $(words $(wildcard erun-devops/k8s/*_test.sh)) $(HELM_CHART_TEST_JOB_MEMORY_MIB))

# Helm-render assertions for the erun-devops/k8s charts (erun-devops,
# erun-backend-postgres, erun-backend-db, erun-backend-api, erun-oci-registry,
# erun-zitadel, erun-console, erun-docs): each *_test.sh renders its chart with
# `helm template` and asserts on the output. No cluster, no docker -- pure
# rendering -- so a pinned `helm` binary is all the image test stage needs to
# run these (see the Dockerfile's test stage). Iterates the directory rather
# than naming each script so a new chart's *_test.sh is picked up with no
# Makefile edit. Scripts run concurrently (bounded by
# HELM_CHART_TEST_PARALLELISM) via scripts/parallel-gate.sh, which buffers
# each script's output and emits it atomically under its ">> <script>"
# marker, then runs every script and reports every failing one on a single
# "helm-chart-tests failed in:" line -- deliberately aggregate rather than
# the prior fail-fast `|| exit 1`: once the scripts run in parallel, a
# fail-fast exit has already paid for every other script's `helm template`
# work, so discarding those results on the first failure buys nothing (see
# erun#1690).
helm-chart-tests:
	@for t in erun-devops/k8s/*_test.sh; do \
		printf '%s\t%s\t%s\n' "$$t" "$$t" "sh $$t"; \
	done | ./scripts/parallel-gate.sh $(HELM_CHART_TEST_PARALLELISM) helm-chart-tests

# End-to-end proof that a postgres restart cannot destroy committed data,
# against a real postgres and the real atlas migrations. Deliberately NOT part
# of `check`: it needs a real docker daemon and the atlas CLI, and the image
# test stage's bare golang image has neither -- there is no nested docker
# daemon available inside a `docker build` RUN step. Run this by hand, or via
# `erun exec job` in an agent env (which does carry both), before merging any
# change to postgres reset, migrate, or restart behavior.
test-postgres-restart:
	sh erun-devops/docker/erun-backend-db/migrate_test.sh

# End-to-end proof of every retention sweep's age/count bounds against a real
# postgres and the real migrations, same "needs a real docker daemon and the
# atlas CLI, neither available in the bare test-stage image" exclusion from
# make check as test-postgres-restart above. Iterates retention*_test.sh
# (one script per policy group, run against its own postgres container) so a
# new policy's test is picked up with no Makefile edit, the same reasoning
# helm-chart-tests' directory iteration gives. Run this by hand, or via
# `erun exec job` in an agent env, before merging a change to
# erun-backend-db/retention/*.sql or the retention CronJob.
test-retention:
	@for t in erun-devops/docker/erun-backend-db/retention*_test.sh; do \
		echo "=== $$t ==="; \
		sh "$$t" || exit 1; \
	done

# End-to-end proof that erun_operations -- the role every retention policy
# runs as -- can actually INSERT then DELETE a row in every table under an
# implemented or designed retention policy (the #1968 six-table sweep plus
# builds/gate_runs), and remains refused on audit_events/usage_events per
# #1959's deliberate append-only carve-out. Same "needs a real docker daemon
# and the atlas CLI" exclusion from make check as test-retention above. Run
# this by hand, or via `erun exec job` in an agent env, before merging a
# change to schema/roles.sql or any retention policy's target tables.
test-retention-grants:
	sh erun-devops/docker/erun-backend-db/retention_grants_test.sh

# End-to-end proof that the declarative schema (schema/*.sql, atlas.hcl's
# source of truth) and the migration-applied state (migrations/default/*.sql)
# describe the same database -- a grant, trigger, or constraint that exists
# in one and not the other is exactly #2022's bug (a grant shipped only in a
# migration, invisible until a scheduled job hit a permission error at run
# time) and is otherwise invisible until something exercises it. Same "needs
# a real docker daemon and the atlas CLI" exclusion from make check as
# test-retention above. Run this by hand, or via `erun exec job` in an agent
# env, before merging a change to atlas.hcl, schema/, or migrations/default/.
test-schema-drift:
	sh erun-devops/docker/erun-backend-db/schema_drift_test.sh

# End-to-end proof that the console's nginx config (default.conf.template)
# never resolves a missing content-hashed asset or a health/version request to
# the SPA shell (erun#2064). Same "needs a real docker daemon" exclusion from
# make check as the tests above -- it needs to observe actual nginx
# location/try_files behavior. Run this by hand, or via `erun exec job` in an
# agent env, before merging a change to erun-devops/docker/erun-console/.
test-console-nginx:
	sh erun-devops/docker/erun-console/nginx_test.sh

# Build, run, and coverage-gate the erun integration suite.
# The coverage threshold defaults to the value pinned in
# erun-integration/scripts/integration-test.sh; override with
# COVERAGE_THRESHOLD=NN. To refresh testdata files in place, run the script
# directly with --update-golden (./erun-integration/scripts/integration-test.sh
# --update-golden) — gate mode refuses outright if UPDATE_GOLDEN is set in the
# environment, so it cannot be reseeded via `make check UPDATE_GOLDEN=1`.
#
# Detaches through the same wrapper as `check` below, since root AGENTS.md
# tells contributors to run this standalone before pushing and it is long
# enough on its own to hit the same foreground-timeout failure inside an
# agent pod. check-gate depends on integration-test-gate directly rather than
# on this target, so a `make check` run never nests one detached job inside
# another.
integration-test:
	./scripts/agent-gate.sh integration-test "make integration-test" -- $(MAKE) integration-test-gate

integration-test-gate:
	./erun-integration/scripts/integration-test.sh

# The front door. Everywhere but an agent pod this is check-gate by another
# name: scripts/agent-gate.sh execs it directly and exits with exactly its
# status. Inside an agent pod's own coding-agent session (ERUN_ENV_TYPE
# local-agent or remote-agent) it instead detaches check-gate through erun's
# job primitive and awaits it for a bounded window, so a 20-40 minute run
# never sits as an ordinary foreground command for an agent harness to
# auto-background into a bare task handle -- the caller either gets the real
# result or a timeout that says to call `make check` again, either way in a
# small, bounded number of calls. See scripts/agent-gate.sh for why this is
# the fix and not just documentation.
#
# check-gate's own ten prerequisites (below) used to run back-to-back: on a
# real release, the first seven alone (everything before test-playwright)
# cost ~14.5 minutes, and test-playwright is the single largest of the ten by
# itself (measured standalone at ~16.4 minutes -- more than every other
# target combined). `-j` is what actually parallelizes them: check-gate's own
# prerequisite line has to keep every target listed in plain, literal text
# for erun-integration/build_check_coverage_test.go and
# erun_ui_windows_cross_compile_test.go, which parse the Makefile's real text
# (never execute it) to confirm each module's tests are truly wired into
# `make check` -- so the fan-out can't be moved into a recipe body the way
# lint/test-frontend/helm-chart-tests dispatch their own internal fan-out
# through scripts/parallel-gate.sh (that would leave check-gate's own line
# with no prerequisites, which is exactly the drift those gates exist to
# catch). Standard `make` prerequisite semantics already give this the
# ordering it needs for free -- the two `: test-frontend` lines a few lines
# below are real edges in the same DAG `-j` schedules, not a parallel
# bookkeeping system -- and `make`'s own job server is a true event-driven
# scheduler (a slot is reused the instant any job frees it), which is a
# strictly better fit here than replaying scripts/parallel-gate.sh's
# fixed-batch model would be for ten wildly uneven-duration jobs.
CHECK_GATE_TARGET_COUNT := 10
CHECK_GATE_PARALLELISM ?= $(shell ./scripts/parallel-gate.sh width $(CHECK_GATE_TARGET_COUNT) "")

check:
	./scripts/agent-gate.sh check "make check" -- $(MAKE) -j$(CHECK_GATE_PARALLELISM) check-gate

# The full in-build gate: golangci-lint, erun-ui's own Go tests,
# erun-backend-api's own Go tests, erun-mcp's own Go tests,
# erun-devops/dns01-webhook's own Go tests, the frontend kit + desktop
# frontend + console gates, the erun-ui Windows cross-compile check, the
# erun-ui/playwright desktop e2e suite, the
# erun-devops/k8s chart tests, then the integration suite + coverage. The
# erun-devops image test stage runs this (via `check`, which is inert outside
# an agent pod); a failure tags no image. test-postgres-restart is
# deliberately excluded -- see its own comment above for why.
#
# test-playwright joined this list once the suite's own flakiness was
# resolved and re-verified, not merely once the toolchain existed (root
# AGENTS.md "Integration Test Gate" and erun-ui/playwright/AGENTS.md's "No
# flaky tests" carried the exact bar: a repeated-run track record, not one
# clean run). It has one now: a --repeat-each=5 full-suite run (2,525/2,525
# passed, the whole suite five times over) plus two further independent
# full runs (514/514 each, one against the exact commit this comment landed
# on) -- zero failures across every full-suite execution recorded this
# session. Before this, the suite had 27 failing specs and produced
# different failure sets across repeated runs on the same commit -- see the
# git history of this target for the original exclusion and #1937 for the
# fixture-isolation fix that resolved it. A red here is therefore a real
# regression, never "the suite crying wolf" -- fix it in the same PR per
# root AGENTS.md's "Fixing pre-existing issues is mandatory" rule, do not
# revert this line.
#
# These ten run concurrently, bounded by CHECK_GATE_PARALLELISM (see
# `check`'s own comment above for the measured cost this replaced, why `-j`
# rather than scripts/parallel-gate.sh is what drives it here, and where the
# two real ordering dependencies -- test-playwright and
# test-erun-ui-windows-build each needing test-frontend -- are declared).
# Do not drop any of the ten from this line to move the fan-out elsewhere:
# erun-integration/build_check_coverage_test.go and
# erun_ui_windows_cross_compile_test.go both parse this exact line's text to
# confirm every module's tests are really wired into `make check`, and fail
# if any of these names is missing from it.
check-gate: lint test-erun-ui test-erun-backend-api test-erun-mcp test-erun-dns01-webhook test-frontend test-erun-ui-windows-build test-playwright helm-chart-tests integration-test-gate

# A fast, local subset of check-gate for the cheap-and-common failures that
# don't need a full check-gate cycle to find: golangci-lint findings, the
# tracker-reference gate (root AGENTS.md § "Code Comments"), and prettier
# formatting. This is NOT a substitute for check/check-gate -- it runs no
# tests, no build, and no integration suite, so a green fast-check says
# nothing about those. It exists purely so a contributor (human or agent)
# can catch the failures it does cover in seconds locally instead of one
# ~9-10 minute merge-gate cycle later. Measured against a day where 5 of the
# gate's reds were exactly this class of failure (3x tracker reference, 1x
# prettier on a markdown file, 1x erun-common lint finding): fast-check
# reproduces all 5 in ~30s warm (golangci-lint's own cache and node_modules
# already present) and comfortably under a minute cold, against 9-10 minutes
# to discover the same failure via a full gate cycle.
#
# Both halves of the tracker-reference gate are scoped down from their
# check-gate homes rather than reimplemented: the Go half normally only runs
# as part of `go test ./...` inside integration-test-gate, which also builds
# the instrumented erun binary for every other scenario in the module -- but
# TestNoIssueReferenceInCode/TestIssueReferenceBaselineIsCurrent don't call
# erun.Run, so scoping `go test` to erun-integration's root package (`.`
# rather than `./...`) compiles just that package and skips the binary build
# entirely, while the test itself still walks the whole repo tree (it
# resolves its own root independently of which package invoked it). The
# TypeScript half normally runs inside test-frontend after a full yarn
# install plus every workspace's typecheck/lint/build/test; here it runs
# directly against the same node script test-frontend calls, with nothing
# else in front of it.
#
# -count=1 on the Go half is load-bearing, not belt-and-braces (the same
# reasoning as test-erun-ui's own -count=1 above): the test walks the repo
# tree with os/filepath at run time, which Go's test cache cannot see as an
# input, so a second invocation right after adding a tracker reference
# elsewhere in the tree replayed a stale cached "ok" and missed it -- caught
# by hand while validating this target, not theoretical.
#
# Prettier runs the same `yarn format:check` each workspace's own
# package.json already defines, across all three workspaces at once via
# scripts/parallel-gate.sh (same aggregated-output/single-failure-report
# contract as the `lint` target above) rather than the width-computed
# parallelism lint and helm-chart-tests use -- there are only ever three
# fixed jobs here, not a directory-scanned list that could grow unboundedly,
# so a flat parallelism of 3 needs no cgroup-derived sizing to stay safe.
fast-check: lint
	@echo ">> issue-reference gate (Go, whole repo)"
	@(cd erun-integration && go test -count=1 -run '^(TestNoIssueReferenceInCode|TestIssueReferenceBaselineIsCurrent)$$' .)
	@echo ">> yarn install (root workspace: erun-kit, erun-console, erun-ui/frontend)"
	@yarn install --frozen-lockfile
	@echo ">> issue-reference gate (TypeScript: erun-kit, erun-ui/frontend, erun-console)"
	@node --test scripts/check-issue-references.test.mjs
	@node scripts/check-issue-references.mjs erun-kit/src erun-ui/frontend/src erun-console/src
	@echo ">> prettier --check (erun-kit, erun-ui/frontend, erun-console)"
	@for d in erun-kit erun-ui/frontend erun-console; do \
		printf '%s\t%s\t%s\n' "$$d" "prettier $$d" "cd $$d && yarn format:check"; \
	done | ./scripts/parallel-gate.sh 3 fast-check-prettier
