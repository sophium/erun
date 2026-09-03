.PHONY: integration-test integration-test-gate lint test-erun-ui test-erun-backend-api test-erun-mcp test-erun-dns01-webhook test-frontend test-playwright helm-chart-tests test-postgres-restart test-retention check check-gate

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
LINT_TIMEOUT ?= 15m

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
test-erun-backend-api:
	@echo ">> go test erun-backend-api"
	@(cd erun-backend/erun-backend-api && go test ./...)

# erun-mcp's own Go tests. LINT_MODULES above already gives this module
# golangci-lint, but nothing ran `go test ./...` for it: erun-mcp is unioned
# into erun-cli/go.work and erun-integration/go.work, but a `use` directive in
# a go.work only resolves local module dependencies for the build — it does
# not make a sibling module's packages match another module's own `./...`, so
# neither erun-cli's nor erun-integration's `go test ./...` ever runs a single
# erun-mcp test. 282 test cases (including subtests) sat green on every
# contributor's own machine and reachable by nobody's gate.
test-erun-mcp:
	@echo ">> go test erun-mcp"
	@(cd erun-mcp && go test ./...)

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
# context.
test-frontend:
	@echo ">> yarn install (root workspace: erun-kit, erun-console, erun-ui/frontend)"
	@yarn install --frozen-lockfile
	@echo ">> issue-reference gate (erun-kit, erun-ui/frontend, erun-console)"
	@node --test scripts/check-issue-references.test.mjs
	@node scripts/check-issue-references.mjs erun-kit/src erun-ui/frontend/src erun-console/src
	@echo ">> erun-kit gates"
	@(cd erun-kit && yarn typecheck && yarn lint && yarn format:check && yarn build && yarn test)
	@echo ">> generating erun-ui/frontend wailsjs bindings"
	@./erun-ui/generate-wailsjs.sh
	@echo ">> erun-ui/frontend gates"
	@(cd erun-ui/frontend && yarn typecheck && yarn lint && yarn format:check && yarn build && yarn test)
	@echo ">> erun-console gates"
	@(cd erun-console && yarn typecheck && yarn lint && yarn format:check && yarn build && yarn test)

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
# Deliberately NOT a check-gate prerequisite yet. Wiring it in now would red
# every build on main: a real run against main found 27 failing specs, and
# two full runs on the same commit produced different failure sets (27 vs 24,
# only 3 files red in both) -- the suite is not deterministic under parallel
# load today. Gating on a suite that cries wolf is worse than the coverage
# gap it would close (see erun-ui/playwright/AGENTS.md's "No flaky tests"
# rule: fix the nondeterminism, never retry/quarantine it away). Run this by
# hand, or via `erun exec job` in an agent env, to validate a fix to either
# the failures or the flakiness; check-gate grows this as a real prerequisite
# once the suite is green and stays green across repeated runs.
test-playwright:
	@echo ">> erun-ui/playwright suite (desktop tags)"
	@(cd erun-ui/playwright && ./run.sh)

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

# End-to-end proof of the comments/releases retention sweep's age/count
# bounds against a real postgres and the real migrations, same "needs a real
# docker daemon and the atlas CLI, neither available in the bare test-stage
# image" exclusion from make check as test-postgres-restart above. Run this
# by hand, or via `erun exec job` in an agent env, before merging a change to
# erun-backend-db/retention/*.sql or the retention CronJob.
test-retention:
	sh erun-devops/docker/erun-backend-db/retention_test.sh

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
check:
	./scripts/agent-gate.sh check "make check" -- $(MAKE) check-gate

# The full in-build gate: golangci-lint, erun-ui's own Go tests,
# erun-backend-api's own Go tests, erun-mcp's own Go tests,
# erun-devops/dns01-webhook's own Go tests, the frontend kit + desktop
# frontend + console gates, the erun-devops/k8s chart tests, then the
# integration suite + coverage. The
# erun-devops image test stage runs this (via `check`, which is inert outside
# an agent pod); a failure tags no image. test-postgres-restart is
# deliberately excluded -- see its own comment above for why.
check-gate: lint test-erun-ui test-erun-backend-api test-erun-mcp test-erun-dns01-webhook test-frontend helm-chart-tests integration-test-gate
