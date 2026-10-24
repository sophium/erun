package doctor

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// The recovery prompt is skipped under --dry-run, so the integration goldens
// cannot reach this classifier.
func TestRecommendedDeployRecovery(t *testing.T) {
	cases := []struct {
		name          string
		helmStatus    string
		helmReadError string
		wantAction    deploy.DeployRecoveryAction
		wantOK        bool
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
			wantAction: deploy.DeployRecoveryClearPendingHelm,
			wantOK:     true,
		},
		{
			name:       "failed recommends rollback",
			helmStatus: "NAME: team-devops\nSTATUS: failed\nREVISION: 5",
			wantAction: deploy.DeployRecoveryRollback,
			wantOK:     true,
		},
		{
			// A failed read must not recommend a rollback.
			name:          "unreadable release offers nothing even though the status text looks like a failure",
			helmStatus:    "Error from server (Forbidden): secrets is forbidden: User \"jane\" cannot list resource \"secrets\"",
			helmReadError: `observe: not authorized to read helm release "team-devops" in namespace "team-dev"`,
			wantOK:        false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			action, ok := deploy.RecommendedDeployRecovery(deploy.DeployDiagnosisResult{HelmStatus: tc.helmStatus, HelmReadError: tc.helmReadError})
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && action != tc.wantAction {
				t.Fatalf("action = %q, want %q", action, tc.wantAction)
			}
		})
	}
}

// writeDoctorHelmStub points ERUN_HELM_BIN at a script with the given output and exit code.
func writeDoctorHelmStub(t *testing.T, stdout, stderr string, exitCode int) {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/helm-stub"
	script := "#!/bin/sh\n"
	if stdout != "" {
		script += "cat <<'ERUN_TEST_HELM_STDOUT'\n" + stdout + "\nERUN_TEST_HELM_STDOUT\n"
	}
	if stderr != "" {
		script += "cat <<'ERUN_TEST_HELM_STDERR' >&2\n" + stderr + "\nERUN_TEST_HELM_STDERR\n"
	}
	script += "exit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write helm stub: %v", err)
	}
	t.Setenv("ERUN_HELM_BIN", path)
}

// A failed helm read (RBAC denial) alone must block a destructive recovery.
func TestRunDeployDiagnosisRecordsHelmReadError(t *testing.T) {
	writeDoctorHelmStub(t, "", `Error from server (Forbidden): secrets is forbidden: User "jane" cannot list resource "secrets"`, 1)
	writeKubectlStub(t, "", 0)

	diagnosis := deploy.RunDeployDiagnosis(testTraceContext(false), config.ShellLaunchParams{Tenant: "team", Environment: "dev", Namespace: "team-dev"})

	if diagnosis.HelmReadError == "" {
		t.Fatalf("expected a non-empty HelmReadError for a failed RBAC read")
	}
	if !strings.Contains(diagnosis.HelmReadError, "not authorized") {
		t.Fatalf("HelmReadError must explain the RBAC cause, got %q", diagnosis.HelmReadError)
	}
	if action, ok := deploy.RecommendedDeployRecovery(diagnosis); ok {
		t.Fatalf("recommended action = %q, ok = true; want no recovery recommended for an unreadable release", action)
	}
}

// A missing release is a confirmed answer, not a failed read.
func TestRunDeployDiagnosisMissingReleaseHasNoReadError(t *testing.T) {
	writeDoctorHelmStub(t, "", "Error: release: not found", 1)
	writeKubectlStub(t, "", 0)

	diagnosis := deploy.RunDeployDiagnosis(testTraceContext(false), config.ShellLaunchParams{Tenant: "team", Environment: "dev", Namespace: "team-dev"})

	if diagnosis.HelmReadError != "" {
		t.Fatalf("expected no HelmReadError for a confirmed-missing release, got %q", diagnosis.HelmReadError)
	}
	if action, ok := deploy.RecommendedDeployRecovery(diagnosis); ok {
		t.Fatalf("recommended action = %q, ok = true; want no recovery recommended for a missing release", action)
	}
}

// A failed release still reads successfully and must still recommend rollback.
func TestRunDeployDiagnosisFailedReleaseStillRecommendsRollback(t *testing.T) {
	writeDoctorHelmStub(t, "NAME: team-devops\nSTATUS: failed\nREVISION: 5", "", 0)
	writeKubectlStub(t, "", 0)

	diagnosis := deploy.RunDeployDiagnosis(testTraceContext(false), config.ShellLaunchParams{Tenant: "team", Environment: "dev", Namespace: "team-dev"})

	if diagnosis.HelmReadError != "" {
		t.Fatalf("expected no HelmReadError for a genuinely failed release, got %q", diagnosis.HelmReadError)
	}
	action, ok := deploy.RecommendedDeployRecovery(diagnosis)
	if !ok || action != deploy.DeployRecoveryRollback {
		t.Fatalf("action = %q, ok = %v; want (%q, true) for a genuinely failed release", action, ok, deploy.DeployRecoveryRollback)
	}
}

// An RBAC failure does not mean the cluster is unreachable.
func TestRunDeployDiagnosisRunsPodsProbeWhenHelmReadFailsForOtherReasons(t *testing.T) {
	writeDoctorHelmStub(t, "", `Error from server (Forbidden): secrets is forbidden: User "jane" cannot list resource "secrets"`, 1)
	writeKubectlStub(t, "", 0)

	diagnosis := deploy.RunDeployDiagnosis(testTraceContext(false), config.ShellLaunchParams{Tenant: "team", Environment: "dev", Namespace: "team-dev"})

	if diagnosis.ClusterUnreachable {
		t.Fatalf("expected ClusterUnreachable = false for an RBAC read failure, got true")
	}
}

func TestRunDeployDiagnosisDetectsUnreachableFromPodsProbe(t *testing.T) {
	writeDoctorHelmStub(t, "NAME: team-devops\nSTATUS: deployed", "", 0)
	writeKubectlStub(t, "Unable to connect to the server: dial tcp 10.0.0.1:6443: i/o timeout", 1)

	diagnosis := deploy.RunDeployDiagnosis(testTraceContext(false), config.ShellLaunchParams{Tenant: "team", Environment: "dev", Namespace: "team-dev"})

	if !diagnosis.ClusterUnreachable {
		t.Fatalf("expected ClusterUnreachable = true when the pods probe hits an unreachable API server, got false")
	}
}
