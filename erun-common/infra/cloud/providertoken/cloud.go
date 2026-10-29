package providertoken

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/clouddefaults"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
)

// ErrCloudProviderNoOIDC marks a provider (Cloudflare) that has no OIDC issuer, so
// issuer aggregation skips it.
var ErrCloudProviderNoOIDC = errors.New("cloud provider does not support OIDC issuance")

type CloudBearerToken struct {
	Alias    string
	Provider string
	Token    string
	Issuer   string
}

func ListCloudProviderStatuses(store configpkg.CloudReadStore, deps cloud.CloudDependencies) ([]model.CloudProviderStatus, error) {
	providers, err := providerconfig.ListCloudProviders(store)
	if err != nil {
		return nil, err
	}
	statuses := make([]model.CloudProviderStatus, 0, len(providers))
	for _, provider := range providers {
		statuses = append(statuses, CloudProviderTokenStatus(provider, deps))
	}
	return statuses, nil
}

func CloudProviderTokenStatus(provider model.CloudProviderConfig, deps cloud.CloudDependencies) model.CloudProviderStatus {
	provider = providerconfig.NormalizeCloudProviderConfig(provider)
	deps = clouddefaults.NormalizeCloudDependencies(deps)
	if provider.Provider == "" {
		return model.CloudProviderStatus{CloudProviderConfig: provider, Status: configpkg.CloudTokenStatusNotConfigured, Message: "provider is not configured"}
	}
	if deps.CheckAWSStatus != nil && provider.Provider == configpkg.CloudProviderAWS {
		return deps.CheckAWSStatus(core.Context{}, provider)
	}
	if provider.Provider == configpkg.CloudProviderCloudflare {
		return cloudflareCloudProviderTokenStatus(provider, deps)
	}
	if provider.Provider == configpkg.CloudProviderERun {
		return erunCloudProviderTokenStatus(provider, deps)
	}
	if provider.Provider != configpkg.CloudProviderAWS {
		return model.CloudProviderStatus{CloudProviderConfig: provider, Status: configpkg.CloudTokenStatusUnknown, Message: "unsupported provider"}
	}
	return clouddefaults.DefaultCheckAWSStatus(core.Context{}, provider)
}

func issuerFromJWT(token string) (string, error) {
	token = strings.TrimSpace(token)
	parts := strings.SplitN(token, ".", 4)
	if len(parts) != 3 {
		return "", fmt.Errorf("cloud provider bearer token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode cloud provider bearer token payload: %w", err)
	}
	var claims struct {
		Issuer string `json:"iss"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("parse cloud provider bearer token payload: %w", err)
	}
	issuer := providerconfig.NormalizeOIDCIssuerURL(claims.Issuer)
	if issuer == "" {
		return "", fmt.Errorf("cloud provider bearer token does not include issuer claim")
	}
	return issuer, nil
}
