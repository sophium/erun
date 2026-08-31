package eruncommon

import "testing"

// TestRuntimeImageLineMismatch pins the static half of erun#1754: comparing
// the operative RuntimeImage against the observed RuntimeRunningImage needs
// no live cluster read, because an image name always names its release line
// even though a bare version number cannot.
func TestRuntimeImageLineMismatch(t *testing.T) {
	cases := []struct {
		name         string
		env          EnvConfig
		wantRecorded string
		wantObserved string
		wantMismatch bool
	}{
		{
			name: "frs/build's drifted shape: recorded stock, observed the tenant's own line",
			env: EnvConfig{
				RuntimeImage:        "ghcr.io/sophium/erun-devops",
				RuntimeRunningImage: "ghcr.io/sophium/frs-devops:1.0.86",
			},
			wantRecorded: "erun",
			wantObserved: "frs",
			wantMismatch: true,
		},
		{
			name: "frs/local's consistent stock-on-erun's-line shape",
			env: EnvConfig{
				RuntimeImage:        "erun-devops",
				RuntimeRunningImage: "ghcr.io/sophium/erun-devops:1.0.203",
			},
			wantRecorded: "erun",
			wantObserved: "erun",
			wantMismatch: false,
		},
		{
			name: "frs/prod's consistent tenant-line shape",
			env: EnvConfig{
				RuntimeImage:        "frs-devops",
				RuntimeRunningImage: "ghcr.io/sophium/frs-devops:1.0.84",
			},
			wantRecorded: "frs",
			wantObserved: "frs",
			wantMismatch: false,
		},
		{
			name:         "no history recorded yet: never a mismatch",
			env:          EnvConfig{},
			wantRecorded: "",
			wantObserved: "",
			wantMismatch: false,
		},
		{
			name: "recorded set but never observed (no deploy has run since this fix landed)",
			env: EnvConfig{
				RuntimeImage: "frs-devops",
			},
			wantRecorded: "frs",
			wantObserved: "",
			wantMismatch: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorded, observed, mismatched := tc.env.RuntimeImageLineMismatch()
			if recorded != tc.wantRecorded || observed != tc.wantObserved || mismatched != tc.wantMismatch {
				t.Fatalf("RuntimeImageLineMismatch() = (%q, %q, %v), want (%q, %q, %v)",
					recorded, observed, mismatched, tc.wantRecorded, tc.wantObserved, tc.wantMismatch)
			}
		})
	}
}
