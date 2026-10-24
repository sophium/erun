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
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// ErrCloudProviderNoOIDC reports that a provider type does not participate in
// OIDC web-identity federation, so callers aggregating issuers skip it rather
// than fail. Cloudflare authenticates with a scoped API token, not a JWT issuer.
var ErrCloudProviderNoOIDC = errors.New("cloud provider does not support OIDC issuance")

type CloudStore interface {
	configpkg.CloudReadStore
	SaveERunConfig(configpkg.ERunConfig) error
}

type EnvironmentCloudAliasStore interface {
	LoadEnvConfig(string, string) (configpkg.EnvConfig, string, error)
	SaveEnvConfig(string, configpkg.EnvConfig) error
}

type CloudProviderStatus struct {
	configpkg.CloudProviderConfig `json:",inline" yaml:",inline"`
	Status                        string `json:"status" yaml:"status"`
	Message                       string `json:"message,omitempty" yaml:"message,omitempty"`
}

type AWSIdentity struct {
	Account string `json:"Account"`
	Arn     string `json:"Arn"`
	UserID  string `json:"UserId"`
}

type InitAWSCloudProviderParams struct {
	Profile       string
	Username      string
	AccountID     string
	RoleName      string
	Region        string
	SSORegion     string
	SSOStartURL   string
	OIDCIssuerURL string
	SkipLogin     bool
}

type CloudLoginParams struct {
	Alias string
	Force bool
	// Scopes are extra OAuth scopes to request on top of the baseline
	// "openid offline_access". A provider's reserved scopes are often not
	// advertised in discovery, so they can only be asked for by name — e.g.
	// urn:zitadel:iam:user:resourceowner, which is what makes a Zitadel token
	// carry the org claim an org-scoped issuer resolves tenants by.
	Scopes []string
	// Flow selects the OIDC grant used for an erun-hosted alias:
	// ERunLoginFlowDevice, ERunLoginFlowAuthCode, or empty/ERunLoginFlowAuto.
	// Ignored by every other provider, which has only one login path.
	Flow string
}

// The OIDC grants an erun-hosted alias can sign in with. Auto is the default:
// it prefers the device grant when the issuer advertises one, and falls back to
// authorization code + PKCE when that grant cannot complete — a single
// unusable authentication method must not lock the CLI out when a second,
// working flow exists.
const (
	ERunLoginFlowAuto     = "auto"
	ERunLoginFlowDevice   = "device"
	ERunLoginFlowAuthCode = "authcode"
)

// NormalizeERunLoginFlow validates a requested flow and resolves the empty
// value to auto. The error names the accepted values, since this is operator
// input from a flag or a tool argument.
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

type CloudBearerParams struct {
	Alias    string
	Audience string
}

type CloudBearerToken struct {
	Alias    string
	Provider string
	Token    string
	Issuer   string
}

type SetEnvironmentCloudAliasParams struct {
	Tenant      string
	Environment string
	Alias       string
}

type CloudDependencies struct {
	RunAWSConfigureSSO      func(core.Context, AWSProfileConfig) error
	RunAWSLogin             func(core.Context, string) error
	RunAWSLogout            func(core.Context, string) error
	RunAWSBearerToken       func(core.Context, string, string) (string, error)
	RunAWSEnableOIDC        func(core.Context, string) (string, error)
	RunAWSExportCredentials func(core.Context, string) (CloudProviderCredentials, error)
	ResolveAWSIdentity      func(core.Context, string) (AWSIdentity, error)
	CheckAWSStatus          func(core.Context, configpkg.CloudProviderConfig) CloudProviderStatus

	// CloudSecretStore is nil unless a transport wires one; Cloudflare and erun
	// operations that need it fail clearly when it is absent.
	VerifyCloudflareToken  func(core.Context, string) (CloudflareTokenInfo, error)
	ListCloudflareAccounts func(core.Context, string) ([]CloudflareAccount, error)
	CloudSecretStore       CloudSecretStore

	// erun cloud provider: platform/OIDC discovery, the device authorization
	// grant, the Authorization Code + PKCE fallback, and the refresh_token
	// grant. See cloud_erun.go / cloud_erun_oidc.go.
	FetchPlatformInfo            func(core.Context, string) (platformclient.PlatformInfo, error)
	FetchOIDCDiscovery           func(core.Context, string) (OIDCDiscovery, error)
	StartERunDeviceAuthorization func(core.Context, OIDCDiscovery, string, string) (ERunDeviceAuthorization, error)
	PollERunDeviceToken          func(core.Context, OIDCDiscovery, string, ERunDeviceAuthorization) (ERunTokens, error)
	RunERunAuthCodeLogin         func(core.Context, OIDCDiscovery, string, string) (ERunTokens, error)
	RefreshERunTokens            func(core.Context, OIDCDiscovery, string, string) (ERunTokens, error)
	ClientCredentialsERunTokens  func(core.Context, OIDCDiscovery, string, string) (ERunTokens, error)

	// FetchVersionJSON resolves a deployed version surface's own build version
	// (GET <surfaceURL>/version.json, unauthenticated) -- a control plane's
	// console and its docs site both answer it the same way. See
	// control_plane_version_drift.go.
	FetchVersionJSON func(core.Context, string) (string, error)
	// ResolveHostAddrs resolves a hostname to its IP addresses -- used by
	// control_plane_version_drift.go to tell a plane's own discovery document
	// naming a benign canonical alias apart from one advertising a genuinely
	// different backend.
	ResolveHostAddrs func(core.Context, string) ([]string, error)
}

