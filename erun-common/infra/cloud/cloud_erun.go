package cloud

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// InitERunCloudProviderParams carries the API base URL; everything else is discovered from the platform.
type InitERunCloudProviderParams struct {
	APIURL string
}

// HostedPlatformAPIURL is erun's hosted platform API, one apex host for every tenant.
const HostedPlatformAPIURL = "https://api.erunpaas.com"

// InitERunCloudProvider discovers the platform config and records the alias; it does not sign in.
func InitERunCloudProvider(ctx core.Context, store CloudStore, params InitERunCloudProviderParams, deps CloudDependencies) (config.CloudProviderConfig, error) {
	if store == nil {
		return config.CloudProviderConfig{}, fmt.Errorf("store is required")
	}
	deps = NormalizeCloudDependencies(deps)
	apiURL := strings.TrimRight(strings.TrimSpace(params.APIURL), "/")
	ctx.Trace("cloud init erun: api-url=" + apiURL)
	if apiURL == "" {
		return config.CloudProviderConfig{}, fmt.Errorf("erun platform api url is required")
	}

	accountID := erunPlatformHost(apiURL)
	if ctx.DryRun {
		// Dry run makes no network call so a preview works offline.
		ctx.Trace("cloud init erun: would resolve issuer, cli client id, and oidc discovery from " + apiURL)
		provider := NormalizeCloudProviderConfig(config.CloudProviderConfig{
			Provider:  config.CloudProviderERun,
			Username:  erunAliasUsername,
			AccountID: accountID,
			ERun:      &config.ERunProviderConfig{APIURL: apiURL},
		})
		ctx.Trace("write cloud provider " + provider.Alias)
		return provider, nil
	}

	platform, err := deps.FetchPlatformInfo(ctx, apiURL)
	if err != nil {
		ctx.Trace("cloud init erun: platform discovery failed: " + err.Error())
		return config.CloudProviderConfig{}, err
	}
	if platform.Issuer == "" {
		return config.CloudProviderConfig{}, fmt.Errorf("erun platform at %s did not report an OIDC issuer", apiURL)
	}
	if platform.CLIClientID == "" {
		return config.CloudProviderConfig{}, fmt.Errorf("erun platform at %s did not report a cli oidc client id", apiURL)
	}
	if _, err := deps.FetchOIDCDiscovery(ctx, platform.Issuer); err != nil {
		ctx.Trace("cloud init erun: oidc discovery failed: " + err.Error())
		return config.CloudProviderConfig{}, err
	}

	alias := CloudProviderAlias(erunAliasUsername, accountID, config.CloudProviderERun)
	if alias == "" {
		return config.CloudProviderConfig{}, fmt.Errorf("erun cloud provider alias cannot be resolved")
	}
	provider := NormalizeCloudProviderConfig(config.CloudProviderConfig{
		Alias:         alias,
		Provider:      config.CloudProviderERun,
		Username:      erunAliasUsername,
		AccountID:     accountID,
		OIDCIssuerURL: platform.Issuer,
		ERun: &config.ERunProviderConfig{
			APIURL:   apiURL,
			ClientID: platform.CLIClientID,
		},
	})
	provider.ERun.RefreshTokenRef = existingERunRefreshTokenRef(store, alias)
	saved, err := SaveCloudProviderConfig(store, provider)
	if err != nil {
		return config.CloudProviderConfig{}, err
	}
	return saved, nil
}

