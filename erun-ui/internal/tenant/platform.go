package tenant

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
	"github.com/sophium/erun/erun-common/primitive/shared/cloudconfig"
)

// Platform identity resolves like `erun platform` does, never from an
// environment's apiUrl or the tenant's primary cloud alias.

// Each state maps to exactly one next action.
const (
	StateReady                      = ""
	tenantPlatformStateNotConnected = "not-connected"
	tenantPlatformStateChooseAlias  = "choose-alias"
	tenantPlatformStateNotSignedIn  = "not-signed-in"
	tenantPlatformStateNotEnrolled  = "not-enrolled"
	StateNoPermission               = "no-permission"
	// tenantPlatformStateTenantMismatch: the platform's tenant is not the local one,
	// so its rows must not render under the local name.
	tenantPlatformStateTenantMismatch = "tenant-mismatch"
)

// Resolution is how a tenant reaches its platform; Client is nil unless a
// bearer was found.
type Resolution struct {
	State        string
	Client       *platformclient.PlatformClient
	APIURL       string
	Alias        string
	AliasChoices []string
	Issuer       string
	Subject      string
}

// Platform only uses an erun alias the tenant attached, never a
// global one, so one tenant never authenticates with another's credential. The
// API URL comes from the alias, never TenantConfig.APIURL, which would let it
// redirect the bearer token.
func (s *Service) Platform(tenant, aliasOverride string) (Resolution, error) {
	tenantConfig, _, err := s.d.Store.LoadTenantConfig(tenant)
	if err != nil && !errors.Is(err, configpkg.ErrNotInitialized) {
		return Resolution{}, err
	}

	provider, terminal, err := s.resolveTenantERunPlatformAlias(tenantConfig, strings.TrimSpace(aliasOverride))
	if err != nil {
		return Resolution{}, err
	}
	if terminal != nil {
		return *terminal, nil
	}
	if provider.ERun == nil || strings.TrimSpace(provider.ERun.APIURL) == "" {
		// An erun provider with no ERun block is a config-write defect fixed by `erun cloud login`.
		return Resolution{}, fmt.Errorf("erun platform alias %q is incomplete (its erun api configuration is missing); run `erun cloud login %s` to restore it", provider.Alias, provider.Alias)
	}
	apiURL := provider.ERun.APIURL

	bearer, ok := s.tenantPlatformBearer(provider.Alias)
	if !ok {
		return Resolution{State: tenantPlatformStateNotSignedIn, Alias: provider.Alias, APIURL: apiURL}, nil
	}

	issuer, _ := mcpauth.IssuerFromUnverifiedJWT(bearer)
	subject, _ := mcpauth.SubjectFromUnverifiedJWT(bearer)
	client := platformclient.NewPlatformClient(apiURL, func() (string, error) { return bearer, nil }).
		WithUsernameHint(s.tenantDashboardUsernameHint(provider.Alias))
	return Resolution{
		State:   StateReady,
		Client:  client,
		APIURL:  apiURL,
		Alias:   provider.Alias,
		Issuer:  issuer,
		Subject: subject,
	}, nil
}

func (s *Service) tenantPlatformBearer(alias string) (string, bool) {
	token, err := cloud.CloudProviderBearerToken(core.Context{}, s.d.Store, model.CloudBearerParams{Alias: alias}, s.d.Cloud)
	bearer := strings.TrimSpace(token.Token)
	return bearer, err == nil && bearer != ""
}

// resolveTenantERunPlatformAlias honors an explicit aliasOverride verbatim, like
// `erun platform --erun-alias`. A tenant with no attached aliases falls back to
// the sole global erun alias; one with aliases never falls back outside them.
func (s *Service) resolveTenantERunPlatformAlias(tenantConfig model.TenantConfig, aliasOverride string) (provider model.CloudProviderConfig, terminal *Resolution, err error) {
	if aliasOverride != "" || len(tenantConfig.CloudProviderAliases) == 0 {
		return s.resolveGlobalERunPlatformAlias(aliasOverride)
	}

	tenantERunAliases := s.tenantSelectedERunAliases(tenantConfig)
	if len(tenantERunAliases) == 0 {
		return model.CloudProviderConfig{}, &Resolution{State: tenantPlatformStateNotConnected}, nil
	}
	chosen := chooseTenantERunAlias(tenantERunAliases, tenantConfig.PrimaryCloudProviderAlias)
	if chosen == "" {
		return model.CloudProviderConfig{}, &Resolution{State: tenantPlatformStateChooseAlias, AliasChoices: tenantERunAliases}, nil
	}
	provider, err = cloud.ResolveCloudProvider(s.d.Store, chosen)
	if err != nil {
		return model.CloudProviderConfig{}, nil, err
	}
	return provider, nil, nil
}

