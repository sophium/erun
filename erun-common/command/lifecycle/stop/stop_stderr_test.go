package stop

import (
	"strings"
	"testing"
)

// The probe's error feeds an operator trace, so it must carry kubectl's stderr.
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
