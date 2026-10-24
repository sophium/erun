package deploy

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
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
		if got := config.RuntimeImageRegistry(in); got != want {
			t.Errorf("runtimeImageRegistry(%q) = %q, want %q", in, got, want)
		}
	}
}

// A --cluster-registry env's chart follows the runtime image registry; a plain env
// must not follow a differing runtime image.
func TestPublishedDevopsChartRegistry(t *testing.T) {
	clusterRegistries := config.ContainerRegistries{
		{Cluster: &config.ClusterRegistry{}, Roles: []config.RegistryRole{config.RegistryRoleBuild, config.RegistryRoleDeploy}},
	}

	t.Run("cluster-registry env resolves the chart from the runtime image registry", func(t *testing.T) {
		target := config.OpenResult{EnvConfig: config.EnvConfig{
			RuntimeImage:        "ghcr.io/sophium/erun-devops",
			ContainerRegistries: clusterRegistries,
		}}
		if got := publishedDevopsChartRegistry(target); got != "ghcr.io/sophium" {
			t.Fatalf("chart registry = %q, want ghcr.io/sophium (the runtime image's registry, not the in-cluster deploy registry)", got)
		}
	})

	t.Run("explicit runtimeregistry still wins over everything", func(t *testing.T) {
		target := config.OpenResult{EnvConfig: config.EnvConfig{
			RuntimeRegistry:     "example.com/team",
			RuntimeImage:        "ghcr.io/sophium/erun-devops",
			ContainerRegistries: clusterRegistries,
		}}
		if got := publishedDevopsChartRegistry(target); got != "example.com/team" {
			t.Fatalf("chart registry = %q, want the explicit runtimeregistry example.com/team", got)
		}
	})
}

func TestPublishedTenantComponentChartRegistry(t *testing.T) {
	t.Run("cluster-registry env with an explicit runtimeregistry still resolves components from the deploy registry", func(t *testing.T) {
		target := config.OpenResult{
			ClusterPullRegistry: "10.43.0.100:5000",
			EnvConfig: config.EnvConfig{
				RuntimeRegistry:     "ghcr.io/sophium",
				ContainerRegistries: config.ContainerRegistries{{Registry: "10.43.0.100:5000", Roles: []config.RegistryRole{config.RegistryRoleDeploy}}},
			},
		}
		if got := publishedTenantComponentChartRegistry(target); got != "10.43.0.100:5000" {
			t.Fatalf("component chart registry = %q, want the deploy registry 10.43.0.100:5000, not the platform runtimeregistry", got)
		}
		if got := publishedDevopsChartRegistry(target); got != "ghcr.io/sophium" {
			t.Fatalf("platform chart registry = %q, want the explicit runtimeregistry ghcr.io/sophium (unchanged)", got)
		}
	})

	t.Run("plain env resolves components from the same deploy registry as the platform chart", func(t *testing.T) {
		target := config.OpenResult{EnvConfig: config.EnvConfig{
			ContainerRegistries: config.ContainerRegistries{{Registry: "registry.example/test", Roles: []config.RegistryRole{config.RegistryRoleBuild, config.RegistryRoleDeploy}}},
		}}
		if got := publishedTenantComponentChartRegistry(target); got != "registry.example/test" {
			t.Fatalf("component chart registry = %q, want registry.example/test", got)
		}
	})
}

func TestChartRegistryInsecure(t *testing.T) {
	target := config.OpenResult{
		ClusterPullRegistry:     "10.43.0.100:5000",
		ClusterRegistryInsecure: true,
	}
	if !chartRegistryInsecure(target, "10.43.0.100:5000") {
		t.Fatal("the concretized insecure cluster registry must report insecure")
	}
	if chartRegistryInsecure(target, "ghcr.io/sophium") {
		t.Fatal("a different registry (the platform chart's) must never be reported insecure")
	}
	if chartRegistryInsecure(config.OpenResult{}, "") {
		t.Fatal("an empty registry must never be reported insecure")
	}
}

// A tenant's own devops image tag is on the tenant's line and must survive verbatim.
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
		registries  config.ContainerRegistries
		wantHonored bool
	}{
		{
			name: "deploy role held by the tenant's own registry",
			registries: config.ContainerRegistries{
				{Registry: recordedRegistry, Roles: []config.RegistryRole{config.RegistryRoleBuild, config.RegistryRoleDeploy}},
			},
			wantHonored: true,
		},
		{
			name: "deploy role held by the tenant's own registry, build elsewhere",
			registries: config.ContainerRegistries{
				{Registry: recordedRegistry, Roles: []config.RegistryRole{config.RegistryRoleDeploy}},
				{Registry: "ghcr.io/sophium", Roles: []config.RegistryRole{config.RegistryRoleBuild}},
			},
			wantHonored: true,
		},
		{
			name: "deploy role held by erun's own registry",
			registries: config.ContainerRegistries{
				{Registry: "ghcr.io/sophium", Roles: []config.RegistryRole{config.RegistryRoleDeploy}},
				{Registry: recordedRegistry, Roles: []config.RegistryRole{config.RegistryRoleBuild}},
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
			ctx := core.Context{Logger: core.NewLoggerWithWriters(0, &trace, &trace)}
			target := config.OpenResult{
				Tenant: tenant,
				EnvConfig: config.EnvConfig{
					RuntimeImage:        recordedImage,
					ContainerRegistries: tc.registries,
				},
			}

			got, persistImage := resolveDeployRuntimeImage(ctx, target, chartRegistry, erunVersion, config.DevopsComponentName, "", "", false)

			if tc.wantHonored {
				if got != recordedImage {
					t.Fatalf("resolveDeployRuntimeImage() = %q, want the recorded image %q honored verbatim", got, recordedImage)
				}
				if persistImage != recordedImage {
					t.Fatalf("persistImage = %q, want the recorded image %q unchanged (honoring an override is a no-op write)", persistImage, recordedImage)
				}
				if strings.Contains(trace.String(), "ignoring stale runtimeimage") {
					t.Fatalf("trace unexpectedly discarded the recorded image: %s", trace.String())
				}
			}
		})
	}
}

// Only the bare name is persisted, so a later registry change still resolves dynamically.
func TestResolveDeployRuntimeImagePersistNameForDefaultFallback(t *testing.T) {
	ctx := core.Context{}
	target := config.OpenResult{Tenant: "acme", EnvConfig: config.EnvConfig{}}

	image, persistImage := resolveDeployRuntimeImage(ctx, target, "ghcr.io/sophium", "1.0.201", "acme-devops", "1.0.201", "", false)

	const wantImage = "ghcr.io/sophium/acme-devops:1.0.201"
	if image != wantImage {
		t.Fatalf("resolveDeployRuntimeImage() image = %q, want %q", image, wantImage)
	}
	const wantPersist = "acme-devops"
	if persistImage != wantPersist {
		t.Fatalf("persistImage = %q, want the bare name %q (no registry, no tag)", persistImage, wantPersist)
	}
}

func TestResolveDeployRuntimeImagePersistNameEmptyForErunsOwnEnvironments(t *testing.T) {
	ctx := core.Context{}
	target := config.OpenResult{Tenant: "erun", EnvConfig: config.EnvConfig{}}

	_, persistImage := resolveDeployRuntimeImage(ctx, target, "ghcr.io/sophium", "1.0.201", config.DevopsComponentName, "1.0.201", "", false)

	if persistImage != "" {
		t.Fatalf("persistImage = %q, want empty for erun's own environments", persistImage)
	}
}
