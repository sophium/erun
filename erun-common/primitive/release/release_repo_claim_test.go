package release

import (
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func TestReleaseClaimHolderDescriptionRendersSensiblyWithMissingFields(t *testing.T) {
	cases := []struct {
		name        string
		holder      activity.EnvironmentActivityLeaseHolder
		environment string
		want        string
	}{
		{"holder and environment", activity.EnvironmentActivityLeaseHolder{Tenant: "erun"}, "build", "tenant erun, environment build"},
		{"environment only, holder unnamed", activity.EnvironmentActivityLeaseHolder{}, "build", "environment build"},
		{"holder only, no environment", activity.EnvironmentActivityLeaseHolder{Tenant: "erun"}, "", "tenant erun"},
		{"neither", activity.EnvironmentActivityLeaseHolder{}, "", "an unnamed holder"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shared.ReleaseClaimHolderDescription(tc.holder, tc.environment); got != tc.want {
				t.Errorf("releaseClaimHolderDescription(%+v, %q) = %q, want %q", tc.holder, tc.environment, got, tc.want)
			}
		})
	}
}

// The claim ref is the only thing an operator can act on by hand.
func TestReleaseRepoClaimRefusalNamesTheDeliberateRemedy(t *testing.T) {
	start := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
	err := (&shared.ReleaseRepoClaimConflictError{
		Version:     "1.0.289",
		Holder:      activity.EnvironmentActivityLeaseHolder{Tenant: "erun"},
		Environment: "build",
		StartedAt:   start,
		ExpiresAt:   start.Add(shared.ReleaseVersionClaimTTL),
		Now:         start.Add(time.Minute),
	}).Error()

	for _, want := range []string{
		"running for 1m0s",
		"a holder in this environment is reclaimed automatically on the next attempt once its process is gone",
		"a holder in another environment once its claim lapses",
		"git push origin --delete refs/erun/release-claim/1.0.289",
	} {
		if !strings.Contains(err, want) {
			t.Errorf("refusal must contain %q, got: %s", want, err)
		}
	}
}
