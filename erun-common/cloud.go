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
	CloudProviderAWS = "aws"

	CloudProviderBearerAudience = "erun-api"

	CloudTokenStatusActive        = "active"
	CloudTokenStatusExpired       = "expired"
	CloudTokenStatusNotConfigured = "not_configured"
	CloudTokenStatusUnknown       = "unknown"
)

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
	RunAWSConfigureSSO func(Context, AWSProfileConfig) error
	RunAWSLogin        func(Context, string) error
	RunAWSLogout       func(Context, string) error
	RunAWSBearerToken  func(Context, string, string) (string, error)
	RunAWSEnableOIDC   func(Context, string) (string, error)
	ResolveAWSIdentity func(Context, string) (AWSIdentity, error)
	CheckAWSStatus     func(Context, CloudProviderConfig) CloudProviderStatus
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
	profile, err := initAWSProfile(ctx, params, deps)
	if err != nil {
		return CloudProviderConfig{}, err
	}

	identity, err := deps.ResolveAWSIdentity(ctx, profile)
	if err != nil {
		return CloudProviderConfig{}, err
	}
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
	provider, err := ResolveCloudProvider(store, params.Alias)
	if err != nil {
		return CloudProviderStatus{}, err
	}
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
	default:
		return status, fmt.Errorf("unsupported cloud provider %q", provider.Provider)
	}
	return CloudProviderTokenStatus(provider, deps), nil
}

func SetupCloudProviderOIDC(ctx Context, store CloudStore, params CloudBearerParams, deps CloudDependencies) (CloudProviderStatus, CloudBearerToken, error) {
	if store == nil {
		return CloudProviderStatus{}, CloudBearerToken{}, fmt.Errorf("store is required")
	}
	token, err := CloudProviderBearerToken(ctx, store, params, deps)
	if err != nil {
		return CloudProviderStatus{}, CloudBearerToken{}, err
	}
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
		status := CloudProviderTokenStatus(provider, deps)
		if status.Status != CloudTokenStatusActive {
			if err := deps.RunAWSLogin(ctx, provider.Profile); err != nil {
				return CloudBearerToken{}, err
			}
		}
		audience := normalizeCloudBearerAudience(params.Audience)
		rawToken, err := deps.RunAWSBearerToken(ctx, provider.Profile, audience)
		if err != nil {
			if !isAWSOutboundWebIdentityFederationDisabled(err) {
				return CloudBearerToken{}, err
			}
			if _, enableErr := deps.RunAWSEnableOIDC(ctx, provider.Profile); enableErr != nil {
				return CloudBearerToken{}, enableErr
			}
			rawToken, err = deps.RunAWSBearerToken(ctx, provider.Profile, audience)
			if err != nil {
				return CloudBearerToken{}, err
			}
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
	default:
		return CloudBearerToken{}, fmt.Errorf("unsupported cloud provider %q", provider.Provider)
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
	if config.CloudProviderAlias == alias {
		return saveManagedCloudAliasIfNeeded(ctx, store, tenant, config)
	}
	config.CloudProviderAlias = alias
	if config.Remote {
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
		return "", "", "", fmt.Errorf("tenant is required")
	case environment == "":
		return "", "", "", fmt.Errorf("environment is required")
	case alias == "":
		return "", "", "", fmt.Errorf("cloud provider alias is required")
	default:
		return tenant, environment, alias, nil
	}
}

func saveManagedCloudAliasIfNeeded(ctx Context, store EnvironmentCloudAliasStore, tenant string, config EnvConfig) (EnvConfig, error) {
	if !config.Remote || config.ManagedCloud {
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
	if deps.ResolveAWSIdentity == nil {
		deps.ResolveAWSIdentity = defaultResolveAWSIdentity
	}
	if deps.CheckAWSStatus == nil {
		deps.CheckAWSStatus = defaultCheckAWSStatus
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
		return fmt.Errorf("AWS role name is required")
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
