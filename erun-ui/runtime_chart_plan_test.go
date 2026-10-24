package main

import (
	"context"
	"errors"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/registry"
)

func TestResolveRuntimeChartPlan(t *testing.T) {
	const snapshot = "9.9.9-snapshot-20260101010101"
	envs := map[string]config.EnvConfig{
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
					tenants: map[string]config.TenantConfig{"acme": {Name: "acme"}},
					envs:    envs,
				},
				resolveImageRegistry: func(_ context.Context, _, repository string) (registry.RuntimeRegistryVersions, error) {
					if testCase.listErr != nil {
						return registry.RuntimeRegistryVersions{}, testCase.listErr
					}
					return registry.RuntimeRegistryVersions{Tags: testCase.tags[repository]}, nil
				},
			})
			got := app.resolveRuntimeChartPlan("acme", testCase.environment, snapshot, testCase.localChart)
			if got != testCase.want {
				t.Fatalf("plan mismatch\n got: %+v\nwant: %+v", got, testCase.want)
			}
		})
	}
}