// existingERunRefreshTokenRef returns the alias's current session reference so
// re-init, which replaces the alias wholesale, does not sign the operator out.
func existingERunRefreshTokenRef(store CloudStore, alias string) string {
	existing, err := ResolveCloudProvider(store, alias)
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
func erunCloudProviderLogin(ctx core.Context, store CloudStore, provider config.CloudProviderConfig, requestedFlow string, extraScopes []string, deps CloudDependencies) (CloudProviderStatus, error) {
	if provider.ERun == nil || strings.TrimSpace(provider.ERun.ClientID) == "" {
		return CloudProviderStatus{}, fmt.Errorf("erun cloud provider %q is not fully configured; run `erun cloud init erun` first", provider.Alias)
	}
	ctx.Trace("cloud login erun: alias=" + provider.Alias)
	discovery, err := deps.FetchOIDCDiscovery(ctx, provider.OIDCIssuerURL)
	if err != nil {
		return CloudProviderStatus{}, err
	}

	flow, err := NormalizeERunLoginFlow(requestedFlow)
	if err != nil {
		return CloudProviderStatus{}, err
	}
	hasDeviceEndpoint := strings.TrimSpace(discovery.DeviceAuthorizationEndpoint) != ""
	// The org-claim scope is requested by default; issuers that reject it are
	// retried with fallbackScope.
	scope := erunLoginScope(append([]string{erunOrgClaimScope}, extraScopes...))
	fallbackScope := erunLoginScope(extraScopes)
	if scope != erunOAuthScope {
		ctx.Trace("cloud login erun: requesting scope " + scope)
	}

	tokens, err := acquireERunLoginTokens(ctx, discovery, provider.ERun.ClientID, scope, fallbackScope, flow, hasDeviceEndpoint, deps)
	if err != nil {
		return CloudProviderStatus{}, err
	}
	if ctx.DryRun {
		return CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusActive}, nil
	}
	return persistERunLoginTokens(store, provider, deps, tokens)
}

// acquireERunLoginTokens honours an explicit flow exactly. Auto prefers the device
// grant but falls back to authcode, which can reuse an existing browser session.
// A refused scope is retried on the same flow before switching flows.
func acquireERunLoginTokens(ctx core.Context, discovery OIDCDiscovery, clientID, scope, fallbackScope, flow string, hasDeviceEndpoint bool, deps CloudDependencies) (ERunTokens, error) {
	device := func(s string) (ERunTokens, error) { return erunDeviceFlowLogin(ctx, discovery, clientID, s, deps) }
	authCode := func(s string) (ERunTokens, error) { return deps.RunERunAuthCodeLogin(ctx, discovery, clientID, s) }

	switch {
	case flow == ERunLoginFlowAuthCode:
		ctx.Trace("cloud login erun: authorization code + PKCE requested explicitly")
		return runERunLoginAttempt(ctx, scope, fallbackScope, authCode)
	case flow == ERunLoginFlowDevice:
		if !hasDeviceEndpoint {
			return ERunTokens{}, fmt.Errorf("issuer %s does not advertise a device authorization endpoint; retry with --flow %s", discovery.Issuer, ERunLoginFlowAuthCode)
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
func runERunLoginAttempt(ctx core.Context, scope, fallbackScope string, run func(string) (ERunTokens, error)) (ERunTokens, error) {
	tokens, err := run(scope)
	if err == nil || scope == fallbackScope || !isERunInvalidScopeError(err) {
		return tokens, err
	}
	ctx.Trace("cloud login erun: issuer rejected scope " + scope + "; retrying with " + fallbackScope)
	return run(fallbackScope)
}

func persistERunLoginTokens(store CloudStore, provider config.CloudProviderConfig, deps CloudDependencies, tokens ERunTokens) (CloudProviderStatus, error) {
	if deps.CloudSecretStore == nil {
		return CloudProviderStatus{}, fmt.Errorf("cloud secret store is not configured")
	}
	if tokens.RefreshToken != "" {
		ref := ErunRefreshTokenRef(provider.Alias)
		if err := deps.CloudSecretStore.SaveCloudSecret(ref, tokens.RefreshToken); err != nil {
			return CloudProviderStatus{}, fmt.Errorf("store erun refresh token: %w", err)
		}
		provider.ERun.RefreshTokenRef = ref
		saved, err := SaveCloudProviderConfig(store, provider)
		if err != nil {
			return CloudProviderStatus{}, err
		}
		provider = saved
	}
	if err := SaveCachedERunAccessToken(deps.CloudSecretStore, provider.Alias, tokens); err != nil {
		return CloudProviderStatus{}, err
	}
	return CloudProviderTokenStatus(provider, deps), nil
}

func erunDeviceFlowLogin(ctx core.Context, discovery OIDCDiscovery, clientID, scope string, deps CloudDependencies) (ERunTokens, error) {
	auth, err := deps.StartERunDeviceAuthorization(ctx, discovery, clientID, scope)
	if err != nil {
		return ERunTokens{}, err
	}
	if !ctx.DryRun {
		prompt := auth.VerificationURIComplete
		if prompt == "" {
			prompt = auth.VerificationURI
		}
		writeERunLoginPrompt(ctx, fmt.Sprintf("To sign in, visit %s and enter code: %s\n", prompt, auth.UserCode))
	}
	return deps.PollERunDeviceToken(ctx, discovery, clientID, auth)
}

func writeERunLoginPrompt(ctx core.Context, message string) {
	if ctx.Stdout == nil {
		return
	}
	_, _ = ctx.Stdout.Write([]byte(message))
}

// erunCloudProviderLogout removes the stored refresh and access tokens, leaving the alias configured.
func erunCloudProviderLogout(ctx core.Context, provider config.CloudProviderConfig, deps CloudDependencies) error {
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
	return deps.CloudSecretStore.DeleteCloudSecret(ErunAccessTokenCacheRef(provider.Alias))
}

func erunCloudProviderBearerToken(ctx core.Context, provider config.CloudProviderConfig, deps CloudDependencies) (CloudBearerToken, error) {
	if ctx.DryRun {
		return CloudBearerToken{Alias: provider.Alias, Provider: provider.Provider, Issuer: provider.OIDCIssuerURL}, nil
	}
	token, err := ResolveERunAccessToken(ctx, provider, deps)
	if err != nil {
		return CloudBearerToken{}, err
	}
	return CloudBearerToken{Alias: provider.Alias, Provider: provider.Provider, Token: token, Issuer: provider.OIDCIssuerURL}, nil
}

func erunCloudProviderTokenStatus(provider config.CloudProviderConfig, deps CloudDependencies) CloudProviderStatus {
	if provider.ERun == nil || strings.TrimSpace(provider.ERun.ClientID) == "" {
		return CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusNotConfigured, Message: "erun cloud provider is not fully configured"}
	}
	// Without a store nothing can be verified, so report unknown rather than
	// letting resolveERunAccessToken make it look expired.
	if deps.CloudSecretStore == nil {
		return CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusUnknown, Message: "cloud secret store is not configured"}
	}
	if _, err := ResolveERunAccessToken(core.StderrOnlyContext(), provider, deps); err != nil {
		status := config.CloudTokenStatusNotConfigured
		if provider.ERun.RefreshTokenRef != "" || provider.ERun.IsMachineIdentity() {
			status = config.CloudTokenStatusExpired
		}
		return CloudProviderStatus{CloudProviderConfig: provider, Status: status, Message: err.Error()}
	}
	return CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusActive}
}

