package providertoken

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

func cloudflareCloudProviderTokenStatus(provider model.CloudProviderConfig, deps cloud.CloudDependencies) model.CloudProviderStatus {
	if provider.Cloudflare == nil || strings.TrimSpace(provider.Cloudflare.TokenRef) == "" {
		return model.CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusNotConfigured, Message: "cloudflare api token is not configured"}
	}
	if deps.CloudSecretStore == nil {
		return model.CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusUnknown, Message: "cloud secret store is not configured"}
	}
	token, err := deps.CloudSecretStore.LoadCloudSecret(provider.Cloudflare.TokenRef)
	if err != nil || strings.TrimSpace(token) == "" {
		return model.CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusNotConfigured, Message: "cloudflare api token is not available on this machine"}
	}
	if _, err := deps.VerifyCloudflareToken(core.StderrOnlyContext(), token); err != nil {
		return model.CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusExpired, Message: err.Error()}
	}
	return model.CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusActive}
}
