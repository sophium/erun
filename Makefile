.PHONY: integration-test integration-test-gate lint test-erun-common test-erun-ui test-erun-backend-api test-erun-backend-api-e2e test-erun-mcp test-erun-dns01-webhook test-frontend test-playwright test-erun-ui-windows-build helm-chart-tests terraform-module-tests test-postgres-restart test-retention test-retention-grants test-schema-drift test-atlas-validate test-console-nginx check check-gate fast-check

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
# 24-core node before the build-container CPU cap started holding it to its
# declared cpu= (commonly 4). Once every module actually got only that many
# cores, the fixed 15m stopped fitting: every module reported "0 issues" and
# then hit the timeout anyway, turning an environmental CPU shortage into a
# false-red gate. Scale that reference budget -- 15m at 22 cores -- by the
# smallest CPU erun sizes a build environment at (MinimumRuntimeDindCPU in
# erun-common/runtime_resources.go, "the floor RuntimeDindCPULimit refuses to
# size a build below") rather than by this pod's own quota, so the same
# commit's lint is handed the same deadline wherever it runs: 15m * 22 / 4 =
# 82m, past the ~24.5m (1468s) a starved run was observed to take before the
# old formula replaced it, and past the longest lint measured on the fleet
# since. LINT_TIMEOUT's `?=` keeps the existing manual override: an explicit
# `LINT_TIMEOUT=<duration> make check` (or an env var of the same name) still
# wins over this computed default.
#
# The budget reads no cgroup and no PARALLEL_GATE_CPU_LIMIT on purpose. It
# used to be scaled inversely with this pod's own resolved quota, which made
# the lint's deadline -- and so the meaning of a lint red -- a property of the
# machine that ran it: the 22-core environment resolved the 15m floor, the
# 8-core ones 41m, and `erun review record-build --gate --failed` cannot tell
# a lint that found something from one killed by a deadline its environment
# never had a chance to meet. The scaling's premise was that wall-clock scales
# as 1/cores; the measured runs do not, because a lint's fixed phases --
# package loading, the warm cache, the twelve other targets check-gate fans
# out beside it -- do not parallelize: the same commit's lint took 912s, 1296s
# and up to 2569s on the *most*-resourced environment in the fleet, which the
# old formula handed the *least* time. A budget that is the same everywhere is
# what makes a red attributable wherever it was produced.
#
# The trade is deliberate and stated rather than hidden: a lint that never
# finishes -- or never loads its packages, and so has no report to read -- is
# now diagnosed in up to 82m on every environment, where a 22-core pod used to
# give up at 15m. Since the verdict is read from golangci-lint's own report
# rather than from its deadline (see the `lint` target's comment), a deadline
# expiring around an analysis that did report is not a failure: the only red
# this budget can still produce is a starved run killed before it could report
# at all, which is the false red this formula's lineage exists to remove, and
# it costs a review ejected from the merge queue plus a full cold gate
# rebuild. A wedged run needs a human either way; a healthy one must not be
# failed for being slow.
LINT_TIMEOUT_REFERENCE_CPU := 22
LINT_TIMEOUT_BASE_MINUTES := 15
# The smallest CPU erun sizes a build environment at, named rather than inlined
# so the resolved budget below stays auditable against that floor -- and fixed
# rather than read from this pod, so the budget is identical everywhere.
LINT_TIMEOUT_FLEET_MIN_CPU := 4
LINT_TIMEOUT ?= $(shell echo "$$(( $(LINT_TIMEOUT_BASE_MINUTES) * $(LINT_TIMEOUT_REFERENCE_CPU) / $(LINT_TIMEOUT_FLEET_MIN_CPU) ))m")

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

# check-gate's own `-j` fan-out (see check-gate's own comment further below)
# can run lint, test-frontend, and helm-chart-tests concurrently with each
# other, and each of the three independently sizes its own width against the
# *entire* memory ceiling via scripts/parallel-gate.sh -- three individually
# safe widths can still sum past the box's real ceiling once check-gate runs
# them side by side: independent parallelism mechanisms can double-book memory
# even when each is individually safe. CHECK_GATE_FANOUT_PEAK_MEMORY_MIB is the
# largest of the three's own already-measured peaks -- lint's own worst case,
# every LINT_MODULES entry running at once -- and each of the three passes it
# as parallel-gate.sh width's reserved-mem-mib argument before dividing what
# is left among its own jobs. In a box sized like the reference build
# environment (~20GiB, see erun-devops/AGENTS.md's dind sidecar defaults),
# none of the three's own job-count/CPU caps are memory-bound in the first
# place, so this reservation is a no-op there; it only narrows a width in a
# smaller environment where memory actually binds -- exactly the case this
# guards against.
CHECK_GATE_FANOUT_PEAK_MEMORY_MIB := $(shell echo $$(( $(words $(LINT_MODULES)) * $(LINT_JOB_MEMORY_MIB) )))
LINT_PARALLELISM ?= $(shell ./scripts/parallel-gate.sh width $(words $(LINT_MODULES)) $(LINT_JOB_MEMORY_MIB) $(CHECK_GATE_FANOUT_PEAK_MEMORY_MIB))

# LINT_GOMAXPROCS bounds what each golangci-lint invocation may take, so the
# fan-out above stops overcommitting the machine several times over.
#
# parallel-gate.sh sizes a width as min(job-count, CPUs, memory/job) -- one CPU
# per job. That is right for a job that is one process, and wrong for every
# entry here: golangci-lint is internally parallel and takes GOMAXPROCS from
# the cgroup, so each of the LINT_PARALLELISM invocations helps itself to the
# whole quota. In the in-image gate that is 6 invocations x 16 CPUs = 96
# against a 16-CPU cap, and the cost is not merely queueing -- at that ratio
# the build spent 79% of its CPU periods throttled and package downloads began
# timing out (erun#2390), which reads as a network fault and is not.
#
# Divide the quota by the width instead, floored at 1 so a small environment
# still runs. Total demand becomes about the quota rather than a multiple.
LINT_GOMAXPROCS ?= $(shell cpu=$$(./scripts/parallel-gate.sh cpu-quota); \
	n=$$(( cpu / $(LINT_PARALLELISM) )); \
	[ "$$n" -ge 1 ] || n=1; \
	echo $$n)

