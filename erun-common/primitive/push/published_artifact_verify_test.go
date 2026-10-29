package push

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/helm"
)

// Each classifier marker needs a row here; under-matching aborts a release.
type registryReadSample struct {
	output string
	source string
}

// finalRegistryReadSamples say nothing about propagation and must not retry.
func finalRegistryReadSamples() []registryReadSample {
	return []registryReadSample{
		{
			output: `Error: cannot load Chart.yaml: error converting YAML to JSON: yaml: line 3: mapping values are not allowed in this context`,
			source: "constructed: a malformed chart, the final shape named when this coverage was requested",
		},
		{
			output: `Error: file "erun-runtime-1.0.248.tgz" does not match the expected digest`,
			source: "constructed: a checksum failure, the other final shape named when this coverage was requested",
		},
		{
			output: `Error: chart metadata is invalid: name is required`,
			source: "constructed: a chart the registry will never serve, however long it propagates",
		},
		{
			output: `Error: unknown flag: --version`,
			source: "constructed: erun's own invocation is wrong, so no registry answer is involved",
		},
		{
			output: `Error: execution error at (erun-runtime/templates/deployment.yaml:12:14): value for key "image" is required`,
			source: "constructed: a template error raised while rendering the fetched chart",
		},
		{
			output: ``,
			source: "constructed: a silent nonzero exit leaves no text to classify, so the read cannot be assumed transient",
		},
	}
}

func TestIsTransientRegistryReadErrorTreatsFinalFailuresAsFinal(t *testing.T) {
	for _, sample := range finalRegistryReadSamples() {
		if helm.IsTransientRegistryReadError(sample.output) {
			t.Errorf("output %q (%s) classified as transient; retrying it only delays the same verdict", sample.output, sample.source)
		}
	}
}

func captureRegistryReadLog() (core.Context, *bytes.Buffer) {
	log := &bytes.Buffer{}
	return core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, log, log)}, log
}

const (
	finalReadBackOutput = `Error: cannot load Chart.yaml: error converting YAML to JSON: yaml: line 3: mapping values are not allowed in this context`
	readBackSubject     = "chart erun-runtime 1.0.248"
)

func TestReadBackPublishedArtifactDoesNotRetryAFinalFailure(t *testing.T) {
	ctx, log := captureRegistryReadLog()
	lastErr := errors.New("exit status 1")
	attempts := 0

	err := helm.ReadBackPublishedArtifact(ctx, readBackSubject, func() (string, error) {
		attempts++
		return finalReadBackOutput, lastErr
	})

	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1: a non-transient failure must not be retried at all", attempts)
	}
	if !errors.Is(err, lastErr) {
		t.Fatalf("err = %v, want the read's own error", err)
	}
	if strings.Contains(log.String(), "retrying") {
		t.Errorf("a final failure announced a retry:\n%s", log.String())
	}
}
