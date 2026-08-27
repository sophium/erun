package eruncommon

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const (
	CloudProviderAWS        = "aws"
	CloudProviderCloudflare = "cloudflare"
	CloudProviderERun       = "erun"

	CloudProviderBearerAudience = "erun-api"

	CloudTokenStatusActive        = "active"
	CloudTokenStatusExpired       = "expired"
	CloudTokenStatusNotConfigured = "not_configured"
	CloudTokenStatusUnknown       = "unknown"
)

// ErrCloudProviderNoOIDC reports that a provider type does not participate in
// OIDC web-identity federation, so callers aggregating issuers skip it rather
// than fail. Cloudflare authenticates with a scoped API token, not a JWT issuer.
var ErrCloudProviderNoOIDC = errors.New("cloud provider does not support OIDC issuance")

type CloudStore interface {
	CloudReadStore
	SaveERunConfig(ERunConfig) error
}

type EnvironmentCloudAliasStore interface {
	LoadEnvConfig(string, string) (EnvConfig, string, error)
	SaveEnvConfig(string, EnvConfig) error
}

type CloudReadStore interface {
	LoadERunConfig() (ERunConfig, string, error)
}

type CloudProviderConfig struct {
	Alias         string `json:"alias" yaml:"alias"`
	Provider      string `json:"provider" yaml:"provider"`
	Username      string `json:"username,omitempty" yaml:"username,omitempty"`
	AccountID     string `json:"accountId,omitempty" yaml:"accountid,omitempty"`
	Profile       string `json:"profile,omitempty" yaml:"profile,omitempty"`
	SSORegion     string `json:"ssoRegion,omitempty" yaml:"ssoregion,omitempty"`
	SSOStartURL   string `json:"ssoStartUrl,omitempty" yaml:"ssostarturl,omitempty"`
	OIDCIssuerURL string `json:"oidcIssuerUrl,omitempty" yaml:"oidcissuerurl,omitempty"`

	// Cloudflare is set only when Provider == CloudProviderCloudflare; the
	// scoped API token is never stored inline, only a reference to the secret
	// store.
	Cloudflare *CloudflareProviderConfig `json:"cloudflare,omitempty" yaml:"cloudflare,omitempty"`

	// ERun is set only when Provider == CloudProviderERun; the refresh token is
	// never stored inline, only a reference to the secret store.
	ERun *ERunProviderConfig `json:"erun,omitempty" yaml:"erun,omitempty"`
}

// CloudflareProviderConfig is the Cloudflare-specific identity for a cloud
// alias. The raw scoped API token never lands in erun-config.yaml; only
// TokenRef, an opaque handle the secret store resolves, is persisted.
type CloudflareProviderConfig struct {
	AccountID string `json:"accountId,omitempty" yaml:"accountid,omitempty"`
	TokenName string `json:"tokenName,omitempty" yaml:"tokenname,omitempty"`
	TokenRef  string `json:"tokenRef,omitempty" yaml:"tokenref,omitempty"`
}

type CloudProviderStatus struct {
	CloudProviderConfig `json:",inline" yaml:",inline"`
	Status              string `json:"status" yaml:"status"`
	Message             string `json:"message,omitempty" yaml:"message,omitempty"`
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
	RunAWSConfigureSSO      func(Context, AWSProfileConfig) error
	RunAWSLogin             func(Context, string) error
	RunAWSLogout            func(Context, string) error
	RunAWSBearerToken       func(Context, string, string) (string, error)
	RunAWSEnableOIDC        func(Context, string) (string, error)
	RunAWSExportCredentials func(Context, string) (CloudProviderCredentials, error)
	ResolveAWSIdentity      func(Context, string) (AWSIdentity, error)
	CheckAWSStatus          func(Context, CloudProviderConfig) CloudProviderStatus

	// CloudSecretStore is nil unless a transport wires one; Cloudflare and erun
	// operations that need it fail clearly when it is absent.
	VerifyCloudflareToken  func(Context, string) (CloudflareTokenInfo, error)
	ListCloudflareAccounts func(Context, string) ([]CloudflareAccount, error)
	CloudSecretStore       CloudSecretStore

	// erun cloud provider: platform/OIDC discovery, the device authorization
	// grant, the Authorization Code + PKCE fallback, and the refresh_token
	// grant. See cloud_erun.go / cloud_erun_oidc.go.
	FetchPlatformInfo            func(Context, string) (PlatformInfo, error)
	FetchOIDCDiscovery           func(Context, string) (OIDCDiscovery, error)
	StartERunDeviceAuthorization func(Context, OIDCDiscovery, string) (ERunDeviceAuthorization, error)
	PollERunDeviceToken          func(Context, OIDCDiscovery, string, ERunDeviceAuthorization) (ERunTokens, error)
	RunERunAuthCodeLogin         func(Context, OIDCDiscovery, string) (ERunTokens, error)
	RefreshERunTokens            func(Context, OIDCDiscovery, string, string) (ERunTokens, error)
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

func NormalizeCloudProviderConfig(config CloudProviderConfig) CloudProviderConfig {
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

// ParseCloudProviderAlias is the inverse of CloudProviderAlias. The doctor's
// root-config repair flow uses it to reseed setup from a dangling alias when
// the CloudProviderConfig was lost from erun-config but a tenant or
// cloud-context still names it; ok=false means the alias cannot be auto-repaired.
func ParseCloudProviderAlias(alias string) (username, accountID, provider string, ok bool) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return "", "", "", false
	}
	at := strings.LastIndex(alias, "@")
	if at <= 0 || at == len(alias)-1 {
		return "", "", "", false
	}
	left := alias[:at]
	provider = strings.ToLower(strings.TrimSpace(alias[at+1:]))
	plus := strings.LastIndex(left, "+")
	if plus <= 0 || plus == len(left)-1 {
		return "", "", "", false
	}
	username = strings.TrimSpace(left[:plus])
	accountID = strings.TrimSpace(left[plus+1:])
	if username == "" || accountID == "" || provider == "" {
		return "", "", "", false
	}
	return username, accountID, provider, true
}