# GO_TEST_GOMAXPROCS is the same bound as LINT_GOMAXPROCS above, for the other
# internally-parallel Go fan-out in the gate: `go test ./...`, which builds and
# runs package test binaries up to GOMAXPROCS at a time and otherwise takes
# that number straight from the cgroup.
#
# The four module test targets, the dns01-webhook one, and the integration
# suite are siblings in check-gate's own -j fan-out, so each unbounded one
# claims the whole quota and six of them running side by side demand six times
# it. The integration suite is a Go test runner like the rest even though its
# width arrives as `-parallel` from integration-test.sh rather than as
# GOMAXPROCS, so it is counted here too and takes the same share -- otherwise
# it sizes itself against the whole quota on top of the shares the counted
# targets already demand, which is the oversubscription this bound exists to
# prevent. Measured on the 6-CPU
# in-pod gate arrangement (lint plus all four module test targets, warm build
# cache, -j5): unbounded, 13.4% of CPU periods throttled and 127s of throttled
# CPU-time; with each target held to a fifth of the quota, 3.6% and 6.1s -- a
# 95% cut in the CPU-time spent queued behind the cgroup ceiling, at the same
# wall clock (183s vs 179s, and the gate's tests all still run: same packages,
# same -race, same -count=1). Throttling this deep is what turns into the
# syscall-timeout-shaped failures -- a timed-out package fetch, a timed-out
# linter run -- that read as network faults and are not (see the yarn
# --network-timeout note under test-frontend below).
#
# Divide the quota by the count of these targets rather than by a fan-out width
# the way lint does: make, not this recipe, is what runs them concurrently.
# Floored at 1 so a small environment still runs; on a larger one each target
# gets proportionally more.
GO_TEST_TARGET_COUNT := 6
GO_TEST_GOMAXPROCS ?= $(shell cpu=$$(./scripts/parallel-gate.sh cpu-quota); \
	n=$$(( cpu / $(GO_TEST_TARGET_COUNT) )); \
	[ "$$n" -ge 1 ] || n=1; \
	echo $$n)

