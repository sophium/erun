package eruncommon

import (
	"strings"
	"testing"
)

func TestRuntimeImageRegistry(t *testing.T) {
	cases := map[string]string{
		"ghcr.io/sophium/erun-devops":         "ghcr.io/sophium",
		"ghcr.io/sophium/erun-devops:1.0.149": "ghcr.io/sophium",
		"ghcr.io/erun-devops":                 "ghcr.io",
		"10.43.0.100:5000/charts/erun-devops": "10.43.0.100:5000/charts",
		"erun-devops":                         "",
		"":                                    "",
		"  ghcr.io/sophium/erun-devops  ":     "ghcr.io/sophium",
	}
	for in, want := range cases {
		if got := runtimeImageRegistry(in); got != want {
			t.Errorf("runtimeImageRegistry(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPublishedDevopsChartRegistry pins the split between the runtime CHART
// registry (where the erun platform chart is published) and the runtime IMAGE
// registry (an image-only override). A `--cluster-registry` env is the one case
// where the chart follows the runtime image's registry (ghcr), because its
// in-cluster deploy registry never holds the platform chart; a plain env keeps its
// chart on the deploy registry and must NOT follow a differing runtime image (the
// over-generalization this guards against).
func TestPublishedDevopsChartRegistry(t *testing.T) {
	// A cluster: entry as the init path passes it through, unconcretized.
	clusterRegistries := ContainerRegistries{
		{Cluster: &ClusterRegistry{}, Roles: []RegistryRole{RegistryRoleBuild, RegistryRoleDeploy}},
	}

	t.Run("cluster-registry env resolves the chart from the runtime image registry", func(t *testing.T) {
		target := OpenResult{EnvConfig: EnvConfig{
			RuntimeImage:        "ghcr.io/sophium/erun-devops",
			ContainerRegistries: clusterRegistries,
		}}
		if got := publishedDevopsChartRegistry(target); got != "ghcr.io/sophium" {
			t.Fatalf("chart registry = %q, want ghcr.io/sophium (the runtime image's registry, not the in-cluster deploy registry)", got)
		}
	})

	t.Run("concretized cluster-registry env resolves the chart from the runtime image registry", func(t *testing.T) {
		// The deploy path concretizes the cluster entry into its in-cluster pull host
		// on ClusterPullRegistry; the chart must still resolve from the runtime image.
		target := OpenResult{
			ClusterPullRegistry: "10.43.0.100:5000",
			EnvConfig: EnvConfig{
				RuntimeImage:        "ghcr.io/sophium/erun-devops",
				ContainerRegistries: ContainerRegistries{{Registry: "10.43.0.100:5000", Roles: []RegistryRole{RegistryRoleDeploy}}},
			},
		}
		if got := publishedDevopsChartRegistry(target); got != "ghcr.io/sophium" {
			t.Fatalf("chart registry = %q, want ghcr.io/sophium (the runtime image's registry, not the concretized in-cluster pull host)", got)
		}
	})

	t.Run("explicit runtimeregistry still wins over everything", func(t *testing.T) {
		target := OpenResult{EnvConfig: EnvConfig{
			RuntimeRegistry:     "example.com/team",
			RuntimeImage:        "ghcr.io/sophium/erun-devops",
			ContainerRegistries: clusterRegistries,
		}}
		if got := publishedDevopsChartRegistry(target); got != "example.com/team" {
			t.Fatalf("chart registry = %q, want the explicit runtimeregistry example.com/team", got)
		}
	})

	t.Run("plain env keeps the chart on the deploy registry despite a differing runtime image", func(t *testing.T) {
		// The regression 49f7f92f introduced: a plain env's chart followed the runtime
		// image's registry. The chart must stay on the deploy registry — the image
		// override is an image-only concern the chart does not inherit.
		target := OpenResult{EnvConfig: EnvConfig{
			RuntimeImage:        "ghcr.io/sophium/erun-devops",
			ContainerRegistries: ContainerRegistries{{Registry: "registry.example/test", Roles: []RegistryRole{RegistryRoleBuild, RegistryRoleDeploy}}},
		}}
		if got := publishedDevopsChartRegistry(target); got != "registry.example/test" {
			t.Fatalf("chart registry = %q, want the deploy registry registry.example/test (the chart must not follow the image registry)", got)
		}
	})
}

// TestResolveDeployRuntimeImageHonoursTenantsOwnVersionLine pins #1265: a
// tenant's own <tenant>-devops image is versioned on the tenant's own release
// line, independent of the erun version the environment runs (as erun pin
// already documents and enforces — it never rewrites this tag). A persisted
// runtimeimage naming that image at the tenant's own tag must survive a
// redeploy verbatim, never get discarded in favour of a guessed
// <tenant>-devops:<erun-version> tag just because the two happen to share a
// registry. Before the fix, whether the guess "won" depended on an accident —
// which registry held the deploy role — reproducing the four-environment
// matrix from the issue (same recorded image, only the registry wiring
// differs).
func TestResolveDeployRuntimeImageHonoursTenantsOwnVersionLine(t *testing.T) {
	const (
		tenant           = "petios"
		erunVersion      = "1.0.201"
		recordedRegistry = "<acct>.dkr.ecr.eu-west-2.amazonaws.com"
		recordedImage    = recordedRegistry + "/petios-devops:1.0.353-snapshot-20260824165146"
		chartRegistry    = "ghcr.io/sophium"
	)

	cases := []struct {
		name        string
		registries  ContainerRegistries
		wantHonored bool
	}{
		{
			name: "deploy role held by the tenant's own registry",
			registries: ContainerRegistries{
				{Registry: recordedRegistry, Roles: []RegistryRole{RegistryRoleBuild, RegistryRoleDeploy}},
			},
			wantHonored: true,
		},
		{
			name: "deploy role held by the tenant's own registry, build elsewhere",
			registries: ContainerRegistries{
				{Registry: recordedRegistry, Roles: []RegistryRole{RegistryRoleDeploy}},
				{Registry: "ghcr.io/sophium", Roles: []RegistryRole{RegistryRoleBuild}},
			},
			wantHonored: true,
		},
		{
			name: "deploy role held by erun's own registry",
			registries: ContainerRegistries{
				{Registry: "ghcr.io/sophium", Roles: []RegistryRole{RegistryRoleDeploy}},
				{Registry: recordedRegistry, Roles: []RegistryRole{RegistryRoleBuild}},
			},
			wantHonored: true,
		},
		{
			name:        "no registry recorded at all",
			registries:  nil,
			wantHonored: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var trace strings.Builder
			ctx := Context{Logger: NewLoggerWithWriters(0, &trace, &trace)}
			target := OpenResult{
				Tenant: tenant,
				EnvConfig: EnvConfig{
					RuntimeImage:        recordedImage,
					ContainerRegistries: tc.registries,
				},
			}

			got := resolveDeployRuntimeImage(ctx, target, chartRegistry, erunVersion, DevopsComponentName, "", "", false)

			if tc.wantHonored {
				if got != recordedImage {
					t.Fatalf("resolveDeployRuntimeImage() = %q, want the recorded image %q honored verbatim", got, recordedImage)
				}
				if strings.Contains(trace.String(), "ignoring stale runtimeimage") {
					t.Fatalf("trace unexpectedly discarded the recorded image: %s", trace.String())
				}
			}
		})
	}
}

// TestResolveDeployRuntimeImageStillHealsStockImageOnTenantLine guards the one
// staleness signal that remains legitimate after #1265: a runtimeimage still
// naming the stock erun-devops image on a deploy that has moved to the
// tenant's own umbrella chart is provably wrong (that line never publishes
// the stock image), so it is still healed to the umbrella's own image rather
// than kept.
func TestResolveDeployRuntimeImageStillHealsStockImageOnTenantLine(t *testing.T) {
	var trace strings.Builder
	ctx := Context{Logger: NewLoggerWithWriters(0, &trace, &trace)}
	target := OpenResult{
		Tenant: "acme",
		EnvConfig: EnvConfig{
			RuntimeImage: "ghcr.io/sophium/erun-devops:1.0.178",
		},
	}

	got := resolveDeployRuntimeImage(ctx, target, "ghcr.io/sophium", "1.0.201", "acme-devops", "1.0.201", "", false)

	const want = "ghcr.io/sophium/acme-devops:1.0.201"
	if got != want {
		t.Fatalf("resolveDeployRuntimeImage() = %q, want %q (healed off the stale stock image)", got, want)
	}
	if !strings.Contains(trace.String(), "ignoring stale runtimeimage") {
		t.Fatalf("expected a trace explaining the stock-image staleness, got: %s", trace.String())
	}
}
