package eruncommon

import "testing"

// TestRecommendedDeployRecovery locks the diagnosis → single-recovery mapping.
// The classifier only runs on a real `erun doctor` (dry-run produces an empty
// diagnosis because the helm/kubectl probes are traced, not executed), so it is
// unreachable from the dry-run integration subprocess and is covered here.
func TestRecommendedDeployRecovery(t *testing.T) {
	cases := []struct {
		name       string
		helmStatus string
		wantAction DeployRecoveryAction
		wantOK     bool
	}{
		{
			name:       "empty status offers nothing",
			helmStatus: "",
			wantOK:     false,
		},
		{
			name:       "release not found offers nothing",
			helmStatus: "Error: release: not found",
			wantOK:     false,
		},
		{
			name:       "pending-upgrade recommends clearing the lock",
			helmStatus: "NAME: team-devops\nSTATUS: pending-upgrade\nREVISION: 3",
			wantAction: DeployRecoveryClearPendingHelm,
			wantOK:     true,
		},
		{
			name:       "pending-install recommends clearing the lock",
			helmStatus: "STATUS: pending-install",
			wantAction: DeployRecoveryClearPendingHelm,
			wantOK:     true,
		},
		{
			name:       "deployed offers nothing",
			helmStatus: "NAME: team-devops\nSTATUS: deployed\nREVISION: 4",
			wantOK:     false,
		},
		{
			name:       "failed recommends rollback",
			helmStatus: "NAME: team-devops\nSTATUS: failed\nREVISION: 5",
			wantAction: DeployRecoveryRollback,
			wantOK:     true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			action, ok := RecommendedDeployRecovery(DeployDiagnosisResult{HelmStatus: tc.helmStatus})
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && action != tc.wantAction {
				t.Fatalf("action = %q, want %q", action, tc.wantAction)
			}
		})
	}
}