# INTEGRATION_TEST_TIMEOUT is the wall-clock budget the integration suite's own
# `go test` runs under. Left to itself it inherits Go's ten-minute default,
# which does not move with the machine -- so a slow-but-progressing run is
# reported as a failure of the tree rather than of the clock.
#
# The gate is the case that matters, and it is a fixed deadline against a
# variable amount of work: the suite measured 4m7s on a quiet 12-CPU pod and
# crossed the 10m default on a contended one that reported 7487/13091 CPU
# periods throttled (57%) -- the same command, the same suite, a correct tree.
# No test was stuck; the goroutine dump at the alarm showed dozens of
# t.Parallel() scenarios queued in the parallelism barrier and the package
# still making progress. A gate that fails a correct tree for being slow is
# worse than a slow gate, because it teaches its operators to re-run it.
#
# Scale the budget inversely with the resolved CPU quota -- the shape
# LINT_TIMEOUT above used for the identical defect in the sibling linter run
# until its budget was made uniform across environments instead (see its
# comment); this one still has to fit the work to the machine.
# The quota is what GO_TEST_GOMAXPROCS already divides into this suite's
# `-parallel` share, so at or above GO_TEST_TARGET_COUNT CPUs the suite has its
# reference share and takes the base, and below it the suite is at its serial
# floor with proportionally less CPU to finish the same work in.
#
# The cap is not decoration: it is what keeps the budget provably below the
# harness's own per-child backstop, harnessexec.HangNet. That constant is
# deliberately longer than the package deadline so it can never fail a healthy
# child; an uncapped inverse scale would climb past it on a small environment
# and reopen that failure inside the harness. HangNet moves with this cap, and
# TestIntegrationSuiteTimeoutBudgetIsDerivedAndCappedUnderTheHangNet holds the
# two together.
#
# The quota is a floor on what an environment declares, not a ceiling on how
# slow it can be -- it cannot see contention from outside its cgroup, which is
# exactly the measured case -- so the base is generous in absolute terms rather
# than tight against the quiet measurement. The trade is deliberate: a genuinely
# wedged run is now diagnosed in up to CAP_MINUTES instead of 10m, and a
# correct-but-starved one finishes instead of failing.
INTEGRATION_TEST_TIMEOUT_REFERENCE_CPU := $(GO_TEST_TARGET_COUNT)
INTEGRATION_TEST_TIMEOUT_BASE_MINUTES := 30
INTEGRATION_TEST_TIMEOUT_CAP_MINUTES := 45
INTEGRATION_TEST_TIMEOUT ?= $(shell cpu=$$(./scripts/parallel-gate.sh cpu-quota); \
	m=$$(( $(INTEGRATION_TEST_TIMEOUT_BASE_MINUTES) * $(INTEGRATION_TEST_TIMEOUT_REFERENCE_CPU) / cpu )); \
	[ "$$m" -ge $(INTEGRATION_TEST_TIMEOUT_BASE_MINUTES) ] || m=$(INTEGRATION_TEST_TIMEOUT_BASE_MINUTES); \
	[ "$$m" -le $(INTEGRATION_TEST_TIMEOUT_CAP_MINUTES) ] || m=$(INTEGRATION_TEST_TIMEOUT_CAP_MINUTES); \
	echo "$${m}m")

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
#
# Each module's fan-out job is scripts/lint-module.sh, not `golangci-lint`
# directly, so the verdict is read from golangci-lint's own report instead of
# its exit status. The two disagree in exactly the case that reds this target
# for no finding: golangci-lint prints its whole report -- the findings, or
# "0 issues." -- and then overrides the code it decided from that report with
# the run's own deadline, so a clean analysis that outlived --timeout exits as
# a timeout and reads as a lint failure to anything watching the status,
# including `erun review record-build --gate --failed`.
#
# The lineage, because the deadline has moved twice and each move has a
# reason. The timeout used to be a flat 15m, calibrated on a build container
# that took 22 of a 24-core node; once the container's declared cpu= was
# actually enforced every module reported "0 issues" and then hit the deadline
# anyway, so the timeout was scaled inversely with the resolved quota and
# floored at that original 15m. That corrected the starved-environment end of
# the range and left the floor calibrated for a lint that owns the whole
# quota -- which is not what this target hands it. check-gate fans 13 targets
# out at -j13 and this target runs all six LINT_MODULES at LINT_PARALLELISM=6,
# so at PARALLEL_GATE_CPU_LIMIT=22 each lint gets LINT_GOMAXPROCS = 22/6 = 3
# cores while twelve other targets run beside it; erun-backend-api's analysis
# completed with "0 issues." and was reported as a lint failure at 912s and at
# 1296s against that 900s floor.
#
# Raising the floor was not the fix on its own: while the verdict still came
# from the deadline, moving the threshold only traded a short false red for a
# long one. The invariant is narrower than the timeout -- a lint that reported
# its whole result and found nothing must not red the gate -- so it is read
# off the report, and the deadline keeps its role as a bound on a lint that
# never finishes, or never loads its packages, which has no report to read and
# still fails. Reading the verdict off the report is also what makes raising
# the budget safe now: with no verdict riding on it, a longer deadline cannot
# turn a finding green -- it can only let a starved run finish far enough to
# say what it found -- and the run it waits for instead is one that never
# reported at all. LINT_TIMEOUT above is therefore both uniform and generous,
# for the same reason the verdict is read from the report and not from the
# clock: a red has to mean one thing, the same thing, wherever this gate runs.
#
# What this target does not leave implicit: the budget it ran under. The
# timeout above is the same on every environment now, but the fan-out width and
# the per-lint CPU share are not, and both decide how long a module's lint can
# take -- so a red still needs the numbers it ran under beside it to be read.
# The recipe therefore prints the numbers it passes (the timeout, the fan-out
# width, the per-lint GOMAXPROCS) beside the cpu quota the widths were derived
# from, once before the fan-out and again beside the aggregated failure line.
# It prints its own expanded variables rather than recomputing them, so an
# operator's LINT_TIMEOUT/LINT_PARALLELISM/LINT_GOMAXPROCS override is what
# shows up here: the numbers worth printing are the ones the modules got, not a
# second resolution of the formula that produced them.
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
	@cpu=$$(./scripts/parallel-gate.sh cpu-quota); \
	budget="LINT_TIMEOUT=$(LINT_TIMEOUT) LINT_PARALLELISM=$(LINT_PARALLELISM) LINT_GOMAXPROCS=$(LINT_GOMAXPROCS), the widths from a cpu quota of $$cpu (the timeout is the same on every environment)"; \
	echo ">> lint budget: $$budget"; \
	for m in $(LINT_MODULES); do \
		printf '%s\t%s\t%s\n' "$$m" "golangci-lint $$m" "$(CURDIR)/scripts/lint-module.sh $(LINT_GOMAXPROCS) $(LINT_TIMEOUT) $$m"; \
	done | ./scripts/parallel-gate.sh $(LINT_PARALLELISM) lint; \
	status=$$?; \
	[ "$$status" -eq 1 ] && echo ">> lint budget: $$budget (the failure above ran under this)" >&2; \
	exit $$status

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
	@(cd erun-ui && GOMAXPROCS=$(GO_TEST_GOMAXPROCS) go test -race -count=1 ./...)

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
	@(cd erun-backend/erun-backend-api && GOMAXPROCS=$(GO_TEST_GOMAXPROCS) go test -count=1 ./...)

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
	@(cd erun-mcp && GOMAXPROCS=$(GO_TEST_GOMAXPROCS) go test -count=1 ./...)

# erun-common's own Go tests. LINT_MODULES above already gives this module
# golangci-lint, but nothing ran `go test ./...` for it: erun-common is its
# own module, and every sibling that unions it into a go.work (erun-cli,
# erun-mcp, erun-integration) only resolves it as a build dependency -- the
# same `go.work` blind spot documented for erun-mcp above, applied to the
# module every other Go module in the repo depends on. 132 test files sat
# green on every contributor's own machine and reachable by nobody's gate.
#
# -count=1 is load-bearing, not belt-and-braces, same reasoning as
# test-erun-backend-api's own -count=1 above: dockerfile_copy_contract_test.go
# globs and reads every erun-devops/docker/*/Dockerfile at run time -- files
# in a different top-level module this test has no source dependency on --
# so editing one of those Dockerfiles without touching erun-common's own
# source would replay a stale cached "ok" under this module's own persistent
# BuildKit go-build cache mount and miss a drifted COPY/ADD contract.
#
# -race is load-bearing too: this module owns the activity-lease,
# job-supervisor, and workspace-sync concurrent state (see erun-common/AGENTS.md's
# single-writer contract), and turning it on found a real, previously-undetected
# data race the first time it ran here -- a task job's background goroutine
# recorded its own outcome as finished before its heartbeat's deferred
# ReleaseEnvironmentActivityLease call had actually completed, so a caller
# that polled the job as finished could race that still-running cleanup
# against a *different* test's own XDG_CACHE_HOME isolation reload of the
# shared adrg/xdg package state. Fixed by running the heartbeat/alive-beat
# stop explicitly before the outcome is recorded (job_task.go) rather than
# leaving it to a defer that ran after. Measured locally: ~1m for a plain
# `go test -count=1 ./...` run vs ~2m with `-race` added -- worth paying to
# keep this class of bug from going undetected again.
test-erun-common:
	@echo ">> go test erun-common"
	@(cd erun-common && GOMAXPROCS=$(GO_TEST_GOMAXPROCS) go test -race -count=1 ./...)

