package eruncommon

import (
	"strings"
	"testing"
)

// TestReadAttachedAppSessionsIncludesKubectlStderr is erun#1768's pattern
// applied to the stop path's attached-session probe: kubectl's stderr is
// already captured onto exec.ExitError.Stderr by Output(), and
// traceAttachedAppSessions folds the returned error's message straight into
// an operator-visible trace line, so dropping the stderr here means that
// trace line never carries it either.
func TestReadAttachedAppSessionsIncludesKubectlStderr(t *testing.T) {
	writeKubectlStub(t, `Error from server (NotFound): deployments.apps "acme-prod" not found`, 1)
	_, err := readAttachedAppSessions(testTraceContext(false), RuntimeScaleTarget{Tenant: "acme", Environment: "prod", ReleaseName: "acme-prod"})
	if err == nil {
		t.Fatal("expected an error for a failing kubectl invocation")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error must include kubectl's own stderr, got: %v", err)
	}
}
