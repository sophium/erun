package eruncommon

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestRecommendedDeployRecovery locks the diagnosis → single-recovery mapping.
// The classifier only runs on a real `erun doctor` (dry-run produces an empty
// diagnosis because the helm/kubectl probes are traced, not executed), so it is
// unreachable from the dry-run integration subprocess and is covered here.
func TestRecommendedDeployRecovery(t *testing.T) {
	cases := []struct {
		name          string
		helmStatus    string
		helmReadError string
		wantAction    DeployRecoveryAction
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
		{
			// This is the defect erun#1659 exists to fix: a status string
			// that would otherwise fall into the "failed" default arm must
			// not recommend a rollback when the read itself is the thing
			// that failed, not the release.
			name:          "unreadable release offers nothing even though the status text looks like a failure",
			helmStatus:    "Error from server (Forbidden): secrets is forbidden: User \"jane\" cannot list resource \"secrets\"",
			helmReadError: `observe: not authorized to read helm release "team-devops" in namespace "team-dev"`,
			wantOK:        false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			action, ok := RecommendedDeployRecovery(DeployDiagnosisResult{HelmStatus: tc.helmStatus, HelmReadError: tc.helmReadError})
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && action != tc.wantAction {
				t.Fatalf("action = %q, want %q", action, tc.wantAction)
			}
		})
	}
}

// writeDoctorHelmStub points ERUN_HELM_BIN at a script that prints stdout and
// stderr and exits with the given code, so a test can drive RunDeployDiagnosis
// against the real, distinct shapes a live `helm status` invocation returns
// (plain text, not the `-o json` shape observe reads) without a live cluster.
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

// TestRunDeployDiagnosisRecordsHelmReadError is the end-to-end half of
// erun#1659's fix: a failed helm read (here, an RBAC denial reading the
// release Secrets) must populate HelmReadError, and that alone must be enough
// for RecommendedDeployRecovery to refuse a destructive recovery -- without
// this repo adding another strings.Contains arm for the RBAC wording.
func TestRunDeployDiagnosisRecordsHelmReadError(t *testing.T) {
	writeDoctorHelmStub(t, "", `Error from server (Forbidden): secrets is forbidden: User "jane" cannot list resource "secrets"`, 1)
	writeKubectlStub(t, "", 0)

	diagnosis := RunDeployDiagnosis(testTraceContext(false), ShellLaunchParams{Tenant: "team", Environment: "dev", Namespace: "team-dev"})

	if diagnosis.HelmReadError == "" {
		t.Fatalf("expected a non-empty HelmReadError for a failed RBAC read")
	}
	if !strings.Contains(diagnosis.HelmReadError, "not authorized") {
		t.Fatalf("HelmReadError must explain the RBAC cause, got %q", diagnosis.HelmReadError)
	}
	if action, ok := RecommendedDeployRecovery(diagnosis); ok {
		t.Fatalf("recommended action = %q, ok = true; want no recovery recommended for an unreadable release", action)
	}
}

// TestRunDeployDiagnosisMissingReleaseHasNoReadError locks the unchanged
// side: `helm status` also exits non-zero for a genuinely missing release,
// but that is a confirmed answer, not a failed read, so HelmReadError must
// stay empty.
func TestRunDeployDiagnosisMissingReleaseHasNoReadError(t *testing.T) {
	writeDoctorHelmStub(t, "", "Error: release: not found", 1)
	writeKubectlStub(t, "", 0)

	diagnosis := RunDeployDiagnosis(testTraceContext(false), ShellLaunchParams{Tenant: "team", Environment: "dev", Namespace: "team-dev"})

	if diagnosis.HelmReadError != "" {
		t.Fatalf("expected no HelmReadError for a confirmed-missing release, got %q", diagnosis.HelmReadError)
	}
	if action, ok := RecommendedDeployRecovery(diagnosis); ok {
		t.Fatalf("recommended action = %q, ok = true; want no recovery recommended for a missing release", action)
	}
}

// TestRunDeployDiagnosisFailedReleaseStillRecommendsRollback is the test that
// stops erun#1659's fix from regressing into "never recommend anything":
// `helm status` exits 0 for a release that exists but genuinely failed, so
// HelmReadError must stay empty and the rollback recommendation must survive.
func TestRunDeployDiagnosisFailedReleaseStillRecommendsRollback(t *testing.T) {
	writeDoctorHelmStub(t, "NAME: team-devops\nSTATUS: failed\nREVISION: 5", "", 0)
	writeKubectlStub(t, "", 0)

	diagnosis := RunDeployDiagnosis(testTraceContext(false), ShellLaunchParams{Tenant: "team", Environment: "dev", Namespace: "team-dev"})

	if diagnosis.HelmReadError != "" {
		t.Fatalf("expected no HelmReadError for a genuinely failed release, got %q", diagnosis.HelmReadError)
	}
	action, ok := RecommendedDeployRecovery(diagnosis)
	if !ok || action != DeployRecoveryRollback {
		t.Fatalf("action = %q, ok = %v; want (%q, true) for a genuinely failed release", action, ok, DeployRecoveryRollback)
	}
}