func ListCloudProviders(store CloudReadStore) ([]CloudProviderConfig, error) {
	if store == nil {
		return nil, fmt.Errorf("store is required")
	}
	config, _, err := store.LoadERunConfig()
	if errors.Is(err, ErrNotInitialized) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return normalizedCloudProviders(config.CloudProviders), nil
}

func ListCloudProviderStatuses(store CloudReadStore, deps CloudDependencies) ([]CloudProviderStatus, error) {
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

func InitAWSCloudProvider(ctx Context, store CloudStore, params InitAWSCloudProviderParams, deps CloudDependencies) (CloudProviderConfig, error) {
	if store == nil {
		return CloudProviderConfig{}, fmt.Errorf("store is required")
	}
	deps = normalizeCloudDependencies(deps)
	ctx.Trace(fmt.Sprintf("cloud init aws: profile=%s account-id=%s region=%s sso-region=%s sso-start-url=%s skip-login=%v",
		strings.TrimSpace(params.Profile), strings.TrimSpace(params.AccountID),
		strings.TrimSpace(params.Region), strings.TrimSpace(params.SSORegion),
		strings.TrimSpace(params.SSOStartURL), params.SkipLogin))
	profile, err := initAWSProfile(ctx, params, deps)
	if err != nil {
		ctx.Trace("cloud init aws: profile setup failed: " + err.Error())
		return CloudProviderConfig{}, err
	}
	ctx.Trace("cloud init aws: profile = " + profile)

	ctx.Trace("cloud init aws: resolving caller identity")
	identity, err := deps.ResolveAWSIdentity(ctx, profile)
	if err != nil {
		ctx.Trace("cloud init aws: identity resolution failed: " + err.Error())
		return CloudProviderConfig{}, err
	}
	ctx.Trace(fmt.Sprintf("cloud init aws: identity account=%s arn=%s", identity.Account, identity.Arn))
	username := AWSUsernameFromARN(identity.Arn)
	if username == "" {
		username = strings.TrimSpace(params.Username)
	}
	accountID := strings.TrimSpace(identity.Account)
	if accountID == "" {
		accountID = strings.TrimSpace(params.AccountID)
	}
	provider := NormalizeCloudProviderConfig(CloudProviderConfig{
		Provider:      CloudProviderAWS,
		Username:      username,
		AccountID:     accountID,
		Profile:       profile,
		SSORegion:     params.SSORegion,
		SSOStartURL:   params.SSOStartURL,
		OIDCIssuerURL: params.OIDCIssuerURL,
	})
	if provider.Alias == "" {
		return CloudProviderConfig{}, fmt.Errorf("cloud provider alias cannot be resolved")
	}
	saved, err := SaveCloudProviderConfig(store, provider)
	if err != nil {
		return CloudProviderConfig{}, err
	}
	status, _, err := SetupCloudProviderOIDC(ctx, store, CloudBearerParams{Alias: saved.Alias}, deps)
	if err != nil {
		return CloudProviderConfig{}, err
	}
	return status.CloudProviderConfig, nil
}

func initAWSProfile(ctx Context, params InitAWSCloudProviderParams, deps CloudDependencies) (string, error) {
	profile := strings.TrimSpace(params.Profile)
	configureProfile := profile == "" || hasAWSProfileConfig(params)
	if profile == "" {
		profile = generatedAWSProfileName()
	}
	if configureProfile {
		if err := deps.RunAWSConfigureSSO(ctx, awsProfileConfig(profile, params)); err != nil {
			return "", err
		}
	}
	if !params.SkipLogin {
		if err := deps.RunAWSLogin(ctx, profile); err != nil {
			return "", err
		}
	}
	return profile, nil
}

func awsProfileConfig(profile string, params InitAWSCloudProviderParams) AWSProfileConfig {
	return AWSProfileConfig{
		Profile:     profile,
		SSOStartURL: params.SSOStartURL,
		SSORegion:   params.SSORegion,
		AccountID:   params.AccountID,
		RoleName:    params.RoleName,
		Region:      params.Region,
	}
}

func hasAWSProfileConfig(params InitAWSCloudProviderParams) bool {
	return strings.TrimSpace(params.SSOStartURL) != "" ||
		strings.TrimSpace(params.SSORegion) != "" ||
		strings.TrimSpace(params.AccountID) != "" ||
		strings.TrimSpace(params.RoleName) != "" ||
		strings.TrimSpace(params.Region) != ""
}

func LoginCloudProviderAlias(ctx Context, store CloudStore, params CloudLoginParams, deps CloudDependencies) (CloudProviderStatus, error) {
	ctx.Trace(fmt.Sprintf("cloud login: alias=%s force=%v", strings.TrimSpace(params.Alias), params.Force))
	provider, err := ResolveCloudProvider(store, params.Alias)
	if err != nil {
		ctx.Trace("cloud login: provider lookup failed: " + err.Error())
		return CloudProviderStatus{}, err
	}
	ctx.Trace("cloud login: resolved profile = " + provider.Profile)
	deps = normalizeCloudDependencies(deps)
	status := CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusUnknown}
	if !params.Force {
		status = CloudProviderTokenStatus(provider, deps)
		if status.Status == CloudTokenStatusActive {
			return status, nil
		}
	}
	switch provider.Provider {
	case CloudProviderAWS:
		if err := deps.RunAWSLogin(ctx, provider.Profile); err != nil {
			return status, err
		}
	case CloudProviderCloudflare:
		// Cloudflare has no interactive login; fall through to the token
		// status re-check.
	case CloudProviderERun:
		// erunCloudProviderLogin resolves its own final status (it may have
		// just persisted a new refresh token ref onto provider), so it returns
		// directly rather than falling through to the stale pre-login value.
		return erunCloudProviderLogin(ctx, store, provider, deps)
	default:
		return status, fmt.Errorf("unsupported cloud provider %q", provider.Provider)
	}
	return CloudProviderTokenStatus(provider, deps), nil
}

