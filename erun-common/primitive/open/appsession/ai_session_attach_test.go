package appsession

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
)

// A close with no exit status must read as unknown, never a guessed outcome.
func TestResolveAISessionAttachOutcome(t *testing.T) {
	cases := []struct {
		name          string
		exitCode      int
		exitCodeKnown bool
		want          model.AISessionAttachOutcome
	}{
		{"taken over", RemoteShellTakenOverExitCode, true, model.AISessionAttachOutcomeTakenOver},
		{"deploy reattach", RemoteShellReattachDeployExitCode, true, model.AISessionAttachOutcomeDeployReattach},
		{"plain exit", 0, true, model.AISessionAttachOutcomeEnded},
		{"nonzero unrelated exit", 1, true, model.AISessionAttachOutcomeEnded},
		{"no exit status observed", 0, false, model.AISessionAttachOutcomeUnknown},
		{"no exit status observed, nonzero code ignored", RemoteShellTakenOverExitCode, false, model.AISessionAttachOutcomeUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveAISessionAttachOutcome(tc.exitCode, tc.exitCodeKnown)
			if got != tc.want {
				t.Fatalf("ResolveAISessionAttachOutcome(%d, %v) = %q, want %q", tc.exitCode, tc.exitCodeKnown, got, tc.want)
			}
		})
	}
}