// DefaultCloudDependencies returns a CloudDependencies with CloudSecretStore
// set to the default file-backed store when one can be constructed there,
// left nil otherwise. Every transport needs this same tolerant-nil
// construction (the store's directory may not exist, or may be otherwise
// unavailable) — call this from each rather than open-coding it again, which
// is how the listing path went a full release without it.
// Cloudflare/erun operations that need the store fail clearly downstream when
// it stays nil, rather than here.
func DefaultCloudDependencies() CloudDependencies {
	deps := CloudDependencies{}
	if store, err := DefaultCloudSecretStore(); err == nil {
		deps.CloudSecretStore = store
	}
	return deps
}

// CloudProviderCredentials is a snapshot of temporary AWS credentials derived
// from a configured host profile, for injecting into a remote runtime so it
// acts as the host's IAM identity. AWS hands out roughly 1h windows, so
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

func NormalizeCloudProviderConfig(config configpkg.CloudProviderConfig) configpkg.CloudProviderConfig {
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

func ListCloudProviders(store configpkg.CloudReadStore) ([]configpkg.CloudProviderConfig, error) {
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

func ListCloudProviderStatuses(store configpkg.CloudReadStore, deps CloudDependencies) ([]CloudProviderStatus, error) {
	providers, err := ListCloudProviders(store)
	if err != nil {
		return nil, err
	}
	statuses := make([]CloudProviderStatus, 0, len(providers))
	for _, provider := range providers {
		statuses = append(statuses, CloudProviderTokenStatus(provider, deps))
	}
	return statuses, nil
}

func ResolveCloudProvider(store configpkg.CloudReadStore, alias string) (configpkg.CloudProviderConfig, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return configpkg.CloudProviderConfig{}, fmt.Errorf("cloud provider alias is required")
	}
	providers, err := ListCloudProviders(store)
	if err != nil {
		return configpkg.CloudProviderConfig{}, err
	}
	for _, provider := range providers {
		if provider.Alias == alias {
			return provider, nil
		}
	}
	return configpkg.CloudProviderConfig{}, fmt.Errorf("cloud provider alias %q is not configured", alias)
}

func ResolveTenantCloudProviderIssuers(store configpkg.CloudReadStore, tenant configpkg.TenantConfig) ([]string, error) {
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
			// Cloudflare aliases authenticate the runtime with a scoped API
			// token, not an OIDC JWT, so they contribute no issuer. Skip them
			// rather than fail the tenant's issuer aggregation.
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

// cloudProviderSupportsOIDC gates issuer aggregation strictly on provider type
// (only Cloudflare is exempt) so an AWS alias with a genuinely missing issuer
// still surfaces as an error instead of being silently skipped.
func cloudProviderSupportsOIDC(provider string) bool {
	return strings.ToLower(strings.TrimSpace(provider)) != configpkg.CloudProviderCloudflare
}

func CloudProviderOIDCIssuerURL(provider configpkg.CloudProviderConfig) string {
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

// AttachTenantCloudProviderAlias attaches alias to a tenant's own cloud
// provider selection, making it the tenant's primary at the same time: the
// tenant-scoped platform resolution reads the tenant's own selection and, with
// more than one erun alias there and no primary naming one, has to render the
// choose-alias state instead. An attach is therefore never an ambiguous
// selection — the alias just attached is the one the caller meant.
//
// Existing aliases are preserved: this adds to a selection the operator built
// elsewhere, and pruning a different provider's alias here would silently
// revoke an attachment this call knows nothing about.
func AttachTenantCloudProviderAlias(aliases []string, primary, alias string) ([]string, string) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return configpkg.NormalizeTenantCloudProviderAliases(aliases, primary)
	}
	attached := append(append(make([]string, 0, len(aliases)+1), aliases...), alias)
	return configpkg.NormalizeTenantCloudProviderAliases(attached, alias)
}

func SaveCloudProviderConfig(store CloudStore, provider configpkg.CloudProviderConfig) (configpkg.CloudProviderConfig, error) {
	if store == nil {
		return configpkg.CloudProviderConfig{}, fmt.Errorf("store is required")
	}
	provider = NormalizeCloudProviderConfig(provider)
	if provider.Alias == "" {
		return configpkg.CloudProviderConfig{}, fmt.Errorf("cloud provider alias is required")
	}
	config, _, err := store.LoadERunConfig()
	if errors.Is(err, configpkg.ErrNotInitialized) {
		config = configpkg.ERunConfig{}
	} else if err != nil {
		return configpkg.CloudProviderConfig{}, err
	}
	config.CloudProviders = UpsertCloudProvider(config.CloudProviders, provider)
	if err := store.SaveERunConfig(config); err != nil {
		return configpkg.CloudProviderConfig{}, err
	}
	return provider, nil
}

func CloudProviderTokenStatus(provider configpkg.CloudProviderConfig, deps CloudDependencies) CloudProviderStatus {
	provider = NormalizeCloudProviderConfig(provider)
	deps = NormalizeCloudDependencies(deps)
	if provider.Provider == "" {
		return CloudProviderStatus{CloudProviderConfig: provider, Status: configpkg.CloudTokenStatusNotConfigured, Message: "provider is not configured"}
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
		return CloudProviderStatus{CloudProviderConfig: provider, Status: configpkg.CloudTokenStatusUnknown, Message: "unsupported provider"}
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

func UpsertCloudProvider(providers []configpkg.CloudProviderConfig, provider configpkg.CloudProviderConfig) []configpkg.CloudProviderConfig {
	provider = NormalizeCloudProviderConfig(provider)
	updated := false
	result := make([]configpkg.CloudProviderConfig, 0, len(providers)+1)
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

func normalizedCloudProviders(providers []configpkg.CloudProviderConfig) []configpkg.CloudProviderConfig {
	result := make([]configpkg.CloudProviderConfig, 0, len(providers))
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
