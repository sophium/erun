package envdelete

import (
	"context"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-mcp/tools/shared"
)

// assertNamesMissingAndRecovery insists a target-resolution error names the
// missing subject and a concrete recovery ("pass ... explicitly"), not just
// that an error occurred -- a bare "tenant and environment are required"
// would pass a plain err != nil check but is exactly the dead end this
// guards against.
func assertNamesMissingAndRecovery(t *testing.T, err error, wantSubject string) {
	t.Helper()
	if err.Error() == "tenant and environment are required" || err.Error() == "tenant is required" || err.Error() == "environment is required" {
		t.Fatalf("error regressed to a bare required-input message with no recovery: %v", err)
	}
	if !strings.Contains(err.Error(), wantSubject) {
		t.Errorf("error should name %q, got: %v", wantSubject, err)
	}
	if !strings.Contains(err.Error(), "pass") {
		t.Errorf("error should tell the caller to pass the missing value explicitly, got: %v", err)
	}
}

// delete's own tenant/environment resolution (scopedTenantEnv, not
// resolveLocalTarget) hit the identical bare "tenant and environment are
// required" dead end when neither the server nor the caller named a target.
// It carries the same fix and deserves the same regression coverage.
func TestDeleteRefusesWithNamedRecoveryWhenNeitherSideNamesATarget(t *testing.T) {
	runtime := shared.RuntimeConfig{}
	_, _, err := DeleteTool(runtime)(context.Background(), nil, DeleteInput{Preview: true})
	if err == nil {
		t.Fatal("expected an error when neither the server nor the caller names a target")
	}
	assertNamesMissingAndRecovery(t, err, "tenant/environment")
}
