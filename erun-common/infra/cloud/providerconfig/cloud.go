package providerconfig

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/cloudstore"
)

func NormalizeCloudProviderConfig(config model.CloudProviderConfig) model.CloudProviderConfig {
	config.Alias = strings.TrimSpace(config.Alias)
	config.Provider = strings.ToLower(strings.TrimSpace(config.Provider))
	config.Username = strings.TrimSpace(config.Username)
	config.AccountID = strings.TrimSpace(config.AccountID)
	config.Profile = strings.TrimSpace(config.Profile)
	config.SSORegion = strings.TrimSpace(config.SSORegion)
	config.SSOStartURL = strings.TrimSpace(config.SSOStartURL)
	config.OIDCIssuerURL = NormalizeOIDCIssuerURL(config.OIDCIssuerURL)
	if config.Cloudflare != nil {
		config.Cloudflare.AccountID = strings.TrimSpace(config.Cloudflare.AccountID)
		config.Cloudflare.TokenName = strings.TrimSpace(config.Cloudflare.TokenName)
		config.Cloudflare.TokenRef = strings.TrimSpace(config.Cloudflare.TokenRef)
	}
	if config.ERun != nil {
		config.ERun.APIURL = strings.TrimRight(strings.TrimSpace(config.ERun.APIURL), "/")
		config.ERun.ClientID = strings.TrimSpace(config.ERun.ClientID)
		config.ERun.RefreshTokenRef = strings.TrimSpace(config.ERun.RefreshTokenRef)
	}
	if config.Alias == "" && config.Provider != "" && config.Username != "" && config.AccountID != "" {
		config.Alias = CloudProviderAlias(config.Username, config.AccountID, config.Provider)
	}
	return config
}

func CloudProviderAlias(username, accountID, provider string) string {
	username = strings.TrimSpace(username)
	accountID = strings.TrimSpace(accountID)
	provider = strings.ToLower(strings.TrimSpace(provider))
	if username == "" || accountID == "" || provider == "" {
		return ""
	}
	return username + "+" + accountID + "@" + provider
}

func ListCloudProviders(store configpkg.CloudReadStore) ([]model.CloudProviderConfig, error) {
	if store == nil {
		return nil, fmt.Errorf("store is required")
	}
	config, _, err := store.LoadERunConfig()
	if errors.Is(err, configpkg.ErrNotInitialized) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return normalizedCloudProviders(config.CloudProviders), nil
}

func ResolveCloudProvider(store configpkg.CloudReadStore, alias string) (model.CloudProviderConfig, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return model.CloudProviderConfig{}, fmt.Errorf("cloud provider alias is required")
	}
	providers, err := ListCloudProviders(store)
	if err != nil {
		return model.CloudProviderConfig{}, err
	}
	for _, provider := range providers {
		if provider.Alias == alias {
			return provider, nil
		}
	}
	return model.CloudProviderConfig{}, fmt.Errorf("cloud provider alias %q is not configured", alias)
}

func ResolveTenantCloudProviderIssuers(store configpkg.CloudReadStore, tenant model.TenantConfig) ([]string, error) {
	aliases := configpkg.NormalizedTenantCloudProviderAliases(tenant.CloudProviderAliases)
	if len(aliases) == 0 {
		return nil, nil
	}
	issuers := make([]string, 0, len(aliases))
	seen := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		provider, err := ResolveCloudProvider(store, alias)
		if err != nil {
			return nil, err
		}
		if !cloudProviderSupportsOIDC(provider.Provider) {
			continue
		}
		issuer := CloudProviderOIDCIssuerURL(provider)
		if issuer == "" {
			return nil, fmt.Errorf("cloud provider alias %q does not have an OIDC issuer URL", alias)
		}
		if _, ok := seen[issuer]; ok {
			continue
		}
		issuers = append(issuers, issuer)
		seen[issuer] = struct{}{}
	}
	return issuers, nil
}

// Gate strictly on provider type so an AWS alias missing its issuer still errors.
func cloudProviderSupportsOIDC(provider string) bool {
	return strings.ToLower(strings.TrimSpace(provider)) != configpkg.CloudProviderCloudflare
}

func CloudProviderOIDCIssuerURL(provider model.CloudProviderConfig) string {
	provider = NormalizeCloudProviderConfig(provider)
	if provider.OIDCIssuerURL != "" {
		return provider.OIDCIssuerURL
	}
	startURL := NormalizeOIDCIssuerURL(provider.SSOStartURL)
	if startURL == "" || strings.Contains(startURL, ".awsapps.com/start") || strings.Contains(startURL, ".portal.") {
		return ""
	}
	return startURL
}

// AttachTenantCloudProviderAlias adds alias and makes it primary, so the selection is never
// ambiguous. Existing aliases are kept.
func AttachTenantCloudProviderAlias(aliases []string, primary, alias string) ([]string, string) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return configpkg.NormalizeTenantCloudProviderAliases(aliases, primary)
	}
	attached := append(append(make([]string, 0, len(aliases)+1), aliases...), alias)
	return configpkg.NormalizeTenantCloudProviderAliases(attached, alias)
}

func SaveCloudProviderConfig(store cloudstore.CloudStore, provider model.CloudProviderConfig) (model.CloudProviderConfig, error) {
	if store == nil {
		return model.CloudProviderConfig{}, fmt.Errorf("store is required")
	}
	provider = NormalizeCloudProviderConfig(provider)
	if provider.Alias == "" {
		return model.CloudProviderConfig{}, fmt.Errorf("cloud provider alias is required")
	}
	config, _, err := store.LoadERunConfig()
	if errors.Is(err, configpkg.ErrNotInitialized) {
		config = configpkg.ERunConfig{}
	} else if err != nil {
		return model.CloudProviderConfig{}, err
	}
	config.CloudProviders = UpsertCloudProvider(config.CloudProviders, provider)
	if err := store.SaveERunConfig(config); err != nil {
		return model.CloudProviderConfig{}, err
	}
	return provider, nil
}

func AWSUsernameFromARN(arn string) string {
	arn = strings.TrimSpace(arn)
	if arn == "" {
		return ""
	}
	if idx := strings.LastIndex(arn, "/"); idx >= 0 && idx+1 < len(arn) {
		return arn[idx+1:]
	}
	if idx := strings.LastIndex(arn, ":"); idx >= 0 && idx+1 < len(arn) {
		return arn[idx+1:]
	}
	return arn
}

func UpsertCloudProvider(providers []model.CloudProviderConfig, provider model.CloudProviderConfig) []model.CloudProviderConfig {
	provider = NormalizeCloudProviderConfig(provider)
	updated := false
	result := make([]model.CloudProviderConfig, 0, len(providers)+1)
	for _, existing := range providers {
		existing = NormalizeCloudProviderConfig(existing)
		if existing.Alias == "" {
			continue
		}
		if existing.Alias == provider.Alias {
			result = append(result, provider)
			updated = true
			continue
		}
		result = append(result, existing)
	}
	if !updated {
		result = append(result, provider)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Alias < result[j].Alias
	})
	return result
}

func normalizedCloudProviders(providers []model.CloudProviderConfig) []model.CloudProviderConfig {
	result := make([]model.CloudProviderConfig, 0, len(providers))
	for _, provider := range providers {
		provider = NormalizeCloudProviderConfig(provider)
		if provider.Alias == "" {
			continue
		}
		result = append(result, provider)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Alias < result[j].Alias
	})
	return result
}

func NormalizeOIDCIssuerURL(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, "/.well-known/openid-configuration")
	return strings.TrimRight(value, "/")
}