func (s *Service) resolveGlobalERunPlatformAlias(aliasOverride string) (model.CloudProviderConfig, *Resolution, error) {
	provider, err := cloudconfig.ResolveERunPlatformAlias(s.d.Store, aliasOverride)
	if err != nil {
		resolution, resolveErr := s.tenantPlatformResolutionForResolveError(err)
		return model.CloudProviderConfig{}, &resolution, resolveErr
	}
	return provider, nil, nil
}

// Stale alias names are skipped.
func (s *Service) tenantSelectedERunAliases(tenantConfig model.TenantConfig) []string {
	var erunAliases []string
	for _, alias := range tenantConfig.CloudProviderAliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		candidate, err := cloud.ResolveCloudProvider(s.d.Store, alias)
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
func (s *Service) tenantPlatformResolutionForResolveError(resolveErr error) (Resolution, error) {
	aliases, err := s.d.ConfiguredERunAliases()
	if err != nil {
		return Resolution{}, err
	}
	switch len(aliases) {
	case 0:
		return Resolution{State: tenantPlatformStateNotConnected}, nil
	case 1:
		return Resolution{}, resolveErr
	default:
		return Resolution{State: tenantPlatformStateChooseAlias, AliasChoices: aliases}, nil
	}
}

// Client is for paths reachable only after the dashboard resolved,
// so a not-ready state is a plain error here.
func (s *Service) Client(ctx context.Context, tenant string) (*platformclient.PlatformClient, context.Context, context.CancelFunc, error) {
	resolution, err := s.Platform(tenant, "")
	if err != nil {
		return nil, nil, nil, err
	}
	if resolution.State != StateReady {
		return nil, nil, nil, fmt.Errorf("tenant platform is not ready (%s); reopen the dashboard to resolve it", resolution.State)
	}
	requestCtx, cancel := context.WithTimeout(ctx, RequestTimeout)
	return resolution.Client, requestCtx, cancel, nil
}

// Tenant is empty only from the machine-wide settings dialog; otherwise the alias
// must land in the tenant's selection, since a non-empty selection ignores global aliases.
type ConnectERunPlatformInput struct {
	APIURL string `json:"apiUrl"`
	Tenant string `json:"tenant,omitempty"`
}

// ConnectERunPlatform attaches a hosted erun platform alias to the tenant the
// click came from and returns the alias's token status.
func (s *Service) ConnectERunPlatform(input ConnectERunPlatformInput) (model.CloudProviderStatus, error) {
	provider, err := cloud.InitERunCloudProvider(core.Context{}, s.d.Store, model.InitERunCloudProviderParams{
		APIURL: strings.TrimSpace(input.APIURL),
	}, s.d.Cloud)
	if err != nil {
		return model.CloudProviderStatus{}, err
	}
	if tenant := strings.TrimSpace(input.Tenant); tenant != "" {
		if err := s.attachTenantCloudProviderAlias(tenant, provider.Alias); err != nil {
			return model.CloudProviderStatus{}, err
		}
	}
	return cloud.CloudProviderTokenStatus(provider, s.d.Cloud), nil
}

// A tenant with no config yet gets one naming just this alias.
func (s *Service) attachTenantCloudProviderAlias(tenant, alias string) error {
	config, _, err := s.d.Store.LoadTenantConfig(tenant)
	if err != nil && !errors.Is(err, configpkg.ErrNotInitialized) {
		return err
	}
	config.Name = tenant
	config.CloudProviderAliases, config.PrimaryCloudProviderAlias = cloud.AttachTenantCloudProviderAlias(
		config.CloudProviderAliases, config.PrimaryCloudProviderAlias, alias)
	return s.d.Store.SaveTenantConfig(config)
}

type PlatformUserEnrollInput struct {
	Alias    string `json:"alias"`
	Username string `json:"username"`
	Issuer   string `json:"issuer"`
	Subject  string `json:"subject"`
}

type PlatformUser struct {
	UserID   string `json:"userId"`
	TenantID string `json:"tenantId"`
	Username string `json:"username"`
}

// EnrollERunPlatformUser enrolls an identity; the platform refuses it unless the
// tenant has no users or the caller manages users, and that refusal is classified
// by enrollERunPlatformUserError.
func (s *Service) EnrollERunPlatformUser(input PlatformUserEnrollInput) (PlatformUser, error) {
	user, err := platform.RunPlatformCreateUser(core.Context{}, s.d.Store, strings.TrimSpace(input.Alias), model.PlatformCreateUserParams{
		Username: strings.TrimSpace(input.Username),
		Issuer:   strings.TrimSpace(input.Issuer),
		Subject:  strings.TrimSpace(input.Subject),
	}, s.d.Cloud)
	if err != nil {
		return PlatformUser{}, enrollERunPlatformUserError(err)
	}
	return PlatformUser{UserID: user.UserID, TenantID: user.TenantID, Username: user.Username}, nil
}
