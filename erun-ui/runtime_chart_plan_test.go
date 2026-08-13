package main

import (
	"context"
	"errors"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// The Runtime tab has to answer "which chart would this version install?" before
// the operator commits to a rollout, and answer it the way the deploy itself
// resolves the chart: the env's stated chart, else the tenant's published umbrella,
// else the canonical chart. The case that matters most is the fourth -- a version
// naming an image on the project's own line has no chart at all, and saying so
// first is the difference between a named fix and a failed deploy.
func TestResolveRuntimeChartPlan(t *testing.T) {
	const snapshot = "9.9.9-snapshot-20260101010101"
	envs := map[string]eruncommon.EnvConfig{
		"acme/plain":  {Name: "plain", RuntimeVersion: snapshot},
		"acme/stated": {Name: "stated", RuntimeVersion: snapshot, RuntimeChart: "oci://ghcr.io/sophium/charts/erun-devops:1.0.178"},
	}

	for _, testCase := range []struct {
		name        string
		environment string
		tags        map[string][]string
		listErr     error
		localChart  bool
		want        uiRuntimeChartPlan
	}{
		{
			name:        "a stated chart is taken at its word, at its own version",
			environment: "stated",
			listErr:     errors.New("no registry should be read"),
			want: uiRuntimeChartPlan{
				Reference: "oci://ghcr.io/sophium/charts/erun-devops",
				Version:   "1.0.178",
				Chart:     "erun-devops",
				Source:    "stated",
			},
		},
		{
			name:        "the tenant's own umbrella wins when published at the version",
			environment: "plain",
			tags:        map[string][]string{"charts/acme-devops": {snapshot}},
			want: uiRuntimeChartPlan{
				Reference: "oci://ghcr.io/sophium/charts/acme-devops",
				Version:   snapshot,
				Chart:     "acme-devops",
				Source:    "tenant",
			},
		},
		{
			name:        "the canonical chart is the fallback",
			environment: "plain",
			tags:        map[string][]string{"charts/erun-devops": {snapshot}},
			want: uiRuntimeChartPlan{
				Reference: "oci://ghcr.io/sophium/charts/erun-devops",
				Version:   snapshot,
				Chart:     "erun-devops",
				Source:    "canonical",
			},
		},
		{
			name:        "neither chart at the version is a definite miss",
			environment: "plain",
			tags:        map[string][]string{"charts/erun-devops": {"1.0.178"}},
			want: uiRuntimeChartPlan{
				Reference: "oci://ghcr.io/sophium/charts/erun-devops",
				Version:   snapshot,
				Chart:     "erun-devops",
				Source:    "canonical",
				Missing:   true,
			},
		},
		{
			// A private or unreachable registry must not block a deploy that would
			// have worked: the operator is told nothing rather than told something
			// false.
			name:        "a registry that cannot be read is unknown, never missing",
			environment: "plain",
			listErr:     errors.New("unauthorized"),
			want: uiRuntimeChartPlan{
				Reference: "oci://ghcr.io/sophium/charts/erun-devops",
				Version:   snapshot,
				Chart:     "erun-devops",
				Source:    "canonical",
				Unknown:   true,
			},
		},
		{
			name:        "a repo-local runtime chart is nobody's registry business",
			environment: "plain",
			listErr:     errors.New("no registry should be read"),
			localChart:  true,
			want:        uiRuntimeChartPlan{Version: snapshot, Source: "local"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			app := NewApp(erunUIDeps{
				store: stubUIStore{
					tenants: map[string]eruncommon.TenantConfig{"acme": {Name: "acme"}},
					envs:    envs,
				},
				resolveImageRegistry: func(_ context.Context, _, repository string) (eruncommon.RuntimeRegistryVersions, error) {
					if testCase.listErr != nil {
						return eruncommon.RuntimeRegistryVersions{}, testCase.listErr
					}
					return eruncommon.RuntimeRegistryVersions{Tags: testCase.tags[repository]}, nil
				},
			})
			got := app.resolveRuntimeChartPlan("acme", testCase.environment, snapshot, testCase.localChart)
			if got != testCase.want {
				t.Fatalf("plan mismatch\n got: %+v\nwant: %+v", got, testCase.want)
			}
		})
	}
}
