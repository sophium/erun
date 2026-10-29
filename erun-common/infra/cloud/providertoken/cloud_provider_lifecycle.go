package providertoken

import (
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/clouddefaults"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
)

func CloudProviderBearerToken(ctx core.Context, store configpkg.CloudReadStore, params model.CloudBearerParams, deps cloud.CloudDependencies) (CloudBearerToken, error) {
	provider, err := providerconfig.ResolveCloudProvider(store, params.Alias)
	if err != nil {
		return CloudBearerToken{}, err
	}
	deps = clouddefaults.NormalizeCloudDependencies(deps)
	switch provider.Provider {
	case configpkg.CloudProviderAWS:
		return awsCloudProviderBearerToken(ctx, provider, params, deps)
	case configpkg.CloudProviderCloudflare:
		return CloudBearerToken{}, ErrCloudProviderNoOIDC
	case configpkg.CloudProviderERun:
		return erunCloudProviderBearerToken(ctx, provider, deps)
	default:
		return CloudBearerToken{}, fmt.Errorf("unsupported cloud provider %q", provider.Provider)
	}
}

func awsCloudProviderBearerToken(ctx core.Context, provider model.CloudProviderConfig, params model.CloudBearerParams, deps cloud.CloudDependencies) (CloudBearerToken, error) {
	status := CloudProviderTokenStatus(provider, deps)
	if status.Status != configpkg.CloudTokenStatusActive {
		if err := deps.RunAWSLogin(ctx, provider.Profile); err != nil {
			return CloudBearerToken{}, err
		}
	}
	audience := clouddefaults.NormalizeCloudBearerAudience(params.Audience)
	rawToken, err := awsBearerTokenWithOIDCRetry(ctx, provider, audience, deps)
	if err != nil {
		return CloudBearerToken{}, err
	}
	if ctx.DryRun && strings.TrimSpace(rawToken) == "" {
		return CloudBearerToken{
			Alias:    provider.Alias,
			Provider: provider.Provider,
			Issuer:   "derived-from-aws-web-identity-token",
		}, nil
	}
	issuer, err := issuerFromJWT(rawToken)
	if err != nil {
		return CloudBearerToken{}, err
	}
	return CloudBearerToken{
		Alias:    provider.Alias,
		Provider: provider.Provider,
		Token:    rawToken,
		Issuer:   issuer,
	}, nil
}

func awsBearerTokenWithOIDCRetry(ctx core.Context, provider model.CloudProviderConfig, audience string, deps cloud.CloudDependencies) (string, error) {
	rawToken, err := deps.RunAWSBearerToken(ctx, provider.Profile, audience)
	if err != nil && !isAWSOutboundWebIdentityFederationDisabled(err) {
		return "", err
	}
	if err == nil && !ctx.DryRun {
		return rawToken, nil
	}
	// RunAWSBearerToken always succeeds in dry run, so trace the recovery unconditionally.
	if _, enableErr := deps.RunAWSEnableOIDC(ctx, provider.Profile); enableErr != nil {
		return "", enableErr
	}
	return deps.RunAWSBearerToken(ctx, provider.Profile, audience)
}

// ExportCloudProviderCredentials returns short-lived AWS credentials from the alias's host profile.
func ExportCloudProviderCredentials(ctx core.Context, store configpkg.CloudReadStore, alias string, deps cloud.CloudDependencies) (cloud.CloudProviderCredentials, error) {
	provider, err := providerconfig.ResolveCloudProvider(store, alias)
	if err != nil {
		return cloud.CloudProviderCredentials{}, err
	}
	deps = clouddefaults.NormalizeCloudDependencies(deps)
	switch provider.Provider {
	case configpkg.CloudProviderAWS:
		creds, err := deps.RunAWSExportCredentials(ctx, provider.Profile)
		if err != nil {
			return cloud.CloudProviderCredentials{}, err
		}
		creds.Alias = provider.Alias
		return creds, nil
	default:
		return cloud.CloudProviderCredentials{}, fmt.Errorf("unsupported cloud provider %q", provider.Provider)
	}
}
