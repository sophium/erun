.PHONY: integration-test lint test-erun-ui test-frontend helm-chart-tests test-postgres-restart check check-gate

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
LINT_MODULES := erun-common erun-cli erun-mcp erun-integration erun-backend/erun-backend-api erun-ui

# Run golangci-lint across the gated modules, each against its own
# .golangci.yml (erun-integration has none, so it uses the default linters).
# Runs every module even when an earlier one has findings, then fails once at
# the end, so one red module never hides the rest -- reporting a subset of
# findings as if it were the whole answer is worse than reporting nothing.
# golangci-lint must be on PATH (the image test stage installs it; locally,
# install the version pinned in the repo-root GOLANGCI_LINT_VERSION file), and
# it must actually be that pinned version: a newer install's vendored
# analyzers can flag things the pinned one does not (and vice versa), so
# `make lint` and the image build would otherwise silently disagree.
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
	@failed=""; \
	for m in $(LINT_MODULES); do \
		echo ">> golangci-lint $$m"; \
		if ! (cd $$m && golangci-lint run ./...); then \
			failed="$$failed $$m"; \
		fi; \
	done; \
	if [ -n "$$failed" ]; then \
		echo "lint failed in:$$failed" >&2; \
		exit 1; \
	fi

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
test-frontend:
	@echo ">> yarn install (root workspace: erun-kit, erun-console, erun-ui/frontend)"
	@yarn install --frozen-lockfile
	@echo ">> erun-kit gates"
	@(cd erun-kit && yarn typecheck && yarn lint && yarn format:check && yarn build)
	@echo ">> erun-console gates"
	@(cd erun-console && yarn typecheck && yarn lint && yarn format:check && yarn build && yarn test)

# Helm-render assertions for the erun-devops/k8s charts (erun-devops,
# erun-backend-postgres, erun-backend-db, erun-backend-api, erun-oci-registry,
# erun-zitadel, erun-console, erun-docs): each *_test.sh renders its chart with
# `helm template` and asserts on the output. No cluster, no docker -- pure
# rendering -- so a pinned `helm` binary is all the image test stage needs to
# run these (see the Dockerfile's test stage). Iterates the directory rather
# than naming each script so a new chart's *_test.sh is picked up with no
# Makefile edit.
helm-chart-tests:
	@for t in erun-devops/k8s/*_test.sh; do \
		echo ">> $$t"; \
		sh "$$t" || exit 1; \
	done

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
integration-test:
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
check-gate: lint test-erun-ui test-frontend helm-chart-tests integration-test
