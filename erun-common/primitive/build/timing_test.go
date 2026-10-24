package build

import (
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func TestIncrementalCacheDecisionMatchesTraceReasons(t *testing.T) {
	cases := []struct {
		name       string
		build      dockerspec.DockerBuildSpec
		applicable bool
		hit        bool
		reason     string
	}{
		{
			name:       "no fingerprint computed",
			build:      dockerspec.DockerBuildSpec{},
			applicable: false,
		},
		{
			name:       "cascade rebuild",
			build:      dockerspec.DockerBuildSpec{Fingerprint: "abc", CascadeRebuildFromTag: "erun-devops:1.0.0"},
			applicable: true,
			hit:        false,
			reason:     "dependency erun-devops:1.0.0 is rebuilding",
		},
		{
			name:       "missing platforms",
			build:      dockerspec.DockerBuildSpec{Fingerprint: "abc", MissingFingerprintPlatforms: []string{"linux/amd64"}},
			applicable: true,
			hit:        false,
			reason:     "fingerprint image is missing for platform linux/amd64",
		},
		{
			name:       "no cached image at all",
			build:      dockerspec.DockerBuildSpec{Fingerprint: "abc"},
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