// ResolveERunAccessToken returns a cached or refreshed access token; it is the
// single resolver so bearer use and status checks agree on "active".
func ResolveERunAccessToken(ctx core.Context, provider config.CloudProviderConfig, deps CloudDependencies) (string, error) {
	if provider.ERun == nil || strings.TrimSpace(provider.ERun.ClientID) == "" {
		return "", fmt.Errorf("erun cloud provider %q is not fully configured", provider.Alias)
	}
	if deps.CloudSecretStore == nil {
		return "", fmt.Errorf("cloud secret store is not configured")
	}
	if token, ok := loadCachedERunAccessToken(deps.CloudSecretStore, provider.Alias); ok {
		return token, nil
	}
	if provider.ERun.IsMachineIdentity() {
		return clientCredentialsERunAccessToken(ctx, provider, deps)
	}
	if provider.ERun.RefreshTokenRef == "" {
		return "", fmt.Errorf("not signed in; run `erun cloud login %s`", provider.Alias)
	}
	return refreshERunAccessToken(ctx, provider, deps)
}

// clientCredentialsERunAccessToken mints a token for a provisioned machine identity.
// Its errors name re-provisioning, since no sign-in fixes a removed credential.
func clientCredentialsERunAccessToken(ctx core.Context, provider config.CloudProviderConfig, deps CloudDependencies) (string, error) {
	if deps.CloudSecretStore == nil {
		return "", fmt.Errorf("cloud secret store is not configured")
	}
	clientSecret, err := deps.CloudSecretStore.LoadCloudSecret(provider.ERun.ClientSecretRef)
	if err != nil || strings.TrimSpace(clientSecret) == "" {
		return "", fmt.Errorf("this environment's machine identity credential is not available; re-provision the environment's identity")
	}
	discovery, err := deps.FetchOIDCDiscovery(ctx, provider.OIDCIssuerURL)
	if err != nil {
		return "", err
	}
	tokens, err := deps.ClientCredentialsERunTokens(ctx, discovery, provider.ERun.ClientID, clientSecret)
	if err != nil {
		return "", fmt.Errorf("mint machine identity token: %w", err)
	}
	if tokens.AccessToken == "" {
		return "", fmt.Errorf("mint machine identity token: response is missing an access token")
	}
	if err := SaveCachedERunAccessToken(deps.CloudSecretStore, provider.Alias, tokens); err != nil {
		return "", err
	}
	return tokens.AccessToken, nil
}

