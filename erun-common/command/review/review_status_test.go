package review

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// The refusal must happen before the platform call, so a mistyped filter never
// resolves an alias or reports a real-looking empty result.
func TestRunReviewListRefusesAnUnknownStatusBeforeAnyPlatformCall(t *testing.T) {
	_, err := RunReviewList(core.Context{}, nil, "", ReviewListParams{Status: "BOGUS"}, cloud.CloudDependencies{})
	if err == nil {
		t.Fatalf("RunReviewList with status %q succeeded, want a bad-argument error", "BOGUS")
	}
	if !strings.Contains(err.Error(), "unsupported review status") {
		t.Fatalf("RunReviewList error = %q, want the unsupported-status refusal", err)
	}
	// The alias lookup is what a mistyped filter used to reach; naming it here
	// pins that the refusal is a bad-argument error rather than a resolution
	// failure that happens to look like one.
	if strings.Contains(err.Error(), "alias") {
		t.Fatalf("RunReviewList error = %q, want the status refusal rather than an alias-resolution failure", err)
	}
}
