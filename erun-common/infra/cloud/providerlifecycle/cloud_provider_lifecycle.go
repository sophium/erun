package providerlifecycle

import (
	"errors"
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/cloudstore"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/clouddefaults"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
	"github.com/sophium/erun/erun-common/infra/cloud/providertoken"
)

func InitAWSCloudProvider(ctx core.Context, store cloudstore.CloudStore, params model.InitAWSCloudProviderParams, deps cloud.CloudDependencies) (model.CloudProviderConfig, error) {
	if store == nil {
		return model.CloudProviderConfig{}, fmt.Errorf("store is required")
	}
	deps = clouddefaults.NormalizeCloudDependencies(deps)
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
	username := providerconfig.AWSUsernameFromARN(identity.Arn)
	if username == "" {
		username = strings.TrimSpace(params.Username)
	}
	accountID := strings.TrimSpace(identity.Account)
	if accountID == "" {
		accountID = strings.TrimSpace(params.AccountID)
	}
	provider := providerconfig.NormalizeCloudProviderConfig(model.CloudProviderConfig{
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
	saved, err := providerconfig.SaveCloudProviderConfig(store, provider)
	if err != nil {
		return model.CloudProviderConfig{}, err
	}
	status, _, err := SetupCloudProviderOIDC(ctx, store, model.CloudBearerParams{Alias: saved.Alias}, deps)
	if err != nil {
		return model.CloudProviderConfig{}, err
	}
	return status.CloudProviderConfig, nil
}

func initAWSProfile(ctx core.Context, params model.InitAWSCloudProviderParams, deps cloud.CloudDependencies) (string, error) {
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

func awsProfileConfig(profile string, params model.InitAWSCloudProviderParams) cloud.AWSProfileConfig {
	return cloud.AWSProfileConfig{
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

func LoginCloudProviderAlias(ctx core.Context, store cloudstore.CloudStore, params model.CloudLoginParams, deps cloud.CloudDependencies) (model.CloudProviderStatus, error) {
	ctx.Trace(fmt.Sprintf("cloud login: alias=%s force=%v flow=%s", strings.TrimSpace(params.Alias), params.Force, strings.TrimSpace(params.Flow)))
	provider, err := providerconfig.ResolveCloudProvider(store, params.Alias)
	if err != nil {
		ctx.Trace("cloud login: provider lookup failed: " + err.Error())
		return model.CloudProviderStatus{}, err
	}
	ctx.Trace("cloud login: resolved profile = " + provider.Profile)
	deps = clouddefaults.NormalizeCloudDependencies(deps)
	status := model.CloudProviderStatus{CloudProviderConfig: provider, Status: configpkg.CloudTokenStatusUnknown}
	if !params.Force {
		status = providertoken.CloudProviderTokenStatus(provider, deps)
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
	return providertoken.CloudProviderTokenStatus(provider, deps), nil
}

func LogoutCloudProviderAlias(ctx core.Context, store cloudstore.CloudStore, params model.CloudLoginParams, deps cloud.CloudDependencies) (model.CloudProviderStatus, error) {
	provider, err := providerconfig.ResolveCloudProvider(store, params.Alias)
	if err != nil {
		return model.CloudProviderStatus{}, err
	}
	deps = clouddefaults.NormalizeCloudDependencies(deps)
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
	return providertoken.CloudProviderTokenStatus(provider, deps), nil
}

func SetupCloudProviderOIDC(ctx core.Context, store cloudstore.CloudStore, params model.CloudBearerParams, deps cloud.CloudDependencies) (model.CloudProviderStatus, providertoken.CloudBearerToken, error) {
	if store == nil {
		return model.CloudProviderStatus{}, providertoken.CloudBearerToken{}, fmt.Errorf("store is required")
	}
	ctx.Trace("cloud oidc: refresh issuer for alias=" + strings.TrimSpace(params.Alias))
	token, err := providertoken.CloudProviderBearerToken(ctx, store, params, deps)
	if err != nil {
		ctx.Trace("cloud oidc: bearer token retrieval failed: " + err.Error())
		return model.CloudProviderStatus{}, providertoken.CloudBearerToken{}, err
	}
	ctx.Trace("cloud oidc: derived issuer = " + token.Issuer)
	provider, err := providerconfig.ResolveCloudProvider(store, token.Alias)
	if err != nil {
		return model.CloudProviderStatus{}, providertoken.CloudBearerToken{}, err
	}
	provider.OIDCIssuerURL = token.Issuer
	if ctx.DryRun {
		ctx.Trace("write cloud provider OIDC issuer for " + provider.Alias)
		return model.CloudProviderStatus{CloudProviderConfig: provider, Status: configpkg.CloudTokenStatusActive}, token, nil
	}
	provider, err = providerconfig.SaveCloudProviderConfig(store, provider)
	if err != nil {
		return model.CloudProviderStatus{}, providertoken.CloudBearerToken{}, err
	}
	return providertoken.CloudProviderTokenStatus(provider, deps), token, nil
}

func SetEnvironmentCloudProviderAlias(ctx core.Context, store cloudstore.EnvironmentCloudAliasStore, params model.SetEnvironmentCloudAliasParams) (configpkg.EnvConfig, error) {
	if store == nil {
		return configpkg.EnvConfig{}, fmt.Errorf("store is required")
	}
	tenant, environment, alias, err := normalizeEnvironmentCloudProviderAliasParams(params)
	if err != nil {
		return configpkg.EnvConfig{}, err
	}

	config, _, err := store.LoadEnvConfig(tenant, environment)
	if errors.Is(err, configpkg.ErrNotInitialized) {
		return configpkg.EnvConfig{}, fmt.Errorf("%w: %s", runtimetarget.ErrEnvironmentNotFound, environment)
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

func saveManagedCloudAliasIfNeeded(ctx core.Context, store cloudstore.EnvironmentCloudAliasStore, tenant string, config configpkg.EnvConfig) (configpkg.EnvConfig, error) {
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
