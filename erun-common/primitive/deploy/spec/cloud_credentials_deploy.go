package spec

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
)

func cloudflareSecretName(releaseName string) string {
	return strings.TrimSpace(releaseName) + "-cloudflare"
}

// applyCloudflareDeployMetadata fills Cloudflare deploy fields; the Secret is applied at execution.
func applyCloudflareDeployMetadata(store config.CloudReadStore, env config.EnvConfig, deployInput *helmdeploy.HelmDeploySpec) {
	if deployInput == nil {
		return
	}
	alias := strings.TrimSpace(env.ResolvedCloudAliases()[config.CloudProviderCloudflare])
	if alias == "" {
		return
	}
	provider, err := providerconfig.ResolveCloudProvider(store, alias)
	if err != nil || provider.Provider != config.CloudProviderCloudflare || provider.Cloudflare == nil {
		return
	}
	deployInput.CloudflareEnabled = true
	deployInput.CloudflareAccountID = provider.Cloudflare.AccountID
	deployInput.CloudflareTokenRef = provider.Cloudflare.TokenRef
	deployInput.CloudflareSecretName = cloudflareSecretName(deployInput.ReleaseName)
}
