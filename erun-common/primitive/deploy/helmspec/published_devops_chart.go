package helmspec

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
)

// resolveRuntimeRegistry is projected as RUNTIME_REGISTRY, with the same precedence
// as publishedDevopsChartRegistry.
func resolveRuntimeRegistry(target config.OpenResult) string {
	if r := strings.TrimSpace(target.EnvConfig.RuntimeRegistry); r != "" {
		return r
	}
	if IsClusterRegistryEnv(target) {
		return config.RuntimeImageRegistry(target.EnvConfig.RuntimeImage)
	}
	return ""
}

// IsClusterRegistryEnv matches both an unresolved cluster: entry and one already
// concretized onto ClusterPullRegistry.
func IsClusterRegistryEnv(target config.OpenResult) bool {
	return target.EnvConfig.ContainerRegistries.HasClusterEntry() ||
		strings.TrimSpace(target.ClusterPullRegistry) != ""
}