func LogoutCloudProviderAlias(ctx Context, store CloudStore, params CloudLoginParams, deps CloudDependencies) (CloudProviderStatus, error) {
	provider, err := ResolveCloudProvider(store, params.Alias)
	if err != nil {
		return CloudProviderStatus{}, err
	}
	deps = normalizeCloudDependencies(deps)
	status := CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusUnknown}
	switch provider.Provider {
	case CloudProviderAWS:
		if err := deps.RunAWSLogout(ctx, provider.Profile); err != nil {
			return status, err
		}
	case CloudProviderCloudflare:
		// Cloudflare has no SSO session; "logout" removes the stored token so
		// the alias keeps its identity but loses its credential.
		if err := deleteCloudflareToken(ctx, provider, deps); err != nil {
			return status, err
		}
	case CloudProviderERun:
		if err := erunCloudProviderLogout(ctx, provider, deps); err != nil {
			return status, err
		}
	default:
		return status, fmt.Errorf("unsupported cloud provider %q", provider.Provider)
	}
	return CloudProviderTokenStatus(provider, deps), nil
}

func SetupCloudProviderOIDC(ctx Context, store CloudStore, params CloudBearerParams, deps CloudDependencies) (CloudProviderStatus, CloudBearerToken, error) {
	if store == nil {
		return CloudProviderStatus{}, CloudBearerToken{}, fmt.Errorf("store is required")
	}
	ctx.Trace("cloud oidc: refresh issuer for alias=" + strings.TrimSpace(params.Alias))
	token, err := CloudProviderBearerToken(ctx, store, params, deps)
	if err != nil {
		ctx.Trace("cloud oidc: bearer token retrieval failed: " + err.Error())
		return CloudProviderStatus{}, CloudBearerToken{}, err
	}
	ctx.Trace("cloud oidc: derived issuer = " + token.Issuer)
	provider, err := ResolveCloudProvider(store, token.Alias)
	if err != nil {
		return CloudProviderStatus{}, CloudBearerToken{}, err
	}
	provider.OIDCIssuerURL = token.Issuer
	if ctx.DryRun {
		ctx.Trace("write cloud provider OIDC issuer for " + provider.Alias)
		return CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusActive}, token, nil
	}
	provider, err = SaveCloudProviderConfig(store, provider)
	if err != nil {
		return CloudProviderStatus{}, CloudBearerToken{}, err
	}
	return CloudProviderTokenStatus(provider, deps), token, nil
}

