package config

import (
	"testing"
)

func TestExecutionModeForUnrecognizedValueFallsBackToSubprocess(t *testing.T) {
	config := ERunConfig{Execution: ExecutionConfig{Modes: map[string]string{"aws-sts": "banana"}}}
	if mode := ExecutionModeFor(config, "aws-sts"); mode != ExecutionModeSubprocess {
		t.Fatalf("mode = %q, want %q", mode, ExecutionModeSubprocess)
	}
}

func TestExecutionModeForIsCaseInsensitive(t *testing.T) {
	config := ERunConfig{Execution: ExecutionConfig{Modes: map[string]string{"aws-sts": "LIBRARY"}}}
	if mode := ExecutionModeFor(config, "aws-sts"); mode != ExecutionModeLibrary {
		t.Fatalf("mode = %q, want %q", mode, ExecutionModeLibrary)
	}
}
