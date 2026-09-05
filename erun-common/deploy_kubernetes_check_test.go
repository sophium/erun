package eruncommon

import (
	"os"
	"regexp"
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

// TestCheckKubernetesDeploymentWithContextSanitizesKlogFrame is erun#1766: the
// operator-facing message must carry the cause klog's "Unhandled Error" line
// names (the unreachable address) without the frame around it (severity,
// timestamp, goroutine id, Go source location) that survives a fixed-width
// toast while the address gets truncated away.
func TestCheckKubernetesDeploymentWithContextSanitizesKlogFrame(t *testing.T) {
	klogLine := `E0831 13:46:43.793908   10289 memcache.go:265] "Unhandled Error" err="couldn't get current server API group list: Get \"https://127.0.0.1:6443/api?timeout=32s\": dial tcp 127.0.0.1:6443: connect: connection refused"`
	writeKubectlStub(t, klogLine, 1)
	deployed, _, err := checkKubernetesDeploymentWithContext(testTraceContext(false), KubernetesDeploymentCheckParams{Name: "petios-devops"}, "orbstack")
	if deployed {
		t.Fatalf("deployed = true, want false when the api server is unreachable")
	}
	if err == nil {
		t.Fatalf("expected an error naming the unreachable cluster, got nil")
	}
	message := err.Error()
	if strings.Contains(message, "memcache.go") {
		t.Fatalf("error must not carry klog's Go source location, got: %v", message)
	}
	if strings.Contains(message, "13:46:43.793908") {
		t.Fatalf("error must not carry klog's timestamp, got: %v", message)
	}
	if regexp.MustCompile(`\b10289\b`).MatchString(message) {
		t.Fatalf("error must not carry klog's goroutine id, got: %v", message)
	}
	if !strings.Contains(message, "127.0.0.1:6443") {
		t.Fatalf("error must keep the address that names the unreachable target, got: %v", message)
	}
	if !strings.Contains(message, "connection refused") {
		t.Fatalf("error must keep the cause sentence, got: %v", message)
	}
	if !strings.Contains(message, `context "orbstack"`) {
		t.Fatalf("error must name the kube context from erun's own configuration, got: %v", message)
	}
}

// TestCheckKubernetesDeploymentWithContextReportsExitStatusPlainlyWhenKubectlWroteNothing
// covers the empty-output case: a failure with nothing captured must still
// name the exit status rather than an empty sanitized fragment.
func TestCheckKubernetesDeploymentWithContextReportsExitStatusPlainlyWhenKubectlWroteNothing(t *testing.T) {
	writeKubectlStub(t, "", 1)
	_, _, err := checkKubernetesDeploymentWithContext(testTraceContext(false), KubernetesDeploymentCheckParams{Name: "frs-devops"}, "")
	if err == nil {
		t.Fatalf("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("error must still name the exit status when kubectl wrote nothing, got: %v", err.Error())
	}
}

// TestSanitizeKubectlFailureOutputStripsKlogFrame locks the helper itself
// against the two shapes it must handle: an "Unhandled Error" carrier (unwrap
// to the err= sentence) and a plain klog line with no such carrier (frame
// stripped, rest kept verbatim).
func TestSanitizeKubectlFailureOutputStripsKlogFrame(t *testing.T) {
	got := sanitizeKubectlFailureOutput(`E0831 13:46:43.793908   10289 memcache.go:265] "Unhandled Error" err="dial tcp 127.0.0.1:6443: connect: connection refused"`)
	want := "dial tcp 127.0.0.1:6443: connect: connection refused"
	if got != want {
		t.Fatalf("sanitizeKubectlFailureOutput() = %q, want %q", got, want)
	}
}

func TestSanitizeKubectlFailureOutputStripsFrameWithNoErrCarrier(t *testing.T) {
	got := sanitizeKubectlFailureOutput(`W0831 09:00:00.000000       1 warnings.go:70] some deprecation notice`)
	want := "some deprecation notice"
	if got != want {
		t.Fatalf("sanitizeKubectlFailureOutput() = %q, want %q", got, want)
	}
}

func TestSanitizeKubectlFailureOutputReturnsEmptyForBlankInput(t *testing.T) {
	if got := sanitizeKubectlFailureOutput("   \n\n  "); got != "" {
		t.Fatalf("sanitizeKubectlFailureOutput() = %q, want empty", got)
	}
}

// TestDeploymentMatchesExpectedSettingsIncludesKubectlOutput is erun#1768's
// pattern applied to the settings-match inspection: kubectl's combined output
// is already captured, and the caller used to discard it entirely in favor of
// the content-free "exit status N" a bare "%w" wrap renders.
func TestDeploymentMatchesExpectedSettingsIncludesKubectlOutput(t *testing.T) {
	writeKubectlStub(t, `Error from server (NotFound): deployments.apps "frs-devops" not found`, 1)
	_, err := deploymentMatchesExpectedSettings(testTraceContext(false), KubernetesDeploymentCheckParams{Name: "frs-devops"})
	if err == nil {
		t.Fatal("expected an error for a failing kubectl invocation")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error must include kubectl's own output, got: %v", err)
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