# erun-devops/dns01-webhook's own Go tests. This module has no entry in
# LINT_MODULES and no test stage of its own -- its Dockerfile only builds the
# binary (see erun-devops/AGENTS.md's Build Workflow section) -- so its real
# regression coverage (including a deleted-token-secret cleanup fix) sat
# reachable only by a contributor running `go test` from the module by hand.
test-erun-dns01-webhook:
	@echo ">> go test erun-devops/dns01-webhook"
	@(cd erun-devops/dns01-webhook && GOMAXPROCS=$(GO_TEST_GOMAXPROCS) go test ./...)

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
# Only the *self-test* of the regression-coverage gate (root AGENTS.md § "A
# Defect Fix Names Its Reproduction") runs here, never the gate itself: the
# gate reads git history, and this target runs inside the erun-devops image
# test stage's Docker build context, which has no `.git`. Running its pure
# classifier here is still the point -- it keeps the enforcement logic itself
# gated by check-gate, the same split erun-integration's structural gates use
# (classifier unit-tested on synthetic data, wiring supplies the real state).
# The gate's real invocation lives in fast-check below, which root AGENTS.md
# already requires before every push.
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
# Reserves room for lint/helm-chart-tests under check-gate's own concurrent
# `-j` fan-out -- see CHECK_GATE_FANOUT_PEAK_MEMORY_MIB's own comment above.
#
# 15, not 3: each workspace's five gates are dispatched as their own job
# rather than chained behind `&&`. They are independent -- typecheck, lint,
# format:check and test all read the workspace's sources, and `build` is the
# only writer, into `dist`, which none of the other four read. Chained, a
# workspace cost the sum of its five; dispatched separately it costs the
# longest. Measured on erun-ui/frontend, the workspace that gates the build:
# typecheck 3s, lint ~5s warm, format:check 8s, build 3s, test 64s -- ~83s
# chained against ~64s at its longest, and that workspace sits on the
# critical path (test-frontend -> test-playwright) where the saving is
# wall-clock rather than slack.
FRONTEND_GATE_JOB_COUNT := 15
FRONTEND_GATE_PARALLELISM ?= $(shell ./scripts/parallel-gate.sh width $(FRONTEND_GATE_JOB_COUNT) $(FRONTEND_GATE_JOB_MEMORY_MIB) $(CHECK_GATE_FANOUT_PEAK_MEMORY_MIB))

# vitest sizes its worker pool from os.availableParallelism(), which reads the
# container's whole CPU quota -- so a single `vitest run` claims all of it, and
# the vitest workspaces below are dispatched as separate *concurrent* jobs in
# the fan-out above. Two of them side by side therefore demand twice the quota
# before the other frontend jobs (build, lint, typecheck) or any concurrent
# check-gate target takes a share, and that oversubscription is spent as cgroup
# throttling. This is the in-image gate's starvation mechanism: a 2.8MB tarball
# fetches in 0.27s from an idle container on the same daemon, but the throttled
# install spends minutes on it and then dies as ESOCKETTIMEDOUT, reading as a
# network fault.
#
# Same bound, same reasoning, same shape as LINT_GOMAXPROCS above: divide the
# environment's real quota by the number of these jobs that actually run
# concurrently, rather than handing each the whole ceiling. It is derived from
# parallel-gate.sh cpu-quota (not a constant and not `nproc`, both of which
# misread a throttled cgroup -- see that script's comment) and floored at 1 so
# a small environment still runs.
#
# FRONTEND_VITEST_JOB_COUNT counts only the workspaces whose `yarn test`
# really runs vitest, since it is vitest's own pool that multiplies. A
# workspace that switches runner has to be counted here too;
# erun-integration/frontend_test_workers_bound_test.go reads this on every run
# and fails if a vitest workspace's job stops naming the bound.
FRONTEND_VITEST_JOB_COUNT := 2
FRONTEND_VITEST_WORKERS ?= $(shell cpu=$$(./scripts/parallel-gate.sh cpu-quota); \
	n=$$(( cpu / $(FRONTEND_VITEST_JOB_COUNT) )); \
	[ "$$n" -ge 1 ] || n=1; \
	echo $$n)

# The bound above says how many workers each vitest job may start, not that
# each one can. Under the oversubscription the whole of `make check` is built
# on -- this fan-out inside a check-gate target, inside check-gate's own `-j`
# fan-out -- a forked worker can be starved past vitest's own start timeout,
# and the pool then exits 1 with a message that reads exactly like a failing
# assertion while naming a different file each time. The bound is not the
# lever for that: vitest 4 exposes no knob for the timeout, and no worker
# count makes a starved start impossible. scripts/vitest-gate.sh runs both
# vitest jobs instead, keeps the bound reaching vitest unchanged, and
# separates the two outcomes so only the pool-start class is retried or
# reported as an environment fault. Its self-test runs below, so a classifier
# that stops distinguishing correctly fails the gate rather than quietly
# absorbing reds.

