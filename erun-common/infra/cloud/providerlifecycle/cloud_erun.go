package providerlifecycle

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/cloudstore"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/clouddefaults"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
	"github.com/sophium/erun/erun-common/infra/cloud/providertoken"
)

// InitERunCloudProvider discovers the platform config and records the alias; it does not sign in.
func InitERunCloudProvider(ctx core.Context, store cloudstore.CloudStore, params model.InitERunCloudProviderParams, deps cloud.CloudDependencies) (model.CloudProviderConfig, error) {
	if store == nil {
		return model.CloudProviderConfig{}, fmt.Errorf("store is required")
	}
	deps = clouddefaults.NormalizeCloudDependencies(deps)
	apiURL := strings.TrimRight(strings.TrimSpace(params.APIURL), "/")
	ctx.Trace("cloud init erun: api-url=" + apiURL)
	if apiURL == "" {
		return model.CloudProviderConfig{}, fmt.Errorf("erun platform api url is required")
	}

	accountID := erunPlatformHost(apiURL)
	if ctx.DryRun {
		// Dry run makes no network call so a preview works offline.
		ctx.Trace("cloud init erun: would resolve issuer, cli client id, and oidc discovery from " + apiURL)
		provider := providerconfig.NormalizeCloudProviderConfig(model.CloudProviderConfig{
			Provider:  config.CloudProviderERun,
			Username:  erunAliasUsername,
			AccountID: accountID,
			ERun:      &model.ERunProviderConfig{APIURL: apiURL},
		})
		ctx.Trace("write cloud provider " + provider.Alias)
		return provider, nil
	}

	platform, err := deps.FetchPlatformInfo(ctx, apiURL)
	if err != nil {
		ctx.Trace("cloud init erun: platform discovery failed: " + err.Error())
		return model.CloudProviderConfig{}, err
	}
	if platform.Issuer == "" {
		return model.CloudProviderConfig{}, fmt.Errorf("erun platform at %s did not report an OIDC issuer", apiURL)
	}
	if platform.CLIClientID == "" {
		return model.CloudProviderConfig{}, fmt.Errorf("erun platform at %s did not report a cli oidc client id", apiURL)
	}
	if _, err := deps.FetchOIDCDiscovery(ctx, platform.Issuer); err != nil {
		ctx.Trace("cloud init erun: oidc discovery failed: " + err.Error())
		return model.CloudProviderConfig{}, err
	}

	alias := providerconfig.CloudProviderAlias(erunAliasUsername, accountID, config.CloudProviderERun)
	if alias == "" {
		return model.CloudProviderConfig{}, fmt.Errorf("erun cloud provider alias cannot be resolved")
	}
	provider := providerconfig.NormalizeCloudProviderConfig(model.CloudProviderConfig{
		Alias:         alias,
		Provider:      config.CloudProviderERun,
		Username:      erunAliasUsername,
		AccountID:     accountID,
		OIDCIssuerURL: platform.Issuer,
		ERun: &model.ERunProviderConfig{
			APIURL:   apiURL,
			ClientID: platform.CLIClientID,
		},
	})
	provider.ERun.RefreshTokenRef = existingERunRefreshTokenRef(store, alias)
	saved, err := providerconfig.SaveCloudProviderConfig(store, provider)
	if err != nil {
		return model.CloudProviderConfig{}, err
	}
	return saved, nil
}

// existingERunRefreshTokenRef returns the alias's current session reference so
// re-init, which replaces the alias wholesale, does not sign the operator out.
func existingERunRefreshTokenRef(store cloudstore.CloudStore, alias string) string {
	existing, err := providerconfig.ResolveCloudProvider(store, alias)
	if err != nil || existing.ERun == nil {
		return ""
	}
	return existing.ERun.RefreshTokenRef
}

// erunAliasUsername is fixed: init runs before sign-in, so there is no subject yet.
const erunAliasUsername = "erun"

// erunPlatformHost returns the API host, which keeps aliases distinct per platform.
func erunPlatformHost(apiURL string) string {
	parsed, err := url.Parse(apiURL)
	if err != nil || parsed.Host == "" {
		return strings.TrimSpace(apiURL)
	}
	return parsed.Host
}

// erunCloudProviderLogin signs in via the device grant when advertised (it works
// without a browser), else Authorization Code + PKCE on a loopback listener.
func erunCloudProviderLogin(ctx core.Context, store cloudstore.CloudStore, provider model.CloudProviderConfig, requestedFlow string, extraScopes []string, deps cloud.CloudDependencies) (model.CloudProviderStatus, error) {
	if provider.ERun == nil || strings.TrimSpace(provider.ERun.ClientID) == "" {
		return model.CloudProviderStatus{}, fmt.Errorf("erun cloud provider %q is not fully configured; run `erun cloud init erun` first", provider.Alias)
	}
	ctx.Trace("cloud login erun: alias=" + provider.Alias)
	discovery, err := deps.FetchOIDCDiscovery(ctx, provider.OIDCIssuerURL)
	if err != nil {
		return model.CloudProviderStatus{}, err
	}

	flow, err := NormalizeERunLoginFlow(requestedFlow)
	if err != nil {
		return model.CloudProviderStatus{}, err
	}
	hasDeviceEndpoint := strings.TrimSpace(discovery.DeviceAuthorizationEndpoint) != ""
	// The org-claim scope is requested by default; issuers that reject it are
	// retried with fallbackScope.
	scope := erunLoginScope(append([]string{clouddefaults.ErunOrgClaimScope}, extraScopes...))
	fallbackScope := erunLoginScope(extraScopes)
	if scope != clouddefaults.ErunOAuthScope {
		ctx.Trace("cloud login erun: requesting scope " + scope)
	}

	tokens, err := acquireERunLoginTokens(ctx, discovery, provider.ERun.ClientID, scope, fallbackScope, flow, hasDeviceEndpoint, deps)
	if err != nil {
		return model.CloudProviderStatus{}, err
	}
	if ctx.DryRun {
		return model.CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusActive}, nil
	}
	return persistERunLoginTokens(store, provider, deps, tokens)
}

