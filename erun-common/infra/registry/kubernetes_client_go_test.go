package registry

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
)

// TestImagePullSecretGetArgsSharedByBothPaths locks the one argv builder
// applyImagePullSecrets traces and defaultExistingImagePullSecretAuths
// executes, so the dry-run trace can never drift from the subprocess
// execution path regardless of which execution mode is active.
func TestImagePullSecretGetArgsSharedByBothPaths(t *testing.T) {
	got := ImagePullSecretGetArgs("team-dev", "my-context", "regcred")
	want := []string{"--context", "my-context", "-n", "team-dev", "get", "secret", "regcred", "-o", "json"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
}

func TestExecutionModeForKubectlSecretGetDefaultsToSubprocess(t *testing.T) {
	if got := config.ExecutionModeFor(config.ERunConfig{}, kubectlSecretGetExecutionOperation); got != config.ExecutionModeSubprocess {
		t.Fatalf("mode = %q, want %q", got, config.ExecutionModeSubprocess)
	}
}

func TestExecutionModeReportListsKubectlSecretGetOperation(t *testing.T) {
	report := config.ExecutionModeReport(config.ERunConfig{})
	for _, status := range report {
		if status.Operation == kubectlSecretGetExecutionOperation {
			if status.Mode != config.ExecutionModeSubprocess {
				t.Fatalf("mode = %q, want %q", status.Mode, config.ExecutionModeSubprocess)
			}
			return
		}
	}
	t.Fatalf("kubectl-secret-get not found in report: %+v", report)
}
