package providertoken

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/cloudstore"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// HostedPlatformAPIURL is erun's hosted platform API, one apex host for every tenant.
const HostedPlatformAPIURL = "https://api.erunpaas.com"

func erunCloudProviderBearerToken(ctx core.Context, provider model.CloudProviderConfig, deps cloud.CloudDependencies) (CloudBearerToken, error) {
	if ctx.DryRun {
		return CloudBearerToken{Alias: provider.Alias, Provider: provider.Provider, Issuer: provider.OIDCIssuerURL}, nil
	}
	token, err := ResolveERunAccessToken(ctx, provider, deps)
	if err != nil {
		return CloudBearerToken{}, err
	}
	return CloudBearerToken{Alias: provider.Alias, Provider: provider.Provider, Token: token, Issuer: provider.OIDCIssuerURL}, nil
}

func erunCloudProviderTokenStatus(provider model.CloudProviderConfig, deps cloud.CloudDependencies) model.CloudProviderStatus {
	if provider.ERun == nil || strings.TrimSpace(provider.ERun.ClientID) == "" {
		return model.CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusNotConfigured, Message: "erun cloud provider is not fully configured"}
	}
	// Without a store nothing can be verified, so report unknown rather than
	// letting resolveERunAccessToken make it look expired.
	if deps.CloudSecretStore == nil {
		return model.CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusUnknown, Message: "cloud secret store is not configured"}
	}
	if _, err := ResolveERunAccessToken(core.StderrOnlyContext(), provider, deps); err != nil {
		status := config.CloudTokenStatusNotConfigured
		if provider.ERun.RefreshTokenRef != "" || provider.ERun.IsMachineIdentity() {
			status = config.CloudTokenStatusExpired
		}
		return model.CloudProviderStatus{CloudProviderConfig: provider, Status: status, Message: err.Error()}
	}
	return model.CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusActive}
}

// ResolveERunAccessToken returns a cached or refreshed access token; it is the
// single resolver so bearer use and status checks agree on "active".
func ResolveERunAccessToken(ctx core.Context, provider model.CloudProviderConfig, deps cloud.CloudDependencies) (string, error) {
	if provider.ERun == nil || strings.TrimSpace(provider.ERun.ClientID) == "" {
		return "", fmt.Errorf("erun cloud provider %q is not fully configured", provider.Alias)
	}
	if deps.CloudSecretStore == nil {
		return "", fmt.Errorf("cloud secret store is not configured")
	}
	if token, ok := LoadCachedERunAccessToken(deps.CloudSecretStore, provider.Alias); ok {
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
func clientCredentialsERunAccessToken(ctx core.Context, provider model.CloudProviderConfig, deps cloud.CloudDependencies) (string, error) {
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
func refreshERunAccessToken(ctx core.Context, provider model.CloudProviderConfig, deps cloud.CloudDependencies) (string, error) {
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

func LoadCachedERunAccessToken(store cloudstore.CloudSecretStore, alias string) (string, bool) {
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

func SaveCachedERunAccessToken(store cloudstore.CloudSecretStore, alias string, tokens cloud.ERunTokens) error {
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
