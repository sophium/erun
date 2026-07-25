package eruncommon

import "testing"

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
