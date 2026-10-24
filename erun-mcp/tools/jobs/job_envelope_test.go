package jobs

import (
	"errors"
	"io"
	"testing"

	"github.com/sophium/erun/erun-mcp/tools/shared"
)

// A synchronous call has no job and no log, and already returns everything
// inline, so it must not be handed a nil writer the work would panic on.
func TestJobEnvelopeGivesASynchronousCallADiscardLog(t *testing.T) {
	runtime := shared.RuntimeConfig{Context: shared.RuntimeContext{Tenant: "acme", Environment: "dev"}}

	envelope, err := shared.RunJobEnvelope(runtime, "build", shared.JobEnvelopeInput{}, false, func(_ bool, log io.Writer) (shared.CommandOutput, error) {
		if log == nil {
			return shared.CommandOutput{}, errors.New("synchronous execute was handed a nil log writer")
		}
		_, writeErr := io.WriteString(log, "ignored")
		return shared.CommandOutput{Executed: true}, writeErr
	})
	if err != nil {
		t.Fatalf("runJobEnvelope: %v", err)
	}
	if !envelope.Wait || envelope.JobID != "" {
		t.Fatalf("envelope = %+v, want the synchronous shape", envelope)
	}
}