func CloudProviderBearerToken(ctx Context, store CloudReadStore, params CloudBearerParams, deps CloudDependencies) (CloudBearerToken, error) {
	provider, err := ResolveCloudProvider(store, params.Alias)
	if err != nil {
		return CloudBearerToken{}, err
	}
	deps = normalizeCloudDependencies(deps)
	switch provider.Provider {
	case CloudProviderAWS:
		return awsCloudProviderBearerToken(ctx, provider, params, deps)
	case CloudProviderCloudflare:
		return CloudBearerToken{}, ErrCloudProviderNoOIDC
	case CloudProviderERun:
		return erunCloudProviderBearerToken(ctx, provider, deps)
	default:
		return CloudBearerToken{}, fmt.Errorf("unsupported cloud provider %q", provider.Provider)
	}
}

func awsCloudProviderBearerToken(ctx Context, provider CloudProviderConfig, params CloudBearerParams, deps CloudDependencies) (CloudBearerToken, error) {
	status := CloudProviderTokenStatus(provider, deps)
	if status.Status != CloudTokenStatusActive {
		if err := deps.RunAWSLogin(ctx, provider.Profile); err != nil {
			return CloudBearerToken{}, err
		}
	}
	audience := normalizeCloudBearerAudience(params.Audience)
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

func awsBearerTokenWithOIDCRetry(ctx Context, provider CloudProviderConfig, audience string, deps CloudDependencies) (string, error) {
	rawToken, err := deps.RunAWSBearerToken(ctx, provider.Profile, audience)
	if err == nil {
		return rawToken, nil
	}
	if !isAWSOutboundWebIdentityFederationDisabled(err) {
		return "", err
	}
	if _, enableErr := deps.RunAWSEnableOIDC(ctx, provider.Profile); enableErr != nil {
		return "", enableErr
	}
	return deps.RunAWSBearerToken(ctx, provider.Profile, audience)
}

// ExportCloudProviderCredentials returns short-lived AWS credentials derived
// from the host profile registered under alias. The desktop uses these to keep
// a remote runtime's `~/.aws/credentials` refreshed so it acts as the host
// identity; a failure typically means the SSO session expired and needs
// `erun cloud login`.
func ExportCloudProviderCredentials(ctx Context, store CloudReadStore, alias string, deps CloudDependencies) (CloudProviderCredentials, error) {
	provider, err := ResolveCloudProvider(store, alias)
	if err != nil {
		return CloudProviderCredentials{}, err
	}
	deps = normalizeCloudDependencies(deps)
	switch provider.Provider {
	case CloudProviderAWS:
		creds, err := deps.RunAWSExportCredentials(ctx, provider.Profile)
		if err != nil {
			return CloudProviderCredentials{}, err
		}
		creds.Alias = provider.Alias
		return creds, nil
	default:
		return CloudProviderCredentials{}, fmt.Errorf("unsupported cloud provider %q", provider.Provider)
	}
}

func SetEnvironmentCloudProviderAlias(ctx Context, store EnvironmentCloudAliasStore, params SetEnvironmentCloudAliasParams) (EnvConfig, error) {
	if store == nil {
		return EnvConfig{}, fmt.Errorf("store is required")
	}
	tenant, environment, alias, err := normalizeEnvironmentCloudProviderAliasParams(params)
	if err != nil {
		return EnvConfig{}, err
	}

	config, _, err := store.LoadEnvConfig(tenant, environment)
	if errors.Is(err, ErrNotInitialized) {
		return EnvConfig{}, fmt.Errorf("%w: %s", ErrEnvironmentNotFound, environment)
	}
	if err != nil {
		return EnvConfig{}, err
	}
	if config.Name == "" {
		config.Name = environment
	}
	providerType := cloudProviderTypeFromAlias(alias)
	if cloudEnvAliasForType(config, providerType) == alias {
		return saveManagedCloudAliasIfNeeded(ctx, store, tenant, config)
	}
	setCloudEnvAliasForType(&config, providerType, alias)
	if config.RemoteWorktree() {
		config.ManagedCloud = true
	}
	if ctx.DryRun {
		ctx.Trace("write erun environment cloud provider alias " + tenant + "/" + environment)
		return config, nil
	}
	if err := store.SaveEnvConfig(tenant, config); err != nil {
		return EnvConfig{}, err
	}
	return config, nil
}

func normalizeEnvironmentCloudProviderAliasParams(params SetEnvironmentCloudAliasParams) (string, string, string, error) {
	tenant := strings.TrimSpace(params.Tenant)
	environment := strings.TrimSpace(params.Environment)
	alias := strings.TrimSpace(params.Alias)
	switch {
	case tenant == "":
		return "", "", "", fmt.Errorf("set cloud provider alias: no tenant given — pass one explicitly (`erun cloud set <tenant> <environment> --alias <alias>`, or the cloud_set tool's tenant field)")
	case environment == "":
		return "", "", "", fmt.Errorf("set cloud provider alias for %s: no environment given — pass one explicitly (`erun cloud set %s <environment> --alias <alias>`, or the cloud_set tool's environment field)", tenant, tenant)
	case alias == "":
		return "", "", "", fmt.Errorf("set cloud provider alias for %s/%s: no alias given — pass one explicitly (`erun cloud set %s %s --alias <alias>`, or the cloud_set tool's alias field)", tenant, environment, tenant, environment)
	default:
		return tenant, environment, alias, nil
	}
}

func saveManagedCloudAliasIfNeeded(ctx Context, store EnvironmentCloudAliasStore, tenant string, config EnvConfig) (EnvConfig, error) {
	if !config.RemoteWorktree() || config.ManagedCloud {
		return config, nil
	}
	config.ManagedCloud = true
	if ctx.DryRun {
		return config, nil
	}
	if err := store.SaveEnvConfig(tenant, config); err != nil {
		return EnvConfig{}, err
	}
	return config, nil
}

// cloudProviderTypeFromAlias defaults to AWS for any legacy or unparseable
// alias so pre-existing single-alias configs keep resolving to the AWS slot.
func cloudProviderTypeFromAlias(alias string) string {
	if _, _, provider, ok := ParseCloudProviderAlias(alias); ok {
		return provider
	}
	return CloudProviderAWS
}

// cloudEnvAliasForType reads AWS from the legacy scalar field and every other
// provider type from the per-type map.
func cloudEnvAliasForType(config EnvConfig, providerType string) string {
	if providerType == CloudProviderAWS {
		return config.CloudProviderAlias
	}
	return config.CloudProviderAliases[providerType]
}

// setCloudEnvAliasForType keeps AWS in the legacy scalar field so AWS behavior
// stays byte-for-byte unchanged; other types go in the per-type map.
func setCloudEnvAliasForType(config *EnvConfig, providerType, alias string) {
	if providerType == CloudProviderAWS {
		config.CloudProviderAlias = alias
		return
	}
	if config.CloudProviderAliases == nil {
		config.CloudProviderAliases = make(map[string]string)
	}
	config.CloudProviderAliases[providerType] = alias
}

func ResolveCloudProvider(store CloudReadStore, alias string) (CloudProviderConfig, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return CloudProviderConfig{}, fmt.Errorf("cloud provider alias is required")
	}
	providers, err := ListCloudProviders(store)
	if err != nil {
		return CloudProviderConfig{}, err
	}
	for _, provider := range providers {
		if provider.Alias == alias {
			return provider, nil
		}
	}
	return CloudProviderConfig{}, fmt.Errorf("cloud provider alias %q is not configured", alias)
}

