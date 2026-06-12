.PHONY: integration-test

# Build, run, and coverage-gate the erun integration suite.
# The coverage threshold defaults to the value pinned in
# erun-integration/scripts/integration-test.sh; override with
# COVERAGE_THRESHOLD=NN. Use UPDATE_GOLDEN=1 to refresh testdata files in
# place.
integration-test:
	./erun-integration/scripts/integration-test.sh
