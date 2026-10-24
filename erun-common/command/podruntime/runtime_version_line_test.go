package podruntime

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// An image name always names its release line; a bare version number cannot.
func TestRuntimeImageLineMismatch(t *testing.T) {
	cases := []struct {
		name         string
		env          config.EnvConfig
		wantRecorded string
		wantObserved string
		wantMismatch bool
	}{
		{
			name: "frs/build's drifted shape: recorded stock, observed the tenant's own line",
			env: config.EnvConfig{
				RuntimeImage:        "ghcr.io/sophium/erun-devops",
				RuntimeRunningImage: "ghcr.io/sophium/frs-devops:1.0.86",
			},
			wantRecorded: "erun",
			wantObserved: "frs",
			wantMismatch: true,
		},
		{
			name: "frs/local's consistent stock-on-erun's-line shape",
			env: config.EnvConfig{
				RuntimeImage:        "erun-devops",
				RuntimeRunningImage: "ghcr.io/sophium/erun-devops:1.0.203",
			},
			wantRecorded: "erun",
			wantObserved: "erun",
			wantMismatch: false,
		},
		{
			name: "frs/prod's consistent tenant-line shape",
			env: config.EnvConfig{
				RuntimeImage:        "frs-devops",
				RuntimeRunningImage: "ghcr.io/sophium/frs-devops:1.0.84",
			},
			wantRecorded: "frs",
			wantObserved: "frs",
			wantMismatch: false,
		},
		{
			name:         "no history recorded yet: never a mismatch",
			env:          config.EnvConfig{},
			wantRecorded: "",
			wantObserved: "",
			wantMismatch: false,
		},
		{
			name: "recorded set but never observed (no deploy has run since this fix landed)",
			env: config.EnvConfig{
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

func TestResolveErunVersion(t *testing.T) {
	cases := []struct {
		name        string
		env         config.EnvConfig
		runtimeLine *model.RuntimeVersionLine
		want        *model.ErunVersion
	}{
		{
			name: "never deployed: no runtime version to annotate",
			env:  config.EnvConfig{},
			want: nil,
		},
		{
			name:        "stock image confirmed on erun's own line, no chart override: coincides with runtime version",
			env:         config.EnvConfig{RuntimeVersion: "1.0.239"},
			runtimeLine: &model.RuntimeVersionLine{Line: "erun"},
			want:        &model.ErunVersion{Version: "1.0.239", SameAsRuntimeVersion: true},
		},
		{
			name:        "tenant's own line, chart explicitly states erun's version",
			env:         config.EnvConfig{RuntimeVersion: "1.0.356-snapshot-20260827091350", RuntimeChart: "oci://ghcr.io/sophium/erun-devops:1.0.239"},
			runtimeLine: &model.RuntimeVersionLine{Line: "petios"},
			want:        &model.ErunVersion{Version: "1.0.239"},
		},
		{
			name:        "no resolved image recorded, chart empty: never guess the pairing",
			env:         config.EnvConfig{RuntimeVersion: "1.0.356-snapshot-20260827091350"},
			runtimeLine: &model.RuntimeVersionLine{Undetermined: true},
			want:        nil,
		},
		{
			name: "runtime version set but the line was never resolved at all: never guess",
			env:  config.EnvConfig{RuntimeVersion: "1.0.239"},
			want: nil,
		},
		{
			name:        "chart stated with no version of its own: cannot read a number from config alone",
			env:         config.EnvConfig{RuntimeVersion: "1.0.356-snapshot-20260827091350", RuntimeChart: "oci://ghcr.io/sophium/erun-devops"},
			runtimeLine: &model.RuntimeVersionLine{Line: "petios"},
			want:        nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deploy.ResolveErunVersion(tc.env, tc.runtimeLine)
			if (got == nil) != (tc.want == nil) {
				t.Fatalf("ResolveErunVersion() = %+v, want %+v", got, tc.want)
			}
			if got != nil && *got != *tc.want {
				t.Fatalf("ResolveErunVersion() = %+v, want %+v", *got, *tc.want)
			}
		})
	}
}