func ResolveTenantCloudProviderIssuers(store CloudReadStore, tenant TenantConfig) ([]string, error) {
	aliases := normalizedTenantCloudProviderAliases(tenant.CloudProviderAliases)
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
	return strings.ToLower(strings.TrimSpace(provider)) != CloudProviderCloudflare
}

func CloudProviderOIDCIssuerURL(provider CloudProviderConfig) string {
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

func NormalizeTenantCloudProviderAliases(aliases []string, primary string) ([]string, string) {
	normalized := normalizedTenantCloudProviderAliases(aliases)
	primary = strings.TrimSpace(primary)
	if len(normalized) == 0 {
		return nil, ""
	}
	if primary == "" || !cloudAliasListContains(normalized, primary) {
		primary = normalized[0]
	}
	return normalized, primary
}

func SaveCloudProviderConfig(store CloudStore, provider CloudProviderConfig) (CloudProviderConfig, error) {
	if store == nil {
		return CloudProviderConfig{}, fmt.Errorf("store is required")
	}
	provider = NormalizeCloudProviderConfig(provider)
	if provider.Alias == "" {
		return CloudProviderConfig{}, fmt.Errorf("cloud provider alias is required")
	}
	config, _, err := store.LoadERunConfig()
	if errors.Is(err, ErrNotInitialized) {
		config = ERunConfig{}
	} else if err != nil {
		return CloudProviderConfig{}, err
	}
	config.CloudProviders = upsertCloudProvider(config.CloudProviders, provider)
	if err := store.SaveERunConfig(config); err != nil {
		return CloudProviderConfig{}, err
	}
	return provider, nil
}

func CloudProviderTokenStatus(provider CloudProviderConfig, deps CloudDependencies) CloudProviderStatus {
	provider = NormalizeCloudProviderConfig(provider)
	deps = normalizeCloudDependencies(deps)
	if provider.Provider == "" {
		return CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusNotConfigured, Message: "provider is not configured"}
	}
	if deps.CheckAWSStatus != nil && provider.Provider == CloudProviderAWS {
		return deps.CheckAWSStatus(Context{}, provider)
	}
	if provider.Provider == CloudProviderCloudflare {
		return cloudflareCloudProviderTokenStatus(provider, deps)
	}
	if provider.Provider == CloudProviderERun {
		return erunCloudProviderTokenStatus(provider, deps)
	}
	if provider.Provider != CloudProviderAWS {
		return CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusUnknown, Message: "unsupported provider"}
	}
	return defaultCheckAWSStatus(Context{}, provider)
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

