package providerlifecycle

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// deleteCloudflareToken keeps the alias but drops its credential, like an SSO logout.
func deleteCloudflareToken(ctx core.Context, provider model.CloudProviderConfig, deps cloud.CloudDependencies) error {
	if provider.Cloudflare == nil || strings.TrimSpace(provider.Cloudflare.TokenRef) == "" {
		return nil
	}
	ctx.Trace("delete cloudflare api token at ref " + provider.Cloudflare.TokenRef)
	if ctx.DryRun {
		return nil
	}
	if deps.CloudSecretStore == nil {
		return fmt.Errorf("cloud secret store is not configured")
	}
	return deps.CloudSecretStore.DeleteCloudSecret(provider.Cloudflare.TokenRef)
}