# ERUN_PLAYWRIGHT_WORKERS is the desktop suite's worker count, and it is
# resolved here -- beside every other quota-derived gate width -- rather than
# in the erun-devops Dockerfile, which used to compute it as DIND_CPU_LIMIT/2
# inline in a RUN line. That made a test-parallelism decision an incidental
# function of a resource limit: raising the sidecar's CPU cap silently raised
# the suite's worker count, and a cap of 4 could only ever yield 2 workers no
# matter what the gate could actually afford. The number is decided on its own
# terms now, and the CPU cap only reaches it as the environment's CPU quota,
# the same input every other width here divides.
#
# Two cores per worker, which is playwright.config.ts's own measured rule (a
# worker is a Go backend *and* a headless Chromium, and they compete: 3 workers
# on 4 cores timed out two specs, 2 passed clean; the 12-core environment runs
# 6 without a contention failure). One environment's worth of that rule is
# deliberately not the ceiling here: `make check` runs this suite concurrently
# with the five Go test targets, golangci-lint, the integration suite and the
# chart tests, all dividing the same quota, so the suite takes a bounded share
# of it rather than the whole of it -- 4 workers needs 8 of the environment's
# cores and leaves the rest of the fan-out its budget. Raise it by hand
# (`make test-playwright ERUN_PLAYWRIGHT_WORKERS=6`) on an environment that is
# not running the rest of the gate. Floored at 1 so a small environment still
# runs, and the same `?=` shape as the widths above so a caller (the
# contention repro script, or a one-off measurement) can override it.
PLAYWRIGHT_CPU_PER_WORKER := 2
PLAYWRIGHT_WORKER_CEILING := 4
ERUN_PLAYWRIGHT_WORKERS ?= $(shell cpu=$$(./scripts/parallel-gate.sh cpu-quota); \
	n=$$(( cpu / $(PLAYWRIGHT_CPU_PER_WORKER) )); \
	[ "$$n" -ge 1 ] || n=1; \
	[ "$$n" -le $(PLAYWRIGHT_WORKER_CEILING) ] || n=$(PLAYWRIGHT_WORKER_CEILING); \
	echo $$n)
export ERUN_PLAYWRIGHT_WORKERS

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