func upsertCloudProvider(providers []CloudProviderConfig, provider CloudProviderConfig) []CloudProviderConfig {
	provider = NormalizeCloudProviderConfig(provider)
	updated := false
	result := make([]CloudProviderConfig, 0, len(providers)+1)
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

func normalizedCloudProviders(providers []CloudProviderConfig) []CloudProviderConfig {
	result := make([]CloudProviderConfig, 0, len(providers))
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

func normalizedTenantCloudProviderAliases(aliases []string) []string {
	result := make([]string, 0, len(aliases))
	seen := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		result = append(result, alias)
		seen[alias] = struct{}{}
	}
	sort.Strings(result)
	return result
}

func cloudAliasListContains(aliases []string, alias string) bool {
	for _, candidate := range aliases {
		if candidate == alias {
			return true
		}
	}
	return false
}

func normalizeOIDCIssuerURL(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, "/.well-known/openid-configuration")
	return strings.TrimRight(value, "/")
}

func normalizeCloudBearerAudience(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return CloudProviderBearerAudience
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

func normalizeCloudDependencies(deps CloudDependencies) CloudDependencies {
	deps = normalizeAWSCloudDependencies(deps)
	deps = normalizeCloudflareCloudDependencies(deps)
	deps = normalizeERunCloudDependencies(deps)
	return deps
}

// normalizeERunCloudDependencies leaves CloudSecretStore nil on purpose: only
// a transport wires one, and erun operations that need it fail clearly when
// it is absent (same convention as Cloudflare).
func normalizeERunCloudDependencies(deps CloudDependencies) CloudDependencies {
	if deps.FetchPlatformInfo == nil {
		deps.FetchPlatformInfo = defaultFetchPlatformInfo
	}
	if deps.FetchOIDCDiscovery == nil {
		deps.FetchOIDCDiscovery = defaultFetchOIDCDiscovery
	}
	if deps.StartERunDeviceAuthorization == nil {
		deps.StartERunDeviceAuthorization = defaultStartERunDeviceAuthorization
	}
	if deps.PollERunDeviceToken == nil {
		deps.PollERunDeviceToken = pollERunDeviceToken
	}
	if deps.RunERunAuthCodeLogin == nil {
		deps.RunERunAuthCodeLogin = runERunAuthorizationCodeLogin
	}
	if deps.RefreshERunTokens == nil {
		deps.RefreshERunTokens = defaultRefreshERunTokens
	}
	return deps
}

func normalizeAWSCloudDependencies(deps CloudDependencies) CloudDependencies {
	if deps.RunAWSConfigureSSO == nil {
		deps.RunAWSConfigureSSO = defaultRunAWSConfigureSSO
	}
	if deps.RunAWSLogin == nil {
		deps.RunAWSLogin = defaultRunAWSLogin
	}
	if deps.RunAWSLogout == nil {
		deps.RunAWSLogout = defaultRunAWSLogout
	}
	if deps.RunAWSBearerToken == nil {
		deps.RunAWSBearerToken = defaultRunAWSBearerToken
	}
	if deps.RunAWSEnableOIDC == nil {
		deps.RunAWSEnableOIDC = defaultRunAWSEnableOIDC
	}
	if deps.RunAWSExportCredentials == nil {
		deps.RunAWSExportCredentials = defaultRunAWSExportCredentials
	}
	if deps.ResolveAWSIdentity == nil {
		deps.ResolveAWSIdentity = defaultResolveAWSIdentity
	}
	if deps.CheckAWSStatus == nil {
		deps.CheckAWSStatus = defaultCheckAWSStatus
	}
	return deps
}

// normalizeCloudflareCloudDependencies leaves CloudSecretStore nil on purpose:
// only a transport wires one, and Cloudflare operations that need it fail
// clearly when it is absent.
func normalizeCloudflareCloudDependencies(deps CloudDependencies) CloudDependencies {
	if deps.VerifyCloudflareToken == nil {
		deps.VerifyCloudflareToken = defaultVerifyCloudflareToken
	}
	if deps.ListCloudflareAccounts == nil {
		deps.ListCloudflareAccounts = defaultListCloudflareAccounts
	}
	return deps
}

func defaultRunAWSConfigureSSO(ctx Context, config AWSProfileConfig) error {
	config = normalizeAWSProfileConfig(config)
	if err := validateAWSProfileConfig(config); err != nil {
		return err
	}
	settings := []struct {
		key   string
		value string
	}{
		{key: "sso_start_url", value: config.SSOStartURL},
		{key: "sso_region", value: config.SSORegion},
		{key: "sso_account_id", value: config.AccountID},
		{key: "sso_role_name", value: config.RoleName},
		{key: "region", value: config.Region},
		{key: "output", value: "json"},
	}
	for _, setting := range settings {
		args := []string{"configure", "set", setting.key, setting.value, "--profile", config.Profile}
		ctx.TraceCommand("", "aws", args...)
		if ctx.DryRun {
			continue
		}
		stdout, _ := captureWriter(ctx.Stdout)
		stderr, stderrBuffer := captureWriter(ctx.Stderr)
		if err := RawCommandRunner("", "aws", args, nil, stdout, stderr); err != nil {
			return fmt.Errorf("aws configure set %s: %s", setting.key, commandErrorMessage(err, stderrBuffer.String(), "AWS SSO setup failed"))
		}
	}
	return nil
}

func normalizeAWSProfileConfig(config AWSProfileConfig) AWSProfileConfig {
	config.Profile = strings.TrimSpace(config.Profile)
	config.SSOStartURL = strings.TrimSpace(config.SSOStartURL)
	config.SSORegion = strings.TrimSpace(config.SSORegion)
	config.AccountID = strings.TrimSpace(config.AccountID)
	config.RoleName = strings.TrimSpace(config.RoleName)
	config.Region = strings.TrimSpace(config.Region)
	if config.Region == "" {
		config.Region = config.SSORegion
	}
	return config
}

func validateAWSProfileConfig(config AWSProfileConfig) error {
	switch {
	case config.Profile == "":
		return fmt.Errorf("AWS profile name is required")
	case config.SSOStartURL == "":
		return fmt.Errorf("AWS SSO start URL is required")
	case config.SSORegion == "":
		return fmt.Errorf("AWS SSO region is required")
	case config.AccountID == "":
		return fmt.Errorf("AWS account ID is required")
	case config.RoleName == "":
		return fmt.Errorf("AWS permission set is required")
	case config.Region == "":
		return fmt.Errorf("AWS region is required")
	default:
		return nil
	}
}

func defaultRunAWSLogin(ctx Context, profile string) error {
	args := []string{"sso", "login"}
	if strings.TrimSpace(profile) != "" {
		args = append(args, "--profile", strings.TrimSpace(profile))
	}
	ctx.TraceCommand("", "aws", args...)
	if ctx.DryRun {
		return nil
	}
	stdout, _ := captureWriter(ctx.Stdout)
	stderr, stderrBuffer := captureWriter(ctx.Stderr)
	if err := RawCommandRunner("", "aws", args, ctx.Stdin, stdout, stderr); err != nil {
		return fmt.Errorf("aws sso login: %s", commandErrorMessage(err, stderrBuffer.String(), "AWS SSO login failed"))
	}
	return nil
}

func defaultRunAWSLogout(ctx Context, profile string) error {
	args := []string{"sso", "logout"}
	if strings.TrimSpace(profile) != "" {
		args = append(args, "--profile", strings.TrimSpace(profile))
	}
	ctx.TraceCommand("", "aws", args...)
	if ctx.DryRun {
		return nil
	}
	stdout, _ := captureWriter(ctx.Stdout)
	stderr, stderrBuffer := captureWriter(ctx.Stderr)
	if err := RawCommandRunner("", "aws", args, ctx.Stdin, stdout, stderr); err != nil {
		return fmt.Errorf("aws sso logout: %s", commandErrorMessage(err, stderrBuffer.String(), "AWS SSO logout failed"))
	}
	return nil
}

func defaultRunAWSBearerToken(ctx Context, profile, audience string) (string, error) {
	audience = normalizeCloudBearerAudience(audience)
	args := []string{
		"sts", "get-web-identity-token",
		"--audience", audience,
		"--signing-algorithm", "RS256",
		"--duration-seconds", "900",
		"--query", "WebIdentityToken",
		"--output", "text",
	}
	if strings.TrimSpace(profile) != "" {
		args = append(args, "--profile", strings.TrimSpace(profile))
	}
	ctx.TraceCommand("", "aws", args...)
	if ctx.DryRun {
		return "", nil
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := RawCommandRunner("", "aws", args, nil, &stdout, &stderr); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("get AWS web identity token: %s", message)
	}
	token := strings.TrimSpace(stdout.String())
	if token == "" {
		return "", fmt.Errorf("get AWS web identity token: empty token")
	}
	return token, nil
}

func defaultRunAWSExportCredentials(ctx Context, profile string) (CloudProviderCredentials, error) {
	args := []string{"configure", "export-credentials", "--format", "process"}
	if strings.TrimSpace(profile) != "" {
		args = append(args, "--profile", strings.TrimSpace(profile))
	}
	ctx.TraceCommand("", "aws", args...)
	if ctx.DryRun {
		return CloudProviderCredentials{}, nil
	}
	var stdout, stderr bytes.Buffer
	if err := RawCommandRunner("", "aws", args, nil, &stdout, &stderr); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return CloudProviderCredentials{}, fmt.Errorf("export AWS credentials: %s", message)
	}
	return parseAWSExportCredentials(stdout.Bytes())
}

