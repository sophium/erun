package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/platform"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/mcpauth"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// Platform identity resolves like `erun platform` does, never from an
// environment's apiUrl or the tenant's primary cloud alias.

// Each state maps to exactly one next action.
const (
	tenantPlatformStateReady        = ""
	tenantPlatformStateNotConnected = "not-connected"
	tenantPlatformStateChooseAlias  = "choose-alias"
	tenantPlatformStateNotSignedIn  = "not-signed-in"
	tenantPlatformStateNotEnrolled  = "not-enrolled"
	tenantPlatformStateNoPermission = "no-permission"
	// tenantPlatformStateTenantMismatch: the platform's tenant is not the local one,
	// so its rows must not render under the local name.
	tenantPlatformStateTenantMismatch = "tenant-mismatch"
)

type tenantPlatformResolution struct {
	state        string
	client       *platformclient.PlatformClient
	apiURL       string
	alias        string
	aliasChoices []string
	issuer       string
	subject      string
}

// resolveTenantPlatform only uses an erun alias the tenant attached, never a
// global one, so one tenant never authenticates with another's credential. The
// API URL comes from the alias, never TenantConfig.APIURL, which would let it
// redirect the bearer token.
func (a *App) resolveTenantPlatform(tenant, aliasOverride string) (tenantPlatformResolution, error) {
	tenantConfig, _, err := a.deps.store.LoadTenantConfig(tenant)
	if err != nil && !errors.Is(err, configpkg.ErrNotInitialized) {
		return tenantPlatformResolution{}, err
	}

	provider, terminal, err := a.resolveTenantERunPlatformAlias(tenantConfig, strings.TrimSpace(aliasOverride))
	if err != nil {
		return tenantPlatformResolution{}, err
	}
	if terminal != nil {
		return *terminal, nil
	}
	if provider.ERun == nil || strings.TrimSpace(provider.ERun.APIURL) == "" {
		// An erun provider with no ERun block is a config-write defect fixed by `erun cloud login`.
		return tenantPlatformResolution{}, fmt.Errorf("erun platform alias %q is incomplete (its erun api configuration is missing); run `erun cloud login %s` to restore it", provider.Alias, provider.Alias)
	}
	apiURL := provider.ERun.APIURL

	bearer, ok := a.tenantPlatformBearer(provider.Alias)
	if !ok {
		return tenantPlatformResolution{state: tenantPlatformStateNotSignedIn, alias: provider.Alias, apiURL: apiURL}, nil
	}

	issuer, _ := mcpauth.IssuerFromUnverifiedJWT(bearer)
	subject, _ := mcpauth.SubjectFromUnverifiedJWT(bearer)
	client := platformclient.NewPlatformClient(apiURL, func() (string, error) { return bearer, nil }).
		WithUsernameHint(a.tenantDashboardUsernameHint(provider.Alias))
	return tenantPlatformResolution{
		state:   tenantPlatformStateReady,
		client:  client,
		apiURL:  apiURL,
		alias:   provider.Alias,
		issuer:  issuer,
		subject: subject,
	}, nil
}

func (a *App) tenantPlatformBearer(alias string) (string, bool) {
	token, err := cloud.CloudProviderBearerToken(core.Context{}, a.deps.store, model.CloudBearerParams{Alias: alias}, a.deps.cloudDeps)
	bearer := strings.TrimSpace(token.Token)
	return bearer, err == nil && bearer != ""
}

// resolveTenantERunPlatformAlias honors an explicit aliasOverride verbatim, like
// `erun platform --erun-alias`. A tenant with no attached aliases falls back to
// the sole global erun alias; one with aliases never falls back outside them.
func (a *App) resolveTenantERunPlatformAlias(tenantConfig model.TenantConfig, aliasOverride string) (provider model.CloudProviderConfig, terminal *tenantPlatformResolution, err error) {
	if aliasOverride != "" || len(tenantConfig.CloudProviderAliases) == 0 {
		return a.resolveGlobalERunPlatformAlias(aliasOverride)
	}

	tenantERunAliases := a.tenantSelectedERunAliases(tenantConfig)
	if len(tenantERunAliases) == 0 {
		return model.CloudProviderConfig{}, &tenantPlatformResolution{state: tenantPlatformStateNotConnected}, nil
	}
	chosen := chooseTenantERunAlias(tenantERunAliases, tenantConfig.PrimaryCloudProviderAlias)
	if chosen == "" {
		return model.CloudProviderConfig{}, &tenantPlatformResolution{state: tenantPlatformStateChooseAlias, aliasChoices: tenantERunAliases}, nil
	}
	provider, err = cloud.ResolveCloudProvider(a.deps.store, chosen)
	if err != nil {
		return model.CloudProviderConfig{}, nil, err
	}
	return provider, nil, nil
}

func (a *App) resolveGlobalERunPlatformAlias(aliasOverride string) (model.CloudProviderConfig, *tenantPlatformResolution, error) {
	provider, err := shared.ResolveERunPlatformAlias(a.deps.store, aliasOverride)
	if err != nil {
		resolution, resolveErr := a.tenantPlatformResolutionForResolveError(err)
		return model.CloudProviderConfig{}, &resolution, resolveErr
	}
	return provider, nil, nil
}

