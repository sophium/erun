package published

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

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

func TestResolveDeployRuntimeImagePersistNameEmptyForErunsOwnEnvironments(t *testing.T) {
	ctx := core.Context{}
	target := config.OpenResult{Tenant: "erun", EnvConfig: config.EnvConfig{}}

	_, persistImage := resolveDeployRuntimeImage(ctx, target, "ghcr.io/sophium", "1.0.201", config.DevopsComponentName, "1.0.201", "", false)

	if persistImage != "" {
		t.Fatalf("persistImage = %q, want empty for erun's own environments", persistImage)
	}
}
