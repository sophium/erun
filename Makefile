.PHONY: integration-test lint check

# Go modules linted by the in-build gate — every Go module the erun-devops
# build image can compile. erun-ui is intentionally excluded: it needs the
# CGO/webkit + frontend toolchain the build image does not carry, so its
# golangci-lint gate lives in erun-ui/build.sh instead (and the .githooks
# pre-commit hook lints it locally, where that toolchain is present).
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
