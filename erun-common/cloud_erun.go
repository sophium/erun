package eruncommon

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ERunProviderConfig is the erun-hosted-platform identity for a cloud alias:
// the API this alias authenticates against, the CLI OIDC client it signs in
// with, and a reference to the stored refresh token (never the token itself,
// which lives only in the secret store).
type ERunProviderConfig struct {
	APIURL          string `json:"apiUrl,omitempty" yaml:"apiurl,omitempty"`
	ClientID        string `json:"clientId,omitempty" yaml:"clientid,omitempty"`
	RefreshTokenRef string `json:"refreshTokenRef,omitempty" yaml:"refreshtokenref,omitempty"`
}

// InitERunCloudProviderParams is the explicit input for attaching a hosted
// erun platform as a cloud alias: just the API base URL. Everything else
// (issuer, CLI client id) is discovered from the platform itself.
type InitERunCloudProviderParams struct {
	APIURL string
}

// HostedPlatformAPIURL is erun's own hosted platform's front door — a single
// apex host serving every tenant, never a per-tenant or per-environment one.
// The CLI's `cloud init erun` help text and the desktop's Connect-tenant
// default both read this constant so the two cannot drift apart the way they
// did when each hardcoded its own guess.
const HostedPlatformAPIURL = "https://api.erunpaas.com"

// InitERunCloudProvider discovers a hosted erun platform's own config (GET
// /v1/platform) and its issuer's OIDC discovery document, then records the
// alias, API URL, issuer, and CLI client id. It performs no sign-in: use
// LoginCloudProviderAlias afterward to obtain a token.
func InitERunCloudProvider(ctx Context, store CloudStore, params InitERunCloudProviderParams, deps CloudDependencies) (CloudProviderConfig, error) {
	if store == nil {
		return CloudProviderConfig{}, fmt.Errorf("store is required")
	}
	deps = normalizeCloudDependencies(deps)
	apiURL := strings.TrimRight(strings.TrimSpace(params.APIURL), "/")
	ctx.Trace("cloud init erun: api-url=" + apiURL)
	if apiURL == "" {
		return CloudProviderConfig{}, fmt.Errorf("erun platform api url is required")
	}

	accountID := erunPlatformHost(apiURL)
	if ctx.DryRun {
		// No network call at all in dry run — a preview must not depend on the
		// platform actually being reachable.
		ctx.Trace("cloud init erun: would resolve issuer, cli client id, and oidc discovery from " + apiURL)
		provider := NormalizeCloudProviderConfig(CloudProviderConfig{
			Provider:  CloudProviderERun,
			Username:  erunAliasUsername,
			AccountID: accountID,
			ERun:      &ERunProviderConfig{APIURL: apiURL},
		})
		ctx.Trace("write cloud provider " + provider.Alias)
		return provider, nil
	}

	platform, err := deps.FetchPlatformInfo(ctx, apiURL)
	if err != nil {
		ctx.Trace("cloud init erun: platform discovery failed: " + err.Error())
		return CloudProviderConfig{}, err
	}
	if platform.Issuer == "" {
		return CloudProviderConfig{}, fmt.Errorf("erun platform at %s did not report an OIDC issuer", apiURL)
	}
	if platform.CLIClientID == "" {
		return CloudProviderConfig{}, fmt.Errorf("erun platform at %s did not report a cli oidc client id", apiURL)
	}
	if _, err := deps.FetchOIDCDiscovery(ctx, platform.Issuer); err != nil {
		ctx.Trace("cloud init erun: oidc discovery failed: " + err.Error())
		return CloudProviderConfig{}, err
	}

	alias := CloudProviderAlias(erunAliasUsername, accountID, CloudProviderERun)
	if alias == "" {
		return CloudProviderConfig{}, fmt.Errorf("erun cloud provider alias cannot be resolved")
	}
	provider := NormalizeCloudProviderConfig(CloudProviderConfig{
		Alias:         alias,
		Provider:      CloudProviderERun,
		Username:      erunAliasUsername,
		AccountID:     accountID,
		OIDCIssuerURL: platform.Issuer,
		ERun: &ERunProviderConfig{
			APIURL:   apiURL,
			ClientID: platform.CLIClientID,
		},
	})
	saved, err := SaveCloudProviderConfig(store, provider)
	if err != nil {
		return CloudProviderConfig{}, err
	}
	return saved, nil
}

// erunAliasUsername is fixed rather than derived from a signed-in identity:
// InitERunCloudProvider runs before any sign-in, so there is no subject yet to
// name the alias with. AccountID (the platform's own host) is what makes the
// alias distinct per platform.
const erunAliasUsername = "erun"

