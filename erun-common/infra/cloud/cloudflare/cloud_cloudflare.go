package cloudflare

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/cloudstore"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/clouddefaults"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
)

// CloudflareCreateTokenURL is the dashboard page for minting a scoped token.
const CloudflareCreateTokenURL = "https://dash.cloudflare.com/profile/api-tokens"

// InitCloudflareCloudProvider verifies and stores the token, then saves the alias by TokenRef.
func InitCloudflareCloudProvider(ctx core.Context, store cloudstore.CloudStore, params model.InitCloudflareCloudProviderParams, deps cloud.CloudDependencies) (model.CloudProviderConfig, error) {
	if store == nil {
		return model.CloudProviderConfig{}, fmt.Errorf("store is required")
	}
	deps = clouddefaults.NormalizeCloudDependencies(deps)
	accountID := strings.TrimSpace(params.AccountID)
	tokenName := strings.TrimSpace(params.TokenName)
	apiToken := strings.TrimSpace(params.APIToken)
	ctx.Trace(fmt.Sprintf("cloud init cloudflare: account-id=%s token-name=%s api-token=%s",
		accountID, tokenName, redactSecretPresence(apiToken)))
	if err := validateCloudflareInitParams(accountID, tokenName, apiToken); err != nil {
		return model.CloudProviderConfig{}, err
	}
	alias := providerconfig.CloudProviderAlias(tokenName, accountID, config.CloudProviderCloudflare)
	if alias == "" {
		return model.CloudProviderConfig{}, fmt.Errorf("cloudflare cloud provider alias cannot be resolved")
	}
	ctx.Trace("cloud init cloudflare: verifying scoped api token")
	if _, err := deps.VerifyCloudflareToken(ctx, apiToken); err != nil {
		ctx.Trace("cloud init cloudflare: token verification failed: " + err.Error())
		return model.CloudProviderConfig{}, err
	}
	tokenRef := cloudflareTokenRef(alias)
	provider := cloudflareProviderConfig(alias, accountID, tokenName, tokenRef)
	if ctx.DryRun {
		traceCloudflareInitDryRunPlan(ctx, deps, tokenRef, alias)
		return provider, nil
	}
	if deps.CloudSecretStore == nil {
		return model.CloudProviderConfig{}, fmt.Errorf("cloud secret store is not configured")
	}
	if err := deps.CloudSecretStore.SaveCloudSecret(tokenRef, apiToken); err != nil {
		return model.CloudProviderConfig{}, fmt.Errorf("store cloudflare api token: %w", err)
	}
	saved, err := providerconfig.SaveCloudProviderConfig(store, provider)
	if err != nil {
		return model.CloudProviderConfig{}, err
	}
	return saved, nil
}

func validateCloudflareInitParams(accountID, tokenName, apiToken string) error {
	switch {
	case accountID == "":
		return fmt.Errorf("cloudflare account id is required")
	case tokenName == "":
		return fmt.Errorf("cloudflare token name is required")
	case apiToken == "":
		return fmt.Errorf("cloudflare api token is required")
	}
	return nil
}

func traceCloudflareInitDryRunPlan(ctx core.Context, deps cloud.CloudDependencies, tokenRef, alias string) {
	if deps.CloudSecretStore == nil {
		ctx.Trace("cloud init cloudflare: secret store is not configured (real run would fail to persist the token)")
	} else {
		ctx.Trace("cloud init cloudflare: secret store is configured")
	}
	ctx.Trace("store cloudflare api token at ref " + tokenRef)
	ctx.Trace("write cloud provider " + alias)
}

// VerifyCloudflareAPIToken validates a scoped token.
func VerifyCloudflareAPIToken(ctx core.Context, token string, deps cloud.CloudDependencies) (cloud.CloudflareTokenInfo, error) {
	return clouddefaults.NormalizeCloudDependencies(deps).VerifyCloudflareToken(ctx, token)
}

// ResolveCloudflareAccounts lists the accounts a token can act on.
func ResolveCloudflareAccounts(ctx core.Context, token string, deps cloud.CloudDependencies) ([]cloud.CloudflareAccount, error) {
	return clouddefaults.NormalizeCloudDependencies(deps).ListCloudflareAccounts(ctx, token)
}

// Mirrors the identity into generic fields so listing needs no Cloudflare special case.
func cloudflareProviderConfig(alias, accountID, tokenName, tokenRef string) model.CloudProviderConfig {
	return providerconfig.NormalizeCloudProviderConfig(model.CloudProviderConfig{
		Alias:     alias,
		Provider:  config.CloudProviderCloudflare,
		Username:  tokenName,
		AccountID: accountID,
		Cloudflare: &model.CloudflareProviderConfig{
			AccountID: accountID,
			TokenName: tokenName,
			TokenRef:  tokenRef,
		},
	})
}

func cloudflareTokenRef(alias string) string {
	return "cloudflare/" + strings.TrimSpace(alias)
}

func redactSecretPresence(value string) string {
	if strings.TrimSpace(value) == "" {
		return "<empty>"
	}
	return "<redacted>"
}