// Stale alias names are skipped.
func (a *App) tenantSelectedERunAliases(tenantConfig model.TenantConfig) []string {
	var erunAliases []string
	for _, alias := range tenantConfig.CloudProviderAliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		candidate, err := cloud.ResolveCloudProvider(a.deps.store, alias)
		if err != nil || candidate.Provider != configpkg.CloudProviderERun {
			continue
		}
		erunAliases = append(erunAliases, alias)
	}
	return erunAliases
}

// chooseTenantERunAlias returns "" when ambiguous.
func chooseTenantERunAlias(candidates []string, primary string) string {
	primary = strings.TrimSpace(primary)
	for _, alias := range candidates {
		if alias == primary {
			return alias
		}
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	return ""
}

// Errors other than none/ambiguous are caller-wiring bugs and surface as errors.
func (a *App) tenantPlatformResolutionForResolveError(resolveErr error) (tenantPlatformResolution, error) {
	aliases, err := a.configuredERunAliases()
	if err != nil {
		return tenantPlatformResolution{}, err
	}
	switch len(aliases) {
	case 0:
		return tenantPlatformResolution{state: tenantPlatformStateNotConnected}, nil
	case 1:
		return tenantPlatformResolution{}, resolveErr
	default:
		return tenantPlatformResolution{state: tenantPlatformStateChooseAlias, aliasChoices: aliases}, nil
	}
}

// tenantPlatformClient is for paths reachable only after the dashboard resolved,
// so a not-ready state is a plain error here.
func (a *App) tenantPlatformClient(ctx context.Context, tenant string) (*platformclient.PlatformClient, context.Context, context.CancelFunc, error) {
	resolution, err := a.resolveTenantPlatform(tenant, "")
	if err != nil {
		return nil, nil, nil, err
	}
	if resolution.state != tenantPlatformStateReady {
		return nil, nil, nil, fmt.Errorf("tenant platform is not ready (%s); reopen the dashboard to resolve it", resolution.state)
	}
	requestCtx, cancel := context.WithTimeout(ctx, tenantDashboardTimeout)
	return resolution.client, requestCtx, cancel, nil
}

// Tenant is empty only from the machine-wide settings dialog; otherwise the alias
// must land in the tenant's selection, since a non-empty selection ignores global aliases.
type uiConnectERunPlatformInput struct {
	APIURL string `json:"apiUrl"`
	Tenant string `json:"tenant,omitempty"`
}

// ConnectERunPlatform attaches a hosted erun platform alias to the tenant the click came from.
func (a *App) ConnectERunPlatform(input uiConnectERunPlatformInput) (uiCloudProviderStatus, error) {
	provider, err := cloud.InitERunCloudProvider(core.Context{}, a.deps.store, model.InitERunCloudProviderParams{
		APIURL: strings.TrimSpace(input.APIURL),
	}, a.deps.cloudDeps)
	if err != nil {
		return uiCloudProviderStatus{}, err
	}
	if tenant := strings.TrimSpace(input.Tenant); tenant != "" {
		if err := a.attachTenantCloudProviderAlias(tenant, provider.Alias); err != nil {
			return uiCloudProviderStatus{}, err
		}
	}
	return cloudProviderStatusToUI(cloud.CloudProviderTokenStatus(provider, a.deps.cloudDeps)), nil
}

// A tenant with no config yet gets one naming just this alias.
func (a *App) attachTenantCloudProviderAlias(tenant, alias string) error {
	config, _, err := a.deps.store.LoadTenantConfig(tenant)
	if err != nil && !errors.Is(err, configpkg.ErrNotInitialized) {
		return err
	}
	config.Name = tenant
	config.CloudProviderAliases, config.PrimaryCloudProviderAlias = cloud.AttachTenantCloudProviderAlias(
		config.CloudProviderAliases, config.PrimaryCloudProviderAlias, alias)
	return a.deps.store.SaveTenantConfig(config)
}

type uiPlatformUserEnrollInput struct {
	Alias    string `json:"alias"`
	Username string `json:"username"`
	Issuer   string `json:"issuer"`
	Subject  string `json:"subject"`
}

type uiPlatformUser struct {
	UserID   string `json:"userId"`
	TenantID string `json:"tenantId"`
	Username string `json:"username"`
}

// EnrollERunPlatformUser enrolls an identity; the platform refuses it unless the
// tenant has no users or the caller manages users, and that refusal is classified
// by enrollERunPlatformUserError.
func (a *App) EnrollERunPlatformUser(input uiPlatformUserEnrollInput) (uiPlatformUser, error) {
	user, err := platform.RunPlatformCreateUser(core.Context{}, a.deps.store, strings.TrimSpace(input.Alias), model.PlatformCreateUserParams{
		Username: strings.TrimSpace(input.Username),
		Issuer:   strings.TrimSpace(input.Issuer),
		Subject:  strings.TrimSpace(input.Subject),
	}, a.deps.cloudDeps)
	if err != nil {
		return uiPlatformUser{}, enrollERunPlatformUserError(err)
	}
	return uiPlatformUser{UserID: user.UserID, TenantID: user.TenantID, Username: user.Username}, nil
}
