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

// TestPublishedDevopsChartRegistryPrefersRuntimeImage guards the fix for the
// `--cluster-registry` init failure: the published erun-devops runtime chart and
// its platform images live at the runtime image's registry (ghcr), never in the
// env's in-cluster deploy registry. When the two differ, the runtime image's
// registry must win so the chart resolves to where it was actually published.
func TestPublishedDevopsChartRegistryPrefersRuntimeImage(t *testing.T) {
	clusterDeploy := ContainerRegistries{
		{Registry: "10.43.0.100:5000", Roles: []RegistryRole{RegistryRoleBuild, RegistryRoleDeploy}},
	}

	t.Run("runtimeimage registry beats the cluster deploy registry", func(t *testing.T) {
		target := OpenResult{EnvConfig: EnvConfig{
			RuntimeImage:        "ghcr.io/sophium/erun-devops",
			ContainerRegistries: clusterDeploy,
		}}
		if got := publishedDevopsChartRegistry(target); got != "ghcr.io/sophium" {
			t.Fatalf("chart registry = %q, want ghcr.io/sophium (not the in-cluster deploy registry)", got)
		}
	})

	t.Run("explicit runtimeregistry still wins over everything", func(t *testing.T) {
		target := OpenResult{EnvConfig: EnvConfig{
			RuntimeRegistry:     "example.com/team",
			RuntimeImage:        "ghcr.io/sophium/erun-devops",
			ContainerRegistries: clusterDeploy,
		}}
		if got := publishedDevopsChartRegistry(target); got != "example.com/team" {
			t.Fatalf("chart registry = %q, want the explicit runtimeregistry example.com/team", got)
		}
	})

	t.Run("falls back to the deploy registry when the image carries no registry", func(t *testing.T) {
		target := OpenResult{EnvConfig: EnvConfig{
			RuntimeImage:        "erun-devops",
			ContainerRegistries: clusterDeploy,
		}}
		if got := publishedDevopsChartRegistry(target); got != "10.43.0.100:5000" {
			t.Fatalf("chart registry = %q, want the deploy registry 10.43.0.100:5000", got)
		}
	})
}
