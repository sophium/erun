package build

import (
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/primitive/shared"
)

// newFakeClock reuses the fakeClock defined in deploy_pod_watch_test.go —
// timing is awkward to test deterministically, so both suites control
// elapsed time exactly instead of sleeping.
func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func TestIncrementalCacheDecisionMatchesTraceReasons(t *testing.T) {
	cases := []struct {
		name       string
		build      shared.DockerBuildSpec
		applicable bool
		hit        bool
		reason     string
	}{
		{
			name:       "no fingerprint computed",
			build:      shared.DockerBuildSpec{},
			applicable: false,
		},
		{
			name:       "promoted from cache",
			build:      shared.DockerBuildSpec{Fingerprint: "abc", Promote: true},
			applicable: true,
			hit:        true,
		},
		{
			name:       "cascade rebuild",
			build:      shared.DockerBuildSpec{Fingerprint: "abc", CascadeRebuildFromTag: "erun-devops:1.0.0"},
			applicable: true,
			hit:        false,
			reason:     "dependency erun-devops:1.0.0 is rebuilding",
		},
		{
			name:       "missing platforms",
			build:      shared.DockerBuildSpec{Fingerprint: "abc", MissingFingerprintPlatforms: []string{"linux/amd64"}},
			applicable: true,
			hit:        false,
			reason:     "fingerprint image is missing for platform linux/amd64",
		},
		{
			name:       "no cached image at all",
			build:      shared.DockerBuildSpec{Fingerprint: "abc"},
			applicable: true,
			hit:        false,
			reason:     "no cached fingerprint image",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hit, applicable, reason := incrementalCacheDecision(tc.build)
			if applicable != tc.applicable {
				t.Fatalf("applicable = %v, want %v", applicable, tc.applicable)
			}
			if !applicable {
				return
			}
			if hit != tc.hit {
				t.Fatalf("hit = %v, want %v", hit, tc.hit)
			}
			if reason != tc.reason {
				t.Fatalf("reason = %q, want %q", reason, tc.reason)
			}
		})
	}
}
