.PHONY: integration-test integration-test-gate lint test-erun-ui test-frontend helm-chart-tests test-postgres-restart check check-gate

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
# carries node/yarn (added for test-frontend below), but erun-ui/frontend's
# own frontend gate (`yarn typecheck && yarn lint && yarn test`) deliberately
# stays out of `make check` and still runs only in erun-ui/build.sh:
# test-frontend's `yarn install` resolves it too (it is a workspace member),
# but does not run its gate, since that is `erun-ui/build.sh`'s job. The
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
# Scaled off `nproc` rather than a flat constant so a small pod runs fewer
# concurrent golangci-lint processes -- each wants the full core count, and a
# flat width sized for a 12-core pod would oversubscribe a 4-core one far
# harder and multiply its peak memory for no wall-clock benefit it has the
# cores to use anyway. Capped at the module count: more workers than modules
# cannot help.
LINT_PARALLELISM ?= $(shell n=$$(nproc 2>/dev/null || echo 4); m=$(words $(LINT_MODULES)); if [ "$$n" -lt "$$m" ]; then echo "$$n"; else echo "$$m"; fi)

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

# The shared frontend kit (erun-kit) and the hosted console (erun-console) —
# the two Yarn-workspace members outside erun-ui/frontend (in the workspace,
# so `yarn install` here resolves all three, but its own gate stays in
# erun-ui/build.sh; see that target's comment for why). Closes the gap where
# the console's own gates (`erun-console/AGENTS.md`) previously ran "by hand
# or not at all", so the module drifted from the desktop's design system by
# default.
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
test-frontend:
	@echo ">> yarn install (root workspace: erun-kit, erun-console, erun-ui/frontend)"
	@yarn install --frozen-lockfile
	@echo ">> issue-reference gate (erun-kit, erun-ui/frontend, erun-console)"
	@node --test scripts/check-issue-references.test.mjs
	@node scripts/check-issue-references.mjs erun-kit/src erun-ui/frontend/src erun-console/src
	@echo ">> erun-kit gates"
	@(cd erun-kit && yarn typecheck && yarn lint && yarn format:check && yarn build)
	@echo ">> erun-console gates"
	@(cd erun-console && yarn typecheck && yarn lint && yarn format:check && yarn build && yarn test)

# Bound on how many chart-test scripts run at once. Each is a single `helm
# template` render (no cluster, no docker), so unlike lint this scales cleanly
# with width and memory stays flat: measured on a 12-core pod at p1 2.7s, p2
# 2.4s, p4 1.9s, p8 (every current script at once) 1.1s wall, ~1.25-1.3GiB
# peak memory across all widths (see erun#1690). Scaled off `nproc` like
# LINT_PARALLELISM anyway, capped at the actual script count, so a small pod
# doesn't launch more `helm template` processes than it has cores for and a
# growing k8s/ directory can't launch unboundedly many at once.
HELM_CHART_TEST_PARALLELISM ?= $(shell n=$$(nproc 2>/dev/null || echo 4); m=$(words $(wildcard erun-devops/k8s/*_test.sh)); if [ "$$n" -lt "$$m" ]; then echo "$$n"; else echo "$$m"; fi)

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

# The full in-build gate: golangci-lint, erun-ui's own Go tests, the frontend
# kit + console gates, the erun-devops/k8s chart tests, then the integration
# suite + coverage. The erun-devops image test stage runs this (via `check`,
# which is inert outside an agent pod); a failure tags no image.
# test-postgres-restart is deliberately excluded -- see its own comment above
# for why.
check-gate: lint test-erun-ui test-frontend helm-chart-tests integration-test-gate
