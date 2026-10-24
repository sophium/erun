package cloud

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// ErrCloudProviderNoOIDC marks a provider (Cloudflare) that has no OIDC issuer, so
// issuer aggregation skips it.
var ErrCloudProviderNoOIDC = errors.New("cloud provider does not support OIDC issuance")

type CloudStore interface {
	configpkg.CloudReadStore
	SaveERunConfig(configpkg.ERunConfig) error
}

type EnvironmentCloudAliasStore interface {
	LoadEnvConfig(string, string) (configpkg.EnvConfig, string, error)
	SaveEnvConfig(string, configpkg.EnvConfig) error
}

type AWSIdentity struct {
	Account string `json:"Account"`
	Arn     string `json:"Arn"`
	UserID  string `json:"UserId"`
}

// Auto prefers the device grant and falls back to auth code + PKCE, so one unusable
// method never locks the CLI out.
const (
	ERunLoginFlowAuto     = "auto"
	ERunLoginFlowDevice   = "device"
	ERunLoginFlowAuthCode = "authcode"
)

// NormalizeERunLoginFlow validates a flow and resolves empty to auto.
func NormalizeERunLoginFlow(flow string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(flow)) {
	case "", ERunLoginFlowAuto:
		return ERunLoginFlowAuto, nil
	case ERunLoginFlowDevice:
		return ERunLoginFlowDevice, nil
	case ERunLoginFlowAuthCode:
		return ERunLoginFlowAuthCode, nil
	default:
		return "", fmt.Errorf("unsupported login flow %q: expected %q, %q or %q", strings.TrimSpace(flow), ERunLoginFlowAuto, ERunLoginFlowDevice, ERunLoginFlowAuthCode)
	}
}

type CloudBearerToken struct {
	Alias    string
	Provider string
	Token    string
	Issuer   string
}

type CloudDependencies struct {
	RunAWSConfigureSSO      func(core.Context, AWSProfileConfig) error
	RunAWSLogin             func(core.Context, string) error
	RunAWSLogout            func(core.Context, string) error
	RunAWSBearerToken       func(core.Context, string, string) (string, error)
	RunAWSEnableOIDC        func(core.Context, string) (string, error)
	RunAWSExportCredentials func(core.Context, string) (CloudProviderCredentials, error)
	ResolveAWSIdentity      func(core.Context, string) (AWSIdentity, error)
	CheckAWSStatus          func(core.Context, model.CloudProviderConfig) model.CloudProviderStatus

	// CloudSecretStore is nil unless a transport wires one.
	VerifyCloudflareToken  func(core.Context, string) (CloudflareTokenInfo, error)
	ListCloudflareAccounts func(core.Context, string) ([]CloudflareAccount, error)
	CloudSecretStore       CloudSecretStore

	FetchPlatformInfo            func(core.Context, string) (model.PlatformInfo, error)
	FetchOIDCDiscovery           func(core.Context, string) (OIDCDiscovery, error)
	StartERunDeviceAuthorization func(core.Context, OIDCDiscovery, string, string) (ERunDeviceAuthorization, error)
	PollERunDeviceToken          func(core.Context, OIDCDiscovery, string, ERunDeviceAuthorization) (ERunTokens, error)
	RunERunAuthCodeLogin         func(core.Context, OIDCDiscovery, string, string) (ERunTokens, error)
	RefreshERunTokens            func(core.Context, OIDCDiscovery, string, string) (ERunTokens, error)
	ClientCredentialsERunTokens  func(core.Context, OIDCDiscovery, string, string) (ERunTokens, error)

	// FetchVersionJSON reads a surface's unauthenticated /version.json.
	FetchVersionJSON func(core.Context, string) (string, error)
	// ResolveHostAddrs lets drift detection tell a benign canonical alias from a different backend.
	ResolveHostAddrs func(core.Context, string) ([]string, error)
}

// DefaultCloudDependencies sets CloudSecretStore when the default store can be built and
// leaves it nil otherwise; every transport must use it.
func DefaultCloudDependencies() CloudDependencies {
	deps := CloudDependencies{}
	if store, err := DefaultCloudSecretStore(); err == nil {
		deps.CloudSecretStore = store
	}
	return deps
}

// CloudProviderCredentials are temporary (~1h) AWS credentials for a remote runtime;
// callers must refresh.
type CloudProviderCredentials struct {
	Alias           string    `json:"alias"`
	AccessKeyID     string    `json:"accessKeyId"`
	SecretAccessKey string    `json:"secretAccessKey"`
	SessionToken    string    `json:"sessionToken"`
	Expiration      time.Time `json:"expiration"`
}

type AWSProfileConfig struct {
	Profile     string
	SSOStartURL string
	SSORegion   string
	AccountID   string
	RoleName    string
	Region      string
}

func NormalizeCloudProviderConfig(config model.CloudProviderConfig) model.CloudProviderConfig {
	config.Alias = strings.TrimSpace(config.Alias)
	config.Provider = strings.ToLower(strings.TrimSpace(config.Provider))
	config.Username = strings.TrimSpace(config.Username)
	config.AccountID = strings.TrimSpace(config.AccountID)
	config.Profile = strings.TrimSpace(config.Profile)
	config.SSORegion = strings.TrimSpace(config.SSORegion)
	config.SSOStartURL = strings.TrimSpace(config.SSOStartURL)
	config.OIDCIssuerURL = normalizeOIDCIssuerURL(config.OIDCIssuerURL)
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

func ListCloudProviderStatuses(store configpkg.CloudReadStore, deps CloudDependencies) ([]model.CloudProviderStatus, error) {
	providers, err := ListCloudProviders(store)
	if err != nil {
		return nil, err
	}
	statuses := make([]model.CloudProviderStatus, 0, len(providers))
	for _, provider := range providers {
		statuses = append(statuses, CloudProviderTokenStatus(provider, deps))
	}
	return statuses, nil
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
	startURL := normalizeOIDCIssuerURL(provider.SSOStartURL)
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

func SaveCloudProviderConfig(store CloudStore, provider model.CloudProviderConfig) (model.CloudProviderConfig, error) {
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

func CloudProviderTokenStatus(provider model.CloudProviderConfig, deps CloudDependencies) model.CloudProviderStatus {
	provider = NormalizeCloudProviderConfig(provider)
	deps = NormalizeCloudDependencies(deps)
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
	return defaultCheckAWSStatus(core.Context{}, provider)
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

func normalizeOIDCIssuerURL(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, "/.well-known/openid-configuration")
	return strings.TrimRight(value, "/")
}

func normalizeCloudBearerAudience(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return configpkg.CloudProviderBearerAudience
	}
	return value
}

func issuerFromJWT(token string) (string, error) {
	token = strings.TrimSpace(token)
	parts := strings.Split(token, ".")
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
	issuer := normalizeOIDCIssuerURL(claims.Issuer)
	if issuer == "" {
		return "", fmt.Errorf("cloud provider bearer token does not include issuer claim")
	}
	return issuer, nil
}
