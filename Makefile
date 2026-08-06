.PHONY: integration-test lint check

# Go modules linted by the in-build gate: erun-cli, erun-common, erun-mcp,
# erun-integration, and erun-backend/erun-backend-api. Every entry must also be
# COPYd into the erun-devops image test stage's context
# (erun-devops/docker/erun-devops/Dockerfile) — a module the stage does not COPY
# makes `cd $m` fail and breaks every build (this happened with
# erun-backend-api, #599), so add the COPY in the same change as the entry.
# erun-ui and erun-backend-db are not gated here: erun-ui's lint lives in
# erun-ui/build.sh, and the .githooks pre-commit hook lints every module
# locally, where the full toolchain is present.
LINT_MODULES := erun-common erun-cli erun-mcp erun-integration erun-backend/erun-backend-api

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

# Build, run, and coverage-gate the erun integration suite.
# The coverage threshold defaults to the value pinned in
# erun-integration/scripts/integration-test.sh; override with
# COVERAGE_THRESHOLD=NN. Use UPDATE_GOLDEN=1 to refresh testdata files in
# place.
integration-test:
	./erun-integration/scripts/integration-test.sh

# The full in-build gate: golangci-lint, then the integration suite + coverage.
# The erun-devops image test stage runs this; a failure tags no image.
check: lint integration-test