// refreshERunAccessToken also rotates the stored refresh token when the server issues a new one.
func refreshERunAccessToken(ctx core.Context, provider config.CloudProviderConfig, deps CloudDependencies) (string, error) {
	refreshToken, err := deps.CloudSecretStore.LoadCloudSecret(provider.ERun.RefreshTokenRef)
	if err != nil || strings.TrimSpace(refreshToken) == "" {
		return "", fmt.Errorf("refresh token is not available on this machine; run `erun cloud login %s`", provider.Alias)
	}
	discovery, err := deps.FetchOIDCDiscovery(ctx, provider.OIDCIssuerURL)
	if err != nil {
		return "", err
	}
	tokens, err := deps.RefreshERunTokens(ctx, discovery, provider.ERun.ClientID, refreshToken)
	if err != nil {
		return "", fmt.Errorf("refresh erun cloud provider token: %w; run `erun cloud login %s`", err, provider.Alias)
	}
	if tokens.AccessToken == "" {
		return "", fmt.Errorf("refresh erun cloud provider token: response is missing an access token; run `erun cloud login %s`", provider.Alias)
	}
	if err := SaveCachedERunAccessToken(deps.CloudSecretStore, provider.Alias, tokens); err != nil {
		return "", err
	}
	if tokens.RefreshToken != "" && tokens.RefreshToken != refreshToken {
		if err := deps.CloudSecretStore.SaveCloudSecret(provider.ERun.RefreshTokenRef, tokens.RefreshToken); err != nil {
			return "", err
		}
	}
	return tokens.AccessToken, nil
}

func ErunRefreshTokenRef(alias string) string {
	return "erun/refresh/" + strings.TrimSpace(alias)
}

func ErunAccessTokenCacheRef(alias string) string {
	return "erun/access/" + strings.TrimSpace(alias)
}

const erunAccessTokenExpiryLeeway = 30 * time.Second

type ErunAccessTokenCache struct {
	AccessToken string    `json:"accessToken"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

func loadCachedERunAccessToken(store CloudSecretStore, alias string) (string, bool) {
	if store == nil {
		return "", false
	}
	raw, err := store.LoadCloudSecret(ErunAccessTokenCacheRef(alias))
	if err != nil || strings.TrimSpace(raw) == "" {
		return "", false
	}
	var cached ErunAccessTokenCache
	if err := json.Unmarshal([]byte(raw), &cached); err != nil {
		return "", false
	}
	if cached.AccessToken == "" || !time.Now().Add(erunAccessTokenExpiryLeeway).Before(cached.ExpiresAt) {
		return "", false
	}
	return cached.AccessToken, true
}

func SaveCachedERunAccessToken(store CloudSecretStore, alias string, tokens ERunTokens) error {
	if store == nil || tokens.AccessToken == "" {
		return nil
	}
	encoded, err := json.Marshal(ErunAccessTokenCache{
		AccessToken: tokens.AccessToken,
		ExpiresAt:   time.Now().Add(tokens.ExpiresIn),
	})
	if err != nil {
		return err
	}
	return store.SaveCloudSecret(ErunAccessTokenCacheRef(alias), string(encoded))
}
