package cmd

import (
	"bytes"
	"testing"
)

func TestTraceLineDoesNotColorizeBufferedOutput(t *testing.T) {
	message := "[dry-run] docker build -t erunpaas/erun-devops:1.0.0"

	got := traceLine(new(bytes.Buffer), traceLineKindCommand, message)

	if got != message {
		t.Fatalf("expected uncolored trace line for buffered output, got %q", got)
	}
}