// erunPlatformHost extracts the host from an API URL for use as the alias's
// account-id-equivalent slot, so two different hosted platforms never collide
// on the same alias.
func erunPlatformHost(apiURL string) string {
	parsed, err := url.Parse(apiURL)
	if err != nil || parsed.Host == "" {
		return strings.TrimSpace(apiURL)
	}
	return parsed.Host
}

// erunCloudProviderLogin runs the OIDC sign-in for an erun cloud alias: the
// device authorization grant when the issuer advertises one (the only flow
// that works with no browser, e.g. from inside a pod), falling back to
// Authorization Code + PKCE on a loopback listener otherwise. On success it
// persists the refresh token (via the secret store, referenced from config)
// and caches the access token with its expiry.
func erunCloudProviderLogin(ctx Context, store CloudStore, provider CloudProviderConfig, requestedFlow string, extraScopes []string, deps CloudDependencies) (CloudProviderStatus, error) {
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
	// The org-claim scope is requested by default so a shared, org-scoped
	// issuer's tokens carry the discriminator erun's tenant resolution reads.
	// fallbackScope is what acquireERunLoginTokens retries with when an
	// issuer that has never heard of it (a dedicated/BYO IdP, the common
	// case) refuses the request — login still succeeds exactly as it did
	// before this default was added, instead of breaking on an unrecognized
	// scope.
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
		return CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusActive}, nil
	}
	return persistERunLoginTokens(store, provider, deps, tokens)
}

// acquireERunLoginTokens picks the grant and runs it. An explicitly requested
// flow is honoured exactly — including refusing the device grant against an
// issuer that does not advertise one, rather than silently substituting a
// different flow. Auto prefers the device grant but falls back to
// authorization code + PKCE: the device page forces a fresh authentication, so
// an account whose only method there is broken can never finish it, while the
// redirect reuses the browser session that same operator may already hold.
//
// Split out of erunCloudProviderLogin to keep that function under the cyclop
// threshold; the selection and its ordering are unchanged. Each flow attempt
// is wrapped by runERunLoginAttempt so a scope the issuer refuses is retried
// once, on the same flow, with fallbackScope — before falling back to a
// different flow at all, since a different flow against the same issuer would
// likely refuse the same scope for the same reason.
func acquireERunLoginTokens(ctx Context, discovery OIDCDiscovery, clientID, scope, fallbackScope, flow string, hasDeviceEndpoint bool, deps CloudDependencies) (ERunTokens, error) {
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

// runERunLoginAttempt runs run(scope), retrying once with fallbackScope when
// the issuer's refusal is specifically an invalid_scope response — never for
// any other failure, which is returned to the caller unchanged.
func runERunLoginAttempt(ctx Context, scope, fallbackScope string, run func(string) (ERunTokens, error)) (ERunTokens, error) {
	tokens, err := run(scope)
	if err == nil || scope == fallbackScope || !isERunInvalidScopeError(err) {
		return tokens, err
	}
	ctx.Trace("cloud login erun: issuer rejected scope " + scope + "; retrying with " + fallbackScope)
	return run(fallbackScope)
}

// persistERunLoginTokens saves a fresh refresh token (when the grant returned
// one) and caches the access token, then returns the resulting status.
func persistERunLoginTokens(store CloudStore, provider CloudProviderConfig, deps CloudDependencies, tokens ERunTokens) (CloudProviderStatus, error) {
	if deps.CloudSecretStore == nil {
		return CloudProviderStatus{}, fmt.Errorf("cloud secret store is not configured")
	}
	if tokens.RefreshToken != "" {
		ref := erunRefreshTokenRef(provider.Alias)
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
	if err := saveCachedERunAccessToken(deps.CloudSecretStore, provider.Alias, tokens); err != nil {
		return CloudProviderStatus{}, err
	}
	return CloudProviderTokenStatus(provider, deps), nil
}

func erunDeviceFlowLogin(ctx Context, discovery OIDCDiscovery, clientID, scope string, deps CloudDependencies) (ERunTokens, error) {
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

func writeERunLoginPrompt(ctx Context, message string) {
	if ctx.Stdout == nil {
		return
	}
	_, _ = ctx.Stdout.Write([]byte(message))
}

// erunCloudProviderLogout is the erun equivalent of Cloudflare's token
// deletion: there is no SSO session to end, so "logout" removes the stored
// refresh token and cached access token, leaving the alias configured but
// signed out.
func erunCloudProviderLogout(ctx Context, provider CloudProviderConfig, deps CloudDependencies) error {
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
	return deps.CloudSecretStore.DeleteCloudSecret(erunAccessTokenCacheRef(provider.Alias))
}

func erunCloudProviderBearerToken(ctx Context, provider CloudProviderConfig, deps CloudDependencies) (CloudBearerToken, error) {
	if ctx.DryRun {
		return CloudBearerToken{Alias: provider.Alias, Provider: provider.Provider, Issuer: provider.OIDCIssuerURL}, nil
	}
	token, err := resolveERunAccessToken(ctx, provider, deps)
	if err != nil {
		return CloudBearerToken{}, err
	}
	return CloudBearerToken{Alias: provider.Alias, Provider: provider.Provider, Token: token, Issuer: provider.OIDCIssuerURL}, nil
}

func erunCloudProviderTokenStatus(provider CloudProviderConfig, deps CloudDependencies) CloudProviderStatus {
	if provider.ERun == nil || strings.TrimSpace(provider.ERun.ClientID) == "" {
		return CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusNotConfigured, Message: "erun cloud provider is not fully configured"}
	}
	// Checked here, ahead of resolveERunAccessToken, because that call also
	// reports a missing store as an error, and a configured provider with a
	// refresh token then reads as "expired" — asserting the token was checked
	// and rejected when it was never checked at all. unknown/"not
	// checked" is the honest answer when nothing was actually verified.
	if deps.CloudSecretStore == nil {
		return CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusUnknown, Message: "cloud secret store is not configured"}
	}
	if _, err := resolveERunAccessToken(Context{}, provider, deps); err != nil {
		status := CloudTokenStatusNotConfigured
		if provider.ERun.RefreshTokenRef != "" {
			status = CloudTokenStatusExpired
		}
		return CloudProviderStatus{CloudProviderConfig: provider, Status: status, Message: err.Error()}
	}
	return CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusActive}
}

