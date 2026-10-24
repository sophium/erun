package jobs

import (
	"errors"
	"io"
	"testing"

	"github.com/sophium/erun/erun-mcp/tools/shared/jobenvelope"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

// A synchronous call has no job and no log, and already returns everything
// inline, so it must not be handed a nil writer the work would panic on.
func TestJobEnvelopeGivesASynchronousCallADiscardLog(t *testing.T) {
	runtime := runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "acme", Environment: "dev"}}

	envelope, err := jobenvelope.RunJobEnvelope(runtime, "build", jobenvelope.JobEnvelopeInput{}, false, func(_ bool, log io.Writer) (toolio.CommandOutput, error) {
		if log == nil {
			return toolio.CommandOutput{}, errors.New("synchronous execute was handed a nil log writer")
		}
		_, writeErr := io.WriteString(log, "ignored")
		return toolio.CommandOutput{Executed: true}, writeErr
	})
	if err != nil {
		t.Fatalf("runJobEnvelope: %v", err)
	}
	if !envelope.Wait || envelope.JobID != "" {
		t.Fatalf("envelope = %+v, want the synchronous shape", envelope)
	}
}
