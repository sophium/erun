package erunmcp

import (
	"strings"
	"testing"
)

// assertNamesMissingAndRecovery requires the error to name the missing subject and a recovery.
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