# --prefer-offline because a warm cache is not the same as an offline
# install: yarn classic still reaches the registry for packages it
# already holds, so one slow response fails a build that needed nothing
# from the network. That cost a release: ESOCKETTIMEDOUT on
# lucide-react after 614s of retries, with that exact tarball sitting in
# the cache. It stays a preference, not --offline, so a genuinely new
# dependency still resolves instead of failing the build outright.
#
# --network-timeout because the fetch that remains has to survive a loaded box.
# Yarn times a request out per-socket, and under `make -j6` the install cannot
# get enough CPU to service the socket even though bandwidth is fine: the same
# tarball fetches in 0.27s from a container on this very daemon when the box is
# idle, while the in-gate install spent 275s and then failed on it. Three
# releases died that way, each reading as a network fault (erun#2390). Raising
# the ceiling turns a hard failure into a slow success; it does not mask a real
# outage, which still fails once the longer window elapses.
test-frontend:
	@./scripts/timed-step.sh "yarn install (root workspace: erun-kit, erun-console, erun-ui/frontend)" \
		yarn install --frozen-lockfile --prefer-offline --network-timeout 600000
	@./scripts/timed-step.sh "issue-reference gate (erun-kit, erun-ui/frontend, erun-console)" \
		sh -c 'node --test scripts/check-issue-references.test.mjs && node scripts/check-issue-references.mjs erun-kit/src erun-ui/frontend/src erun-console/src'
	@./scripts/timed-step.sh "regression-coverage gate self-test" \
		node --test scripts/check-regression-coverage.test.mjs
	@./scripts/timed-step.sh "vitest worker-start classifier self-test" \
		sh scripts/vitest-gate_test.sh
	@./scripts/timed-step.sh "generating erun-ui/frontend wailsjs bindings" \
		./erun-ui/generate-wailsjs.sh
	@( \
		printf 'erun-kit-typecheck\terun-kit typecheck\tcd erun-kit && yarn typecheck\n'; \
		printf 'erun-kit-lint\terun-kit lint\tcd erun-kit && yarn lint -- --cache --cache-strategy content --cache-location $(FRONTEND_LINT_CACHE_DIR)/eslint/erun-kit/\n'; \
		printf 'erun-kit-format\terun-kit format:check\tcd erun-kit && yarn format:check -- --cache --cache-strategy content --cache-location $(FRONTEND_LINT_CACHE_DIR)/prettier/erun-kit.json\n'; \
		printf 'erun-kit-build\terun-kit build\tcd erun-kit && yarn build\n'; \
		printf 'erun-kit-test\terun-kit test\tcd erun-kit && yarn test\n'; \
		printf 'erun-ui-frontend-typecheck\terun-ui/frontend typecheck\tcd erun-ui/frontend && yarn typecheck\n'; \
		printf 'erun-ui-frontend-lint\terun-ui/frontend lint\tcd erun-ui/frontend && yarn lint -- --cache --cache-strategy content --cache-location $(FRONTEND_LINT_CACHE_DIR)/eslint/erun-ui-frontend/\n'; \
		printf 'erun-ui-frontend-format\terun-ui/frontend format:check\tcd erun-ui/frontend && yarn format:check -- --cache --cache-strategy content --cache-location $(FRONTEND_LINT_CACHE_DIR)/prettier/erun-ui-frontend.json\n'; \
		printf 'erun-ui-frontend-build\terun-ui/frontend build\tcd erun-ui/frontend && yarn build\n'; \
		printf 'erun-ui-frontend-test\terun-ui/frontend test\tcd erun-ui/frontend && $(CURDIR)/scripts/vitest-gate.sh --maxWorkers=$(FRONTEND_VITEST_WORKERS)\n'; \
		printf 'erun-console-typecheck\terun-console typecheck\tcd erun-console && yarn typecheck\n'; \
		printf 'erun-console-lint\terun-console lint\tcd erun-console && yarn lint -- --cache --cache-strategy content --cache-location $(FRONTEND_LINT_CACHE_DIR)/eslint/erun-console/\n'; \
		printf 'erun-console-format\terun-console format:check\tcd erun-console && yarn format:check -- --cache --cache-strategy content --cache-location $(FRONTEND_LINT_CACHE_DIR)/prettier/erun-console.json\n'; \
		printf 'erun-console-build\terun-console build\tcd erun-console && yarn build\n'; \
		printf 'erun-console-test\terun-console test\tcd erun-console && $(CURDIR)/scripts/vitest-gate.sh --maxWorkers=$(FRONTEND_VITEST_WORKERS)\n' \
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
# This is a real check-gate prerequisite, not a manual coverage attestation.
# Keep worker fixtures isolated and validate repeated-run determinism when
# changing their lifecycle; see erun-ui/playwright/AGENTS.md.
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
# ...and after test-erun-ui-windows-build, which embeds erun-ui/frontend/dist
# while this target's own build.sh rewrites it. Both already depend on
# test-frontend, so both start only once dist exists -- but that orders their
# *starts*, not their access to the directory, and `vite build` empties dist
# before repopulating it. The embed then reads it mid-rewrite:
#
#   assets_production.go:10:12: pattern all:frontend/dist:
#     cannot embed directory frontend/dist: contains no embeddable files
#
# It went unnoticed while build.sh spent ~2 minutes on gates before its vite
# build; dropping those (erun#2375) moved the rewrite early enough to collide.
# The cross-compile is ~4s, so sequencing it first costs nothing and removes
# the overlap outright rather than making it less likely.
test-playwright: test-erun-ui-windows-build test-frontend
	@echo ">> erun-ui/playwright suite (desktop tags)"
	@(cd erun-ui/playwright && ./run.sh --skip-app-gates)

# A plain local `make check`/`make test-playwright` never goes through `erun
# build`'s own resolution above, so PLAYWRIGHT_TEST_AREAS stayed unset here
# and this target always ran the full suite (~21-23 minutes) while the gate
# that actually protects `main` ran in tens of seconds -- backwards, since a
# local run is supposed to be a cheaper preview of the same gate, not a
# stricter one. Resolve the identical selection here too, via `erun exec
# resolve-playwright-areas` (a thin CLI wrapper around the same
# erun-common.ResolvePlaywrightTestAreaSelection function `erun build` calls
# above), so a developer or agent iterating locally pays the same cost the
# gate does. This is a target-specific variable using `?=`, so it is only
# evaluated when the caller has not already supplied PLAYWRIGHT_TEST_AREAS --
# the Dockerfile test stage's own build-arg thread, including its
# empty-string "run everything" default, is left untouched. `export` (no
# value) marks the variable for export to a recipe's environment whenever it
# does get a value, from either source. Declared after the recipe, not beside
# it: the coverage gate in erun-integration reads a target's recipe from its
# first definition line, so that line has to stay adjacent to the recipe.
test-playwright: PLAYWRIGHT_TEST_AREAS ?= $(shell cd erun-cli && go run . exec resolve-playwright-areas 2>/dev/null)

# The same resolution on `check`, which needs its own copy rather than
# inheriting test-playwright's: Make gives a target-specific variable to the
# target that declares it and to the chain of prerequisites *below* it, and
# `check` sits above test-playwright rather than below it. What reached
# `check` instead was the bare `export` on the next line -- an empty but
# *defined* PLAYWRIGHT_TEST_AREAS, and "defined" is the operative word.
# `check`'s recipe is the boundary where the job's environment is captured
# (scripts/agent-gate.sh hands it to `erun exec job start`), so inside that job
# test-playwright's own `?=` read the empty value as "the caller already
# supplied this" and never resolved, and run.sh read it as "no selection":
# `make check` in an agent pod ran the full suite while `erun exec
# resolve-playwright-areas` on the same clean tree printed `smoke`, with
# nothing in the gate's output saying which of the two it had used. Resolving
# at the boundary makes the selection that crosses it the one the tree
# resolved. `?=` still preserves a value the caller supplied (the Dockerfile
# build-arg thread), and an unresolvable tree still resolves to "all" through
# the CLI's own fail-safe. Both declarations sit above the `export` because
# the bare `export` defines the variable, and a target-specific `?=` parsed
# after that definition is skipped -- the ordering is load-bearing, not
# stylistic. Declared after their recipes, not beside them: the coverage gate
# in erun-integration reads a target's recipe from its first definition line.
check: PLAYWRIGHT_TEST_AREAS ?= $(shell cd erun-cli && go run . exec resolve-playwright-areas 2>/dev/null)
export PLAYWRIGHT_TEST_AREAS

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
# Reserves room for lint/test-frontend under check-gate's own concurrent `-j`
# fan-out -- see CHECK_GATE_FANOUT_PEAK_MEMORY_MIB's own comment above.
HELM_CHART_TEST_PARALLELISM ?= $(shell ./scripts/parallel-gate.sh width $(words $(wildcard erun-devops/k8s/*_test.sh)) $(HELM_CHART_TEST_JOB_MEMORY_MIB) $(CHECK_GATE_FANOUT_PEAK_MEMORY_MIB))

# Helm-render assertions for the erun-devops/k8s charts (erun-devops,
# erun-backend-postgres, erun-backend-db, erun-backend-api, erun-oci-registry,
# erun-zitadel, erun-console, erun-docs): each *_test.sh renders its chart with
# `helm template` and asserts on the output. image-version_test.sh sits here
# too, asserting chart image defaults against the docker/<image>/VERSION pins
# they mirror. No cluster, no docker -- pure rendering -- so a pinned `helm`
# binary is all the image test stage needs to run these (see the Dockerfile's
# test stage). Iterates the directory rather
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

# Every published Terraform module's own behaviour suite, run with
# `terraform test` against mocked providers -- no cluster, no cloud account, no
# state. erun-devops/terraform-erun/modules/*/tests pin invariants the modules
# are consumed for: edge_transport_policy.tftest.hcl holds the plaintext->https
# redirect and the HSTS Middleware, and the refusal to accept a transport
# policy for an ingress controller the module is not installing -- a refusal
# that is the difference between a plan failing loudly and every public host
# serving cleartext. Nothing in this repository ran any of them before this
# target existed, so a module edit that dropped one applied cleanly with every
# suite green beside it, which is the shape this closes (see helm-chart-tests
# above and test-atlas-validate below for the same job on the charts and the
# migration checksums).
#
# The loop lives in scripts/terraform-module-tests.sh rather than in this
# recipe for two reasons: it discovers modules by scanning for suites (so a new
# module is gated with no Makefile edit), and naming a script here brings the
# tree it resolves under the copy-contract guard in erun-common -- that guard
# reads the scripts check-gate's targets run for `${script_dir}/../...` roots
# and fails when the erun-devops image test stage COPYs nothing that provides
# them, which is the difference between a gate that runs here and one that
# cannot run inside any image build. `terraform` itself is installed there too;
# no binary the gate needs is visible to that guard, so the Dockerfile's own
# install is what has to stay in step.
#
# Each module's `terraform init` is retried a bounded number of times on a
# transient registry failure, because that one network step has reddened
# branches that changed no terraform file; see the header of
# scripts/terraform-module-tests.sh, and its self-test in fast-check. The
# suites themselves are not retried -- they run against mocked providers, so a
# retry there could only hide a real assertion failure.
terraform-module-tests:
	sh scripts/terraform-module-tests.sh

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

# Proof that erun-backend-db's baked migration directory is internally
# consistent -- every migrations/default/*.sql file hashes to the atlas.sum
# entry recorded for it -- checked purely against the files on disk, no
# postgres and no docker. Unlike test-schema-drift/test-postgres-restart/
# test-retention* above, this needs only the `atlas` CLI, which the
# erun-devops image test stage already installs (for erun-integration's
# gate-merge scenarios) and the final runtime image installs too, so it runs
# inside `make check` itself rather than needing a separate by-hand/job
# invocation. This is the release gate that was missing when v1.0.247
# shipped with `20260902130000_gate_runs.sql`'s atlas.sum entry not matching
# its own file content (the migration was edited after `atlas migrate hash`
# was run for it, and the mismatch landed on main undetected through a
# squash-merge), and nothing validated the baked migration directory before
# that image was built and published. `atlas migrate validate` reports
# exactly the "checksum mismatch" atlas reports at deploy time, before an
# image is ever built.
test-atlas-validate:
	sh erun-devops/docker/erun-backend-db/atlas_validate_test.sh

# End-to-end proof that the console's nginx config (default.conf.template)
# never resolves a missing content-hashed asset or a health/version request to
# the SPA shell (erun#2064). Same "needs a real docker daemon" exclusion from
# make check as the tests above -- it needs to observe actual nginx
# location/try_files behavior. Run this by hand, or via `erun exec job` in an
# agent env, before merging a change to erun-devops/docker/erun-console/.
test-console-nginx:
	sh erun-devops/docker/erun-console/nginx_test.sh

# The opt-in ERUN_E2E_* database suites of erun-backend-api, against a real
# postgres and the real migrations -- the venue that decides whether the
# `Regression-Test:` cases those suites carry are actually run. The gate proper
# is the erun-backend-api image's own `test` stage, which runs this same script
# inside `erun build`/`erun build --gate` and fails the build when it fails;
# that is the venue a pull request is graded by, and it is why this target is
# not in `make check`.
#
# Same "needs a real docker daemon and the atlas CLI" exclusion from make check
# as test-postgres-restart/test-retention above -- and, unlike those, the
# erun-backend-api image's own test stage carries both, which is what lets it
# run this script in-build. Kept as a target as well so an agent or human can
# reproduce a gate failure without a full image build. Run it before merging a
# change to erun-backend-api's database-backed behavior.
test-erun-backend-api-e2e:
	sh erun-devops/docker/erun-backend-api/e2e_gate_test.sh

# Build, run, and coverage-gate the erun integration suite.
# The coverage threshold defaults to the value pinned in
# erun-integration/scripts/integration-test.sh; override with
# COVERAGE_THRESHOLD=NN. To refresh testdata files in place, run the script
# directly with --update-golden (./erun-integration/scripts/integration-test.sh
# --update-golden) — gate mode refuses outright if UPDATE_GOLDEN is set in the
# environment, so it cannot be reseeded via `make check UPDATE_GOLDEN=1`.
#
# Detaches through the same wrapper as `check` below: this standalone gate is long
# enough on its own to hit the same foreground-timeout failure inside an
# agent pod. check-gate depends on integration-test-gate directly rather than
# on this target, so a `make check` run never nests one detached job inside
# another.
integration-test:
	./scripts/agent-gate.sh integration-test "make integration-test" -- $(MAKE) integration-test-gate

integration-test-gate:
	GO_TEST_GOMAXPROCS=$(GO_TEST_GOMAXPROCS) INTEGRATION_TEST_TIMEOUT=$(INTEGRATION_TEST_TIMEOUT) ./erun-integration/scripts/integration-test.sh

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
# check-gate's own thirteen prerequisites (below) used to run back-to-back: on a
# real release, the first seven alone (everything before test-playwright)
# cost ~14.5 minutes, and test-playwright is the single largest of the thirteen by
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
# fixed-batch model would be for thirteen wildly uneven-duration jobs.
# CHECK_GATE_PARALLELISM deliberately passes no mem-per-job-mib: unlike
# lint/test-frontend/helm-chart-tests (each a uniform fan-out of near-
# identical jobs with a real measured per-job cost), these thirteen targets are
# wildly heterogeneous -- some are flat single processes, three are
# themselves internally parallel fan-outs, and none has a comparable
# measured per-job memory figure, so a number here would be fabricated
# rather than measured (the same "measure, don't fabricate a slope" standard
# HELM_CHART_TEST_JOB_MEMORY_MIB's own comment holds to). CPU/job-count alone
# deciding the width matches that target's own precedent for the identical
# reason. What this width does NOT bound: three of these thirteen
# (lint/test-frontend/helm-chart-tests) each already run their own internal
# fan-out sized against the full memory ceiling -- CHECK_GATE_FANOUT_PEAK_MEMORY_MIB
# (see lint's own comment above) is what stops those three from
# double-booking memory against *each other* when `-j` runs them side by
# side. It does not bound the other ten (in particular test-erun-ui's
# race-enabled test process) against any of the
# thirteen running concurrently -- verify actual peak memory on a real
# `make check-gate` run before trusting this width in a memory-constrained
# environment, and narrow it with real numbers if that run shows a problem.
#
# This is the count of check-gate's own prerequisite targets above, and it is
# kept equal to it by erun-integration/check_gate_target_count_test.go rather
# than by hand: it is the term that keeps `-j` from opening more slots than
# there are jobs to fill them, so a target added without bumping it queues
# behind a free slot instead of taking one. It went stale exactly that way --
# two targets joined the list while this still read 10, which resolved -j10
# for twelve targets because the CPU term (the in-pod DIND_CPU_LIMIT of 12)
# was the larger one. Derive it, do not re-count it by eye.
CHECK_GATE_TARGET_COUNT := 13
CHECK_GATE_PARALLELISM ?= $(shell ./scripts/parallel-gate.sh width $(CHECK_GATE_TARGET_COUNT) "")

check:
	@echo ">> concurrent-phase-spans: check-gate runs $(CHECK_GATE_TARGET_COUNT) targets at -j$(CHECK_GATE_PARALLELISM)"
	@./scripts/agent-gate.sh check "make check" -- $(MAKE) -j$(CHECK_GATE_PARALLELISM) check-gate; \
	status=$$?; \
	if [ $$status -eq 124 ]; then \
		echo "make check: INCONCLUSIVE -- the gate is still running and reached no verdict." >&2; \
		echo "make check: that is not a failure and says nothing about the change. GNU Make collapses every nonzero recipe exit to 2, so this exit status alone cannot tell you so; re-run 'make check' to re-attach to the same job and keep waiting." >&2; \
	fi; \
	exit $$status

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
# Desktop Playwright runs here with the actual build toolchain. Do not remove
# it to bypass failures; diagnose against comparable state and fix them under
# root Working Rules. Fixture-isolation requirements live in the Playwright guide.
#
# These thirteen run concurrently, bounded by CHECK_GATE_PARALLELISM (see
# `check`'s own comment above for the measured cost this replaced, why `-j`
# rather than scripts/parallel-gate.sh is what drives it here, and where the
# two real ordering dependencies -- test-playwright and
# test-erun-ui-windows-build each needing test-frontend -- are declared).
# Do not drop any of the thirteen from this line to move the fan-out elsewhere:
# erun-integration/build_check_coverage_test.go and
# erun_ui_windows_cross_compile_test.go both parse this exact line's text to
# confirm every module's tests are really wired into `make check`, and fail
# if any of these names is missing from it.
# The prerequisite ORDER on this line is load-bearing when the resolved fan-out
# width is narrower than the target list, not cosmetic: `make -j` dispatches
# prerequisites in the order listed, filling each free slot with the next one,
# so a target listed late cannot start until enough earlier targets have
# finished. `test-frontend` heads the single longest chain in the gate --
# test-frontend -> {test-playwright, test-erun-ui-windows-build}, where
# test-playwright then builds the wailsjs bindings and the desktop erun-app
# before any spec can run -- so while it sat seventh it took a slot only after
# the six lint/module targets ahead of it began to drain, and at the reference
# 4-CPU build container those are the longest jobs in the gate. Listing the
# critical-path targets first lets the chain head take a slot in the first
# dispatch batch. While the width covered every target this was a no-op (the
# in-pod gate resolved -j12 and dispatched all twelve within 0.32s); with a
# thirteenth target the list is one longer than that CPU-quota term, so the
# last-listed one now takes the first slot to free rather than one in the first
# dispatch batch. That wait is seconds, not minutes -- several of the targets
# ahead of it finish in well under a minute -- but it is why a short addition
# belongs after the long jobs rather than ahead of them.
# Reordering this line is safe (nothing keys on the order); DROPPING a name is
# not -- see the coverage-test note directly above.
check-gate: test-frontend test-playwright test-erun-ui-windows-build lint test-erun-common test-erun-ui test-erun-backend-api test-erun-mcp test-erun-dns01-webhook helm-chart-tests terraform-module-tests test-atlas-validate integration-test-gate

# A fast, local subset of check-gate for the cheap-and-common failures that
# don't need a full check-gate cycle to find: golangci-lint findings, the
# tracker-reference gate (root AGENTS.md § "Code Comments"), the
# regression-coverage gate (root AGENTS.md § "A Defect Fix Names Its
# Reproduction"), the pre-commit hook's own regression test, the
# terraform-module-tests init-retry self-test, and prettier
# formatting. This is NOT a substitute for
# check/check-gate -- it runs no
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
# The regression-coverage gate is the one step here that has no check-gate
# home to be scoped down from: it reads this branch's own commits and diff,
# and check-gate runs inside a Docker build context with no `.git`. fast-check
# is where it belongs anyway -- root AGENTS.md requires fast-check before
# every push, which is exactly the moment a defect fix either does or does not
# name the case that reproduces the failure it was filed for. Its own
# self-test runs immediately before it (and again inside check-gate, via
# test-frontend) so a broken classifier fails loudly rather than waving every
# change through.
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
	@yarn install --frozen-lockfile --prefer-offline --network-timeout 600000
	@echo ">> issue-reference gate (TypeScript: erun-kit, erun-ui/frontend, erun-console)"
	@node --test scripts/check-issue-references.test.mjs
	@node scripts/check-issue-references.mjs erun-kit/src erun-ui/frontend/src erun-console/src
	@echo ">> regression-coverage gate (this branch)"
	@node --test scripts/check-regression-coverage.test.mjs
	@node scripts/check-regression-coverage.mjs
	@echo ">> pre-commit hook (bindings regenerated before the frontend lint)"
	@sh scripts/pre-commit_test.sh
	@echo ">> terraform-module-tests init retry (transient retried, bounded, permanent fails fast)"
	@sh scripts/terraform-module-tests_test.sh
	@echo ">> prettier --check (erun-kit, erun-ui/frontend, erun-console)"
	@for d in erun-kit erun-ui/frontend erun-console; do \
		printf '%s\t%s\t%s\n' "$$d" "prettier $$d" "cd $$d && yarn format:check"; \
	done | ./scripts/parallel-gate.sh 3 fast-check-prettier
