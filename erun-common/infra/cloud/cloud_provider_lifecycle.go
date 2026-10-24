package cloud

import (
	"errors"
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

func InitAWSCloudProvider(ctx core.Context, store CloudStore, params model.InitAWSCloudProviderParams, deps CloudDependencies) (model.CloudProviderConfig, error) {
	if store == nil {
		return model.CloudProviderConfig{}, fmt.Errorf("store is required")
	}
	deps = NormalizeCloudDependencies(deps)
	ctx.Trace(fmt.Sprintf("cloud init aws: profile=%s account-id=%s region=%s sso-region=%s sso-start-url=%s skip-login=%v",
		strings.TrimSpace(params.Profile), strings.TrimSpace(params.AccountID),
		strings.TrimSpace(params.Region), strings.TrimSpace(params.SSORegion),
		strings.TrimSpace(params.SSOStartURL), params.SkipLogin))
	profile, err := initAWSProfile(ctx, params, deps)
	if err != nil {
		ctx.Trace("cloud init aws: profile setup failed: " + err.Error())
		return model.CloudProviderConfig{}, err
	}
	ctx.Trace("cloud init aws: profile = " + profile)

	ctx.Trace("cloud init aws: resolving caller identity")
	identity, err := deps.ResolveAWSIdentity(ctx, profile)
	if err != nil {
		ctx.Trace("cloud init aws: identity resolution failed: " + err.Error())
		return model.CloudProviderConfig{}, err
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
	provider := NormalizeCloudProviderConfig(model.CloudProviderConfig{
		Provider:      configpkg.CloudProviderAWS,
		Username:      username,
		AccountID:     accountID,
		Profile:       profile,
		SSORegion:     params.SSORegion,
		SSOStartURL:   params.SSOStartURL,
		OIDCIssuerURL: params.OIDCIssuerURL,
	})
	if provider.Alias == "" {
		return model.CloudProviderConfig{}, fmt.Errorf("cloud provider alias cannot be resolved")
	}
	saved, err := SaveCloudProviderConfig(store, provider)
	if err != nil {
		return model.CloudProviderConfig{}, err
	}
	status, _, err := SetupCloudProviderOIDC(ctx, store, model.CloudBearerParams{Alias: saved.Alias}, deps)
	if err != nil {
		return model.CloudProviderConfig{}, err
	}
	return status.CloudProviderConfig, nil
}

func initAWSProfile(ctx core.Context, params model.InitAWSCloudProviderParams, deps CloudDependencies) (string, error) {
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

func awsProfileConfig(profile string, params model.InitAWSCloudProviderParams) AWSProfileConfig {
	return AWSProfileConfig{
		Profile:     profile,
		SSOStartURL: params.SSOStartURL,
		SSORegion:   params.SSORegion,
		AccountID:   params.AccountID,
		RoleName:    params.RoleName,
		Region:      params.Region,
	}
}

func hasAWSProfileConfig(params model.InitAWSCloudProviderParams) bool {
	return strings.TrimSpace(params.SSOStartURL) != "" ||
		strings.TrimSpace(params.SSORegion) != "" ||
		strings.TrimSpace(params.AccountID) != "" ||
		strings.TrimSpace(params.RoleName) != "" ||
		strings.TrimSpace(params.Region) != ""
}

func LoginCloudProviderAlias(ctx core.Context, store CloudStore, params model.CloudLoginParams, deps CloudDependencies) (model.CloudProviderStatus, error) {
	ctx.Trace(fmt.Sprintf("cloud login: alias=%s force=%v flow=%s", strings.TrimSpace(params.Alias), params.Force, strings.TrimSpace(params.Flow)))
	provider, err := ResolveCloudProvider(store, params.Alias)
	if err != nil {
		ctx.Trace("cloud login: provider lookup failed: " + err.Error())
		return model.CloudProviderStatus{}, err
	}
	ctx.Trace("cloud login: resolved profile = " + provider.Profile)
	deps = NormalizeCloudDependencies(deps)
	status := model.CloudProviderStatus{CloudProviderConfig: provider, Status: configpkg.CloudTokenStatusUnknown}
	if !params.Force {
		status = CloudProviderTokenStatus(provider, deps)
		if status.Status == configpkg.CloudTokenStatusActive {
			return status, nil
		}
	}
	switch provider.Provider {
	case configpkg.CloudProviderAWS:
		if err := deps.RunAWSLogin(ctx, provider.Profile); err != nil {
			return status, err
		}
	case configpkg.CloudProviderCloudflare:
		// Cloudflare has no interactive login.
	case configpkg.CloudProviderERun:
		// Returns its own status: it may have just persisted a new refresh token ref.
		return erunCloudProviderLogin(ctx, store, provider, params.Flow, params.Scopes, deps)
	default:
		return status, fmt.Errorf("unsupported cloud provider %q", provider.Provider)
	}
	return CloudProviderTokenStatus(provider, deps), nil
}

func LogoutCloudProviderAlias(ctx core.Context, store CloudStore, params model.CloudLoginParams, deps CloudDependencies) (model.CloudProviderStatus, error) {
	provider, err := ResolveCloudProvider(store, params.Alias)
	if err != nil {
		return model.CloudProviderStatus{}, err
	}
	deps = NormalizeCloudDependencies(deps)
	status := model.CloudProviderStatus{CloudProviderConfig: provider, Status: configpkg.CloudTokenStatusUnknown}
	switch provider.Provider {
	case configpkg.CloudProviderAWS:
		if err := deps.RunAWSLogout(ctx, provider.Profile); err != nil {
			return status, err
		}
	case configpkg.CloudProviderCloudflare:
		// Cloudflare has no SSO session; logout removes the stored token.
		if err := deleteCloudflareToken(ctx, provider, deps); err != nil {
			return status, err
		}
	case configpkg.CloudProviderERun:
		if err := erunCloudProviderLogout(ctx, provider, deps); err != nil {
			return status, err
		}
	default:
		return status, fmt.Errorf("unsupported cloud provider %q", provider.Provider)
	}
	return CloudProviderTokenStatus(provider, deps), nil
}

func SetupCloudProviderOIDC(ctx core.Context, store CloudStore, params model.CloudBearerParams, deps CloudDependencies) (model.CloudProviderStatus, CloudBearerToken, error) {
	if store == nil {
		return model.CloudProviderStatus{}, CloudBearerToken{}, fmt.Errorf("store is required")
	}
	ctx.Trace("cloud oidc: refresh issuer for alias=" + strings.TrimSpace(params.Alias))
	token, err := CloudProviderBearerToken(ctx, store, params, deps)
	if err != nil {
		ctx.Trace("cloud oidc: bearer token retrieval failed: " + err.Error())
		return model.CloudProviderStatus{}, CloudBearerToken{}, err
	}
	ctx.Trace("cloud oidc: derived issuer = " + token.Issuer)
	provider, err := ResolveCloudProvider(store, token.Alias)
	if err != nil {
		return model.CloudProviderStatus{}, CloudBearerToken{}, err
	}
	provider.OIDCIssuerURL = token.Issuer
	if ctx.DryRun {
		ctx.Trace("write cloud provider OIDC issuer for " + provider.Alias)
		return model.CloudProviderStatus{CloudProviderConfig: provider, Status: configpkg.CloudTokenStatusActive}, token, nil
	}
	provider, err = SaveCloudProviderConfig(store, provider)
	if err != nil {
		return model.CloudProviderStatus{}, CloudBearerToken{}, err
	}
	return CloudProviderTokenStatus(provider, deps), token, nil
}

func CloudProviderBearerToken(ctx core.Context, store configpkg.CloudReadStore, params model.CloudBearerParams, deps CloudDependencies) (CloudBearerToken, error) {
	provider, err := ResolveCloudProvider(store, params.Alias)
	if err != nil {
		return CloudBearerToken{}, err
	}
	deps = NormalizeCloudDependencies(deps)
	switch provider.Provider {
	case configpkg.CloudProviderAWS:
		return awsCloudProviderBearerToken(ctx, provider, params, deps)
	case configpkg.CloudProviderCloudflare:
		return CloudBearerToken{}, ErrCloudProviderNoOIDC
	case configpkg.CloudProviderERun:
		return erunCloudProviderBearerToken(ctx, provider, deps)
	default:
		return CloudBearerToken{}, fmt.Errorf("unsupported cloud provider %q", provider.Provider)
	}
}

func awsCloudProviderBearerToken(ctx core.Context, provider model.CloudProviderConfig, params model.CloudBearerParams, deps CloudDependencies) (CloudBearerToken, error) {
	status := CloudProviderTokenStatus(provider, deps)
	if status.Status != configpkg.CloudTokenStatusActive {
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

func awsBearerTokenWithOIDCRetry(ctx core.Context, provider model.CloudProviderConfig, audience string, deps CloudDependencies) (string, error) {
	rawToken, err := deps.RunAWSBearerToken(ctx, provider.Profile, audience)
	if err != nil && !isAWSOutboundWebIdentityFederationDisabled(err) {
		return "", err
	}
	if err == nil && !ctx.DryRun {
		return rawToken, nil
	}
	// RunAWSBearerToken always succeeds in dry run, so trace the recovery unconditionally.
	if _, enableErr := deps.RunAWSEnableOIDC(ctx, provider.Profile); enableErr != nil {
		return "", enableErr
	}
	return deps.RunAWSBearerToken(ctx, provider.Profile, audience)
}

// ExportCloudProviderCredentials returns short-lived AWS credentials from the alias's host profile.
func ExportCloudProviderCredentials(ctx core.Context, store configpkg.CloudReadStore, alias string, deps CloudDependencies) (CloudProviderCredentials, error) {
	provider, err := ResolveCloudProvider(store, alias)
	if err != nil {
		return CloudProviderCredentials{}, err
	}
	deps = NormalizeCloudDependencies(deps)
	switch provider.Provider {
	case configpkg.CloudProviderAWS:
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

func SetEnvironmentCloudProviderAlias(ctx core.Context, store EnvironmentCloudAliasStore, params model.SetEnvironmentCloudAliasParams) (configpkg.EnvConfig, error) {
	if store == nil {
		return configpkg.EnvConfig{}, fmt.Errorf("store is required")
	}
	tenant, environment, alias, err := normalizeEnvironmentCloudProviderAliasParams(params)
	if err != nil {
		return configpkg.EnvConfig{}, err
	}

	config, _, err := store.LoadEnvConfig(tenant, environment)
	if errors.Is(err, configpkg.ErrNotInitialized) {
		return configpkg.EnvConfig{}, fmt.Errorf("%w: %s", configpkg.ErrEnvironmentNotFound, environment)
	}
	if err != nil {
		return configpkg.EnvConfig{}, err
	}
	if config.Name == "" {
		config.Name = environment
	}
	providerType := configpkg.CloudProviderTypeFromAlias(alias)
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
		return configpkg.EnvConfig{}, err
	}
	return config, nil
}

func normalizeEnvironmentCloudProviderAliasParams(params model.SetEnvironmentCloudAliasParams) (string, string, string, error) {
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

func saveManagedCloudAliasIfNeeded(ctx core.Context, store EnvironmentCloudAliasStore, tenant string, config configpkg.EnvConfig) (configpkg.EnvConfig, error) {
	if !config.RemoteWorktree() || config.ManagedCloud {
		return config, nil
	}
	config.ManagedCloud = true
	if ctx.DryRun {
		return config, nil
	}
	if err := store.SaveEnvConfig(tenant, config); err != nil {
		return configpkg.EnvConfig{}, err
	}
	return config, nil
}

// cloudEnvAliasForType reads AWS from the legacy scalar field, other types from the per-type map.
func cloudEnvAliasForType(config configpkg.EnvConfig, providerType string) string {
	if providerType == configpkg.CloudProviderAWS {
		return config.CloudProviderAlias
	}
	return config.CloudProviderAliases[providerType]
}

// setCloudEnvAliasForType keeps AWS in the legacy scalar field for compatibility.
func setCloudEnvAliasForType(config *configpkg.EnvConfig, providerType, alias string) {
	if providerType == configpkg.CloudProviderAWS {
		config.CloudProviderAlias = alias
		return
	}
	if config.CloudProviderAliases == nil {
		config.CloudProviderAliases = make(map[string]string)
	}
	config.CloudProviderAliases[providerType] = alias
}