func parseAWSExportCredentials(raw []byte) (CloudProviderCredentials, error) {
	var payload struct {
		Version         int    `json:"Version"`
		AccessKeyID     string `json:"AccessKeyId"`
		SecretAccessKey string `json:"SecretAccessKey"`
		SessionToken    string `json:"SessionToken"`
		Expiration      string `json:"Expiration"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(raw), &payload); err != nil {
		return CloudProviderCredentials{}, fmt.Errorf("parse AWS exported credentials: %w", err)
	}
	if payload.AccessKeyID == "" || payload.SecretAccessKey == "" {
		return CloudProviderCredentials{}, fmt.Errorf("AWS exported credentials are missing access key id or secret")
	}
	creds := CloudProviderCredentials{
		AccessKeyID:     payload.AccessKeyID,
		SecretAccessKey: payload.SecretAccessKey,
		SessionToken:    payload.SessionToken,
	}
	if expiration := strings.TrimSpace(payload.Expiration); expiration != "" {
		parsed, err := time.Parse(time.RFC3339, expiration)
		if err != nil {
			return CloudProviderCredentials{}, fmt.Errorf("parse AWS credential expiration %q: %w", expiration, err)
		}
		creds.Expiration = parsed
	}
	return creds, nil
}

func defaultRunAWSEnableOIDC(ctx Context, profile string) (string, error) {
	args := []string{
		"iam", "enable-outbound-web-identity-federation",
		"--query", "IssuerIdentifier",
		"--output", "text",
	}
	if strings.TrimSpace(profile) != "" {
		args = append(args, "--profile", strings.TrimSpace(profile))
	}
	ctx.TraceCommand("", "aws", args...)
	if ctx.DryRun {
		return "", nil
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := RawCommandRunner("", "aws", args, nil, &stdout, &stderr); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		if isAWSOutboundWebIdentityFederationAlreadyEnabledMessage(message) {
			return "", nil
		}
		return "", fmt.Errorf("enable AWS outbound web identity federation: %s", message)
	}
	return normalizeOIDCIssuerURL(stdout.String()), nil
}

func isAWSOutboundWebIdentityFederationDisabled(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "outboundwebidentityfederationdisabledexception") ||
		strings.Contains(message, "outboundwebidentityfederation is disabled") ||
		strings.Contains(message, "outbound web identity federation is disabled")
}

func isAWSOutboundWebIdentityFederationAlreadyEnabledMessage(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "featureenabled") ||
		strings.Contains(message, "outbound identity federation is already enabled") ||
		strings.Contains(message, "outbound web identity federation is already enabled")
}

func generatedAWSProfileName() string {
	return "erun-sso-" + time.Now().UTC().Format("20060102150405")
}

func defaultResolveAWSIdentity(ctx Context, profile string) (AWSIdentity, error) {
	args := []string{"sts", "get-caller-identity", "--output", "json"}
	if strings.TrimSpace(profile) != "" {
		args = append(args, "--profile", strings.TrimSpace(profile))
	}
	ctx.TraceCommand("", "aws", args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := RawCommandRunner("", "aws", args, nil, &stdout, &stderr); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return AWSIdentity{}, fmt.Errorf("resolve AWS identity: %s", message)
	}
	var identity AWSIdentity
	if err := json.Unmarshal(stdout.Bytes(), &identity); err != nil {
		return AWSIdentity{}, fmt.Errorf("parse AWS identity: %w", err)
	}
	return identity, nil
}

func defaultCheckAWSStatus(_ Context, provider CloudProviderConfig) CloudProviderStatus {
	args := []string{"sts", "get-caller-identity", "--output", "json"}
	if provider.Profile != "" {
		args = append(args, "--profile", provider.Profile)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := RawCommandRunner("", "aws", args, nil, &stdout, &stderr)
	if err == nil {
		return CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusActive}
	}
	message := strings.TrimSpace(stderr.String())
	if message == "" {
		message = strings.TrimSpace(err.Error())
	}
	if errors.Is(err, exec.ErrNotFound) {
		return CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusUnknown, Message: "aws CLI is not installed"}
	}
	status := CloudTokenStatusExpired
	if strings.Contains(strings.ToLower(message), "could not be found") || strings.Contains(strings.ToLower(message), "not found") {
		status = CloudTokenStatusNotConfigured
	}
	return CloudProviderStatus{CloudProviderConfig: provider, Status: status, Message: message}
}

func captureWriter(writer io.Writer) (io.Writer, *bytes.Buffer) {
	buffer := new(bytes.Buffer)
	if writer == nil {
		return buffer, buffer
	}
	return io.MultiWriter(writer, buffer), buffer
}

func commandErrorMessage(err error, stderr, fallback string) string {
	message := strings.TrimSpace(stderr)
	if message != "" {
		return message
	}
	if fallback != "" {
		return fallback + ": " + err.Error()
	}
	return err.Error()
}
