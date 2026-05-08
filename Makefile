.PHONY: integration-test

# Build, run, and coverage-gate the erun integration suite.
# Threshold defaults to 90 percent; override with COVERAGE_THRESHOLD=NN.
# Use UPDATE_GOLDEN=1 to refresh testdata files in place.
integration-test:
	./erun-integration/scripts/integration-test.sh