// resolveERunAccessToken returns a usable access token: the cached one if
// still fresh, otherwise a freshly minted one via the refresh_token grant
// (cached for next time). It is the single place both the bearer-token path
// and the status check resolve a token, so they never disagree about what
// "active" means.
func resolveERunAccessToken(ctx Context, provider CloudProviderConfig, deps CloudDependencies) (string, error) {
	if provider.ERun == nil || strings.TrimSpace(provider.ERun.ClientID) == "" {
		return "", fmt.Errorf("erun cloud provider %q is not fully configured", provider.Alias)
	}
	if deps.CloudSecretStore == nil {
		return "", fmt.Errorf("cloud secret store is not configured")
	}
	if token, ok := loadCachedERunAccessToken(deps.CloudSecretStore, provider.Alias); ok {
		return token, nil
	}
	if provider.ERun.RefreshTokenRef == "" {
		return "", fmt.Errorf("not signed in; run `erun cloud login %s`", provider.Alias)
	}
	return refreshERunAccessToken(ctx, provider, deps)
}

// refreshERunAccessToken mints a fresh access token via the refresh_token
// grant, caches it, and rotates the stored refresh token when the server
// issued a new one (refresh-token rotation).
func refreshERunAccessToken(ctx Context, provider CloudProviderConfig, deps CloudDependencies) (string, error) {
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
	if err := saveCachedERunAccessToken(deps.CloudSecretStore, provider.Alias, tokens); err != nil {
		return "", err
	}
	if tokens.RefreshToken != "" && tokens.RefreshToken != refreshToken {
		if err := deps.CloudSecretStore.SaveCloudSecret(provider.ERun.RefreshTokenRef, tokens.RefreshToken); err != nil {
			return "", err
		}
	}
	return tokens.AccessToken, nil
}

func erunRefreshTokenRef(alias string) string {
	return "erun/refresh/" + strings.TrimSpace(alias)
}

func erunAccessTokenCacheRef(alias string) string {
	return "erun/access/" + strings.TrimSpace(alias)
}

// erunAccessTokenExpiryLeeway avoids handing back a token that expires
// moments after the caller receives it.
const erunAccessTokenExpiryLeeway = 30 * time.Second

type erunAccessTokenCache struct {
	AccessToken string    `json:"accessToken"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

func loadCachedERunAccessToken(store CloudSecretStore, alias string) (string, bool) {
	if store == nil {
		return "", false
	}
	raw, err := store.LoadCloudSecret(erunAccessTokenCacheRef(alias))
	if err != nil || strings.TrimSpace(raw) == "" {
		return "", false
	}
	var cached erunAccessTokenCache
	if err := json.Unmarshal([]byte(raw), &cached); err != nil {
		return "", false
	}
	if cached.AccessToken == "" || !time.Now().Add(erunAccessTokenExpiryLeeway).Before(cached.ExpiresAt) {
		return "", false
	}
	return cached.AccessToken, true
}

func saveCachedERunAccessToken(store CloudSecretStore, alias string, tokens ERunTokens) error {
	if store == nil || tokens.AccessToken == "" {
		return nil
	}
	encoded, err := json.Marshal(erunAccessTokenCache{
		AccessToken: tokens.AccessToken,
		ExpiresAt:   time.Now().Add(tokens.ExpiresIn),
	})
	if err != nil {
		return err
	}
	return store.SaveCloudSecret(erunAccessTokenCacheRef(alias), string(encoded))
}
