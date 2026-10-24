package jobs

import (
	"errors"
	"io"
	"testing"

	sharedpkg "github.com/sophium/erun/erun-mcp/tools/shared"
)

// A synchronous call has no job and no log, and already returns everything
// inline, so it must not be handed a nil writer the work would panic on.
func TestJobEnvelopeGivesASynchronousCallADiscardLog(t *testing.T) {
	runtime := sharedpkg.RuntimeConfig{Context: sharedpkg.RuntimeContext{Tenant: "acme", Environment: "dev"}}

	envelope, err := sharedpkg.RunJobEnvelope(runtime, "build", sharedpkg.JobEnvelopeInput{}, false, func(_ bool, log io.Writer) (sharedpkg.CommandOutput, error) {
		if log == nil {
			return sharedpkg.CommandOutput{}, errors.New("synchronous execute was handed a nil log writer")
		}
		_, writeErr := io.WriteString(log, "ignored")
		return sharedpkg.CommandOutput{Executed: true}, writeErr
	})
	if err != nil {
		t.Fatalf("runJobEnvelope: %v", err)
	}
	if !envelope.Wait || envelope.JobID != "" {
		t.Fatalf("envelope = %+v, want the synchronous shape", envelope)
	}
}
