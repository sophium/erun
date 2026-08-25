.PHONY: integration-test lint test-erun-ui check

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
# or erun-common misses erun-ui for the same reason. The frontend
# (`yarn typecheck && yarn lint && yarn test`) still runs only in
# erun-ui/build.sh: it needs node/yarn, which the test stage does not carry,
# and the Playwright suite (needs a built app) stays out of the per-commit
# gate entirely, run on its own schedule instead.
#
# erun-backend-db has no Go module at all (Atlas migrations + SQL only), so
# there is nothing here for golangci-lint to run against.
LINT_MODULES := erun-common erun-cli erun-mcp erun-integration erun-backend/erun-backend-api erun-ui

# Run golangci-lint across the gated modules, each against its own
# .golangci.yml (erun-integration has none, so it uses the default linters).
# Stops at the first module with findings so the build fails with a clear,
# scoped error. golangci-lint must be on PATH (the image test stage installs
# it; locally, install the pinned version or run `make integration-test`).
lint:
	@for m in $(LINT_MODULES); do \
		echo ">> golangci-lint $$m"; \
		(cd $$m && golangci-lint run ./...) || exit 1; \
	done

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
test-erun-ui:
	@echo ">> go test erun-ui"
	@(cd erun-ui && go test -count=1 ./...)

# Build, run, and coverage-gate the erun integration suite.
# The coverage threshold defaults to the value pinned in
# erun-integration/scripts/integration-test.sh; override with
# COVERAGE_THRESHOLD=NN. To refresh testdata files in place, run the script
# directly with --update-golden (./erun-integration/scripts/integration-test.sh
# --update-golden) — gate mode refuses outright if UPDATE_GOLDEN is set in the
# environment, so it cannot be reseeded via `make check UPDATE_GOLDEN=1`.
integration-test:
	./erun-integration/scripts/integration-test.sh

# The full in-build gate: golangci-lint, erun-ui's own Go tests, then the
# integration suite + coverage. The erun-devops image test stage runs this;
# a failure tags no image.
check: lint test-erun-ui integration-test