// acquireERunLoginTokens honours an explicit flow exactly. Auto prefers the device
// grant but falls back to authcode, which can reuse an existing browser session.
// A refused scope is retried on the same flow before switching flows.
func acquireERunLoginTokens(ctx core.Context, discovery cloud.OIDCDiscovery, clientID, scope, fallbackScope, flow string, hasDeviceEndpoint bool, deps cloud.CloudDependencies) (cloud.ERunTokens, error) {
	device := func(s string) (cloud.ERunTokens, error) {
		return erunDeviceFlowLogin(ctx, discovery, clientID, s, deps)
	}
	authCode := func(s string) (cloud.ERunTokens, error) {
		return deps.RunERunAuthCodeLogin(ctx, discovery, clientID, s)
	}

	switch {
	case flow == ERunLoginFlowAuthCode:
		ctx.Trace("cloud login erun: authorization code + PKCE requested explicitly")
		return runERunLoginAttempt(ctx, scope, fallbackScope, authCode)
	case flow == ERunLoginFlowDevice:
		if !hasDeviceEndpoint {
			return cloud.ERunTokens{}, fmt.Errorf("issuer %s does not advertise a device authorization endpoint; retry with --flow %s", discovery.Issuer, ERunLoginFlowAuthCode)
		}
		return runERunLoginAttempt(ctx, scope, fallbackScope, device)
	case hasDeviceEndpoint:
		tokens, err := runERunLoginAttempt(ctx, scope, fallbackScope, device)
		if err == nil {
			return tokens, nil
		}
		ctx.Trace("cloud login erun: device flow did not complete (" + err.Error() + "); falling back to authorization code + PKCE")
		return runERunLoginAttempt(ctx, scope, fallbackScope, authCode)
	default:
		return runERunLoginAttempt(ctx, scope, fallbackScope, authCode)
	}
}

// runERunLoginAttempt retries once with fallbackScope only on invalid_scope.
func runERunLoginAttempt(ctx core.Context, scope, fallbackScope string, run func(string) (cloud.ERunTokens, error)) (cloud.ERunTokens, error) {
	tokens, err := run(scope)
	if err == nil || scope == fallbackScope || !clouddefaults.IsERunInvalidScopeError(err) {
		return tokens, err
	}
	ctx.Trace("cloud login erun: issuer rejected scope " + scope + "; retrying with " + fallbackScope)
	return run(fallbackScope)
}

func persistERunLoginTokens(store cloudstore.CloudStore, provider model.CloudProviderConfig, deps cloud.CloudDependencies, tokens cloud.ERunTokens) (model.CloudProviderStatus, error) {
	if deps.CloudSecretStore == nil {
		return model.CloudProviderStatus{}, fmt.Errorf("cloud secret store is not configured")
	}
	if tokens.RefreshToken != "" {
		ref := providertoken.ErunRefreshTokenRef(provider.Alias)
		if err := deps.CloudSecretStore.SaveCloudSecret(ref, tokens.RefreshToken); err != nil {
			return model.CloudProviderStatus{}, fmt.Errorf("store erun refresh token: %w", err)
		}
		provider.ERun.RefreshTokenRef = ref
		saved, err := providerconfig.SaveCloudProviderConfig(store, provider)
		if err != nil {
			return model.CloudProviderStatus{}, err
		}
		provider = saved
	}
	if err := providertoken.SaveCachedERunAccessToken(deps.CloudSecretStore, provider.Alias, tokens); err != nil {
		return model.CloudProviderStatus{}, err
	}
	return providertoken.CloudProviderTokenStatus(provider, deps), nil
}

func erunDeviceFlowLogin(ctx core.Context, discovery cloud.OIDCDiscovery, clientID, scope string, deps cloud.CloudDependencies) (cloud.ERunTokens, error) {
	auth, err := deps.StartERunDeviceAuthorization(ctx, discovery, clientID, scope)
	if err != nil {
		return cloud.ERunTokens{}, err
	}
	if !ctx.DryRun {
		prompt := auth.VerificationURIComplete
		if prompt == "" {
			prompt = auth.VerificationURI
		}
		clouddefaults.WriteERunLoginPrompt(ctx, fmt.Sprintf("To sign in, visit %s and enter code: %s\n", prompt, auth.UserCode))
	}
	return deps.PollERunDeviceToken(ctx, discovery, clientID, auth)
}

// erunCloudProviderLogout removes the stored refresh and access tokens, leaving the alias configured.
func erunCloudProviderLogout(ctx core.Context, provider model.CloudProviderConfig, deps cloud.CloudDependencies) error {
	ctx.Trace("cloud logout erun: alias=" + provider.Alias)
	if ctx.DryRun {
		return nil
	}
	if deps.CloudSecretStore == nil {
		return fmt.Errorf("cloud secret store is not configured")
	}
	if provider.ERun != nil && provider.ERun.RefreshTokenRef != "" {
		if err := deps.CloudSecretStore.DeleteCloudSecret(provider.ERun.RefreshTokenRef); err != nil {
			return err
		}
	}
	return deps.CloudSecretStore.DeleteCloudSecret(providertoken.ErunAccessTokenCacheRef(provider.Alias))
}
