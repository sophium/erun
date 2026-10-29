package cloudcontext

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
)

// ResolveEnvironmentAWSRegion returns "" when nothing resolves; callers must then omit
// AWS_REGION, since an empty value overrides the pod profile's region.
func ResolveEnvironmentAWSRegion(store config.CloudReadStore, env config.EnvConfig) string {
	if region := cloudContextRegionFromName(env.KubernetesContext); region != "" {
		return region
	}
	alias := strings.TrimSpace(env.CloudProviderAlias)
	if alias != "" {
		if provider, err := providerconfig.ResolveCloudProvider(store, alias); err == nil {
			if region := strings.TrimSpace(provider.SSORegion); region != "" {
				return region
			}
		}
	}
	return awsRegionFromContainerRegistries(env.ContainerRegistries)
}

func awsRegionFromContainerRegistries(registries config.ContainerRegistries) string {
	for _, registry := range registries.DistinctRegistries() {
		if region := awsRegionFromECRHost(registry); region != "" {
			return region
		}
	}
	return ""
}

func awsRegionFromECRHost(reference string) string {
	host := strings.TrimSpace(reference)
	if idx := strings.Index(host, "/"); idx >= 0 {
		host = host[:idx]
	}
	const marker = ".dkr.ecr."
	start := strings.Index(host, marker)
	if start < 0 {
		return ""
	}
	region, suffix, ok := strings.Cut(host[start+len(marker):], ".")
	if !ok || !strings.HasPrefix(suffix, "amazonaws.com") {
		return ""
	}
	return strings.TrimSpace(region)
}
