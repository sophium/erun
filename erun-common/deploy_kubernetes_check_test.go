package eruncommon

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// writeKubectlStub points ERUN_KUBECTL_BIN at a script that prints stderr and
// exits non-zero (or exits 0 with no output when stderr is empty), so a test
// can drive checkKubernetesDeploymentWithContext's classification without a
// real cluster.
func writeKubectlStub(t *testing.T, stderr string, exitCode int) {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/kubectl-stub"
	script := "#!/bin/sh\n"
	if stderr != "" {
		script += "cat <<'EOF' >&2\n" + stderr + "\nEOF\n"
	}
	script += "exit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("ERUN_KUBECTL_BIN", path)
}

// TestCheckKubernetesDeploymentWithContextAbsent locks the one negative
// answer the check may give without an error: a genuine kubectl NotFound.
func TestCheckKubernetesDeploymentWithContextAbsent(t *testing.T) {
	writeKubectlStub(t, `Error from server (NotFound): deployments.apps "frs-devops" not found`, 1)
	deployed, _, err := checkKubernetesDeploymentWithContext(testTraceContext(false), KubernetesDeploymentCheckParams{Name: "frs-devops"}, "")
	if err != nil {
		t.Fatalf("unexpected error for a genuinely absent deployment: %v", err)
	}
	if deployed {
		t.Fatalf("deployed = true, want false for an absent deployment")
	}
}

// TestCheckKubernetesDeploymentWithContextPresent locks the healthy case: no
// error, deployed.
func TestCheckKubernetesDeploymentWithContextPresent(t *testing.T) {
	writeKubectlStub(t, "", 0)
	deployed, _, err := checkKubernetesDeploymentWithContext(testTraceContext(false), KubernetesDeploymentCheckParams{Name: "frs-devops"}, "")
	if err != nil {
		t.Fatalf("unexpected error for a running deployment: %v", err)
	}
	if !deployed {
		t.Fatalf("deployed = false, want true when kubectl reports the deployment present")
	}
}

// TestCheckKubernetesDeploymentWithContextMissingContext locks the fix at the
// The heart of the defect: a deployment that is running but whose kube context could
// not be resolved must not be reported as absent, and the returned error
// must name the context problem, reusing isKubernetesContextMissingMessage.
func TestCheckKubernetesDeploymentWithContextMissingContext(t *testing.T) {
	writeKubectlStub(t, `error: context "frs-prod" does not exist`, 1)
	deployed, _, err := checkKubernetesDeploymentWithContext(testTraceContext(false), KubernetesDeploymentCheckParams{Name: "frs-devops"}, "frs-prod")
	if deployed {
		t.Fatalf("deployed = true, want false when the check could not resolve an answer")
	}
	if err == nil {
		t.Fatalf("expected an error naming the missing context, got nil")
	}
	if strings.Contains(strings.ToLower(err.Error()), "not deployed") {
		t.Fatalf("a missing-context failure must never read as absence, got: %v", err)
	}
	if !strings.Contains(err.Error(), "context") {
		t.Fatalf("error must name the missing-context cause, got: %v", err)
	}
	if strings.Count(err.Error(), "exit status") > 1 {
		t.Fatalf("exit status must not be doubled, got: %v", err)
	}
}

// TestCheckKubernetesDeploymentWithContextUnreachableAPIServer locks the
// "could not ask" family: erun never got a response at all.
func TestCheckKubernetesDeploymentWithContextUnreachableAPIServer(t *testing.T) {
	writeKubectlStub(t, `Unable to connect to the server: dial tcp 10.0.0.1:443: i/o timeout`, 1)
	deployed, _, err := checkKubernetesDeploymentWithContext(testTraceContext(false), KubernetesDeploymentCheckParams{Name: "frs-devops"}, "")
	if deployed {
		t.Fatalf("deployed = true, want false when the api server is unreachable")
	}
	if err == nil {
		t.Fatalf("expected an error naming the unreachable cluster, got nil")
	}
	if strings.Contains(strings.ToLower(err.Error()), "not deployed") {
		t.Fatalf("an unreachable-cluster failure must never read as absence, got: %v", err)
	}
	if !strings.Contains(err.Error(), "could not be reached") {
		t.Fatalf("error must name the unreachable-cluster cause, got: %v", err)
	}
	if !strings.Contains(err.Error(), "i/o timeout") {
		t.Fatalf("error must carry kubectl's own output, got: %v", err)
	}
	if strings.Count(err.Error(), "exit status") > 1 {
		t.Fatalf("exit status must not be doubled, got: %v", err)
	}
}

// TestCheckKubernetesDeploymentWithContextRBACDenied locks the other "could
// not ask" family member named by the issue: a request the cluster refused.
func TestCheckKubernetesDeploymentWithContextRBACDenied(t *testing.T) {
	writeKubectlStub(t, `Error from server (Forbidden): deployments.apps "frs-devops" is forbidden: User "jane" cannot get resource "deployments" in API group "apps"`, 1)
	deployed, _, err := checkKubernetesDeploymentWithContext(testTraceContext(false), KubernetesDeploymentCheckParams{Name: "frs-devops"}, "")
	if deployed {
		t.Fatalf("deployed = true, want false when RBAC refuses the request")
	}
	if err == nil {
		t.Fatalf("expected an error naming the RBAC refusal, got nil")
	}
	if strings.Contains(strings.ToLower(err.Error()), "not deployed") {
		t.Fatalf("an RBAC-denied failure must never read as absence, got: %v", err)
	}
	if !strings.Contains(err.Error(), "credentials or permissions") {
		t.Fatalf("error must name the credentials/permissions cause, got: %v", err)
	}
}

// TestCheckKubernetesDeploymentWithContextUnclassified locks the closing
// The closing principle: an error erun does not recognize must still read as
// unknown, carrying the raw kubectl output, and never as absence.
func TestCheckKubernetesDeploymentWithContextUnclassified(t *testing.T) {
	writeKubectlStub(t, `Error from server: etcdserver: request timed out`, 1)
	deployed, _, err := checkKubernetesDeploymentWithContext(testTraceContext(false), KubernetesDeploymentCheckParams{Name: "frs-devops"}, "")
	if deployed {
		t.Fatalf("deployed = true, want false for an unrecognized kubectl error")
	}
	if err == nil {
		t.Fatalf("expected an error for the unrecognized failure, got nil")
	}
	if strings.Contains(strings.ToLower(err.Error()), "not deployed") {
		t.Fatalf("an unclassified failure must never read as absence, got: %v", err)
	}
	if !strings.Contains(err.Error(), "does not recognize") {
		t.Fatalf("error must admit it does not recognize the cause, got: %v", err)
	}
	if !strings.Contains(err.Error(), "etcdserver: request timed out") {
		t.Fatalf("error must carry kubectl's own output, got: %v", err)
	}
}
