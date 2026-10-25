package settings

import (
	"errors"
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/orchestrator"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/cloudcontext"
	"github.com/sophium/erun/erun-common/infra/cloud/contextinit"
	"github.com/sophium/erun/erun-common/infra/cloud/contextpower"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
	"github.com/sophium/erun/erun-common/infra/cloud/providerlifecycle"
	"github.com/sophium/erun/erun-common/infra/cloud/providertoken"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func (s *Service) LoadERunConfig() (ERunConfig, error) {
	config, _, err := s.d.Store.LoadERunConfig()
	if err != nil {
		return ERunConfig{}, err
	}
	ui := s.erunConfigToUI(config)
	statuses, err := contextpower.RefreshCloudContextStatuses(core.Context{}, s.d.Store, s.d.ContextDeps)
	if err != nil {
		return ERunConfig{}, err
	}
	s.d.CloudStatuses.ApplyStatuses(statuses)
	ui.CloudContexts = cloudContextStatusesToUI(statuses)
	return ui, nil
}

func (s *Service) SaveERunConfig(config ERunConfig) (ERunConfig, error) {
	existing, _, err := s.d.Store.LoadERunConfig()
	if errors.Is(err, configpkg.ErrNotInitialized) {
		existing = configpkg.ERunConfig{}
	} else if err != nil {
		return ERunConfig{}, err
	}
	// Preserve fields this dialog does not edit; only DefaultTenant changes here.
	updated := existing
	updated.DefaultTenant = strings.TrimSpace(config.DefaultTenant)
	// Replace the catalog only when sent, so other callers cannot drop it.
	if config.OpenRouter != nil {
		updated.OpenRouter = openRouterConfigFromUI(config.OpenRouter)
		if err := validateGatewayCatalogModelIDs(updated.OpenRouter); err != nil {
			return ERunConfig{}, err
		}
	}
	if err := s.d.Store.SaveERunConfig(updated); err != nil {
		return ERunConfig{}, err
	}
	return s.erunConfigToUI(updated), nil
}

func (s *Service) LoadCloudProviderStatuses() ([]CloudProviderStatus, error) {
	statuses, err := providertoken.ListCloudProviderStatuses(s.d.Store, s.d.Cloud)
	if err != nil {
		return nil, err
	}
	return CloudProviderStatusesToUI(statuses), nil
}

func (s *Service) LoadCloudContextStatuses() ([]CloudContextStatus, error) {
	statuses, err := contextpower.RefreshCloudContextStatuses(core.Context{}, s.d.Store, s.d.ContextDeps)
	if err != nil {
		return nil, err
	}
	s.d.CloudStatuses.ApplyStatuses(statuses)
	return cloudContextStatusesToUI(statuses), nil
}

func (s *Service) InitCloudContext(input CloudContextInitInput) (CloudContextStatus, error) {
	status, err := contextinit.InitCloudContext(core.Context{}, s.d.Store, model.InitCloudContextParams{
		Name:               strings.TrimSpace(input.Name),
		CloudProviderAlias: strings.TrimSpace(input.CloudProviderAlias),
		Region:             strings.TrimSpace(input.Region),
		InstanceType:       strings.TrimSpace(input.InstanceType),
		DiskType:           strings.TrimSpace(input.DiskType),
		DiskSizeGB:         input.DiskSizeGB,
	}, cloudcontext.CloudContextDependencies{})
	if err != nil {
		return CloudContextStatus{}, err
	}
	s.d.CloudStatuses.SetStatus(status.Name, status.Status)
	return cloudContextStatusToUI(status), nil
}

func (s *Service) StopCloudContext(name string) (CloudContextStatus, error) {
	ctx := s.d.Context()
	// Mark the stop before the AWS call so the reconnect gate sees it when the session
	// dies; clear it on failure so a stop error does not disable reconnect.
	s.d.MarkIntentionalStop(name)
	status, err := s.d.StopCloudContext(ctx, name)
	if err != nil {
		s.d.ClearIntentionalStop(name)
		return CloudContextStatus{}, err
	}
	s.d.CloudStatuses.SetStatus(status.Name, status.Status)
	// Best-effort History entry; the stop already succeeded.
	s.d.RecordManualStop(ctx, status.Name)
	return cloudContextStatusToUI(status), nil
}

func (s *Service) StartCloudContext(name string) (CloudContextStatus, error) {
	status, err := contextpower.StartCloudContext(core.Context{}, s.d.Store, model.CloudContextParams{Name: name}, s.d.ContextDeps)
	if err != nil {
		return CloudContextStatus{}, err
	}
	s.d.ClearIdleStops(status.Name)
	s.d.ClearIntentionalStop(status.Name)
	s.d.CloudStatuses.SetStatus(status.Name, status.Status)
	return cloudContextStatusToUI(status), nil
}

// DisableCloudContextApiStop turns on AWS stop protection.
func (s *Service) DisableCloudContextApiStop(name string) (CloudContextStatus, error) {
	status, err := contextpower.SetCloudContextStopProtection(core.Context{}, s.d.Store, model.CloudContextStopProtectionParams{
		Name:    name,
		Enabled: true,
	}, s.d.ContextDeps)
	if err != nil {
		return CloudContextStatus{}, err
	}
	return cloudContextStatusToUI(status), nil
}

// EnableCloudContextApiStop turns AWS stop protection off.
func (s *Service) EnableCloudContextApiStop(name string) (CloudContextStatus, error) {
	status, err := contextpower.SetCloudContextStopProtection(core.Context{}, s.d.Store, model.CloudContextStopProtectionParams{
		Name:    name,
		Enabled: false,
	}, s.d.ContextDeps)
	if err != nil {
		return CloudContextStatus{}, err
	}
	return cloudContextStatusToUI(status), nil
}

// DescribeCloudContextApiStop reads stop protection on demand; the bulk refresh skips it.
func (s *Service) DescribeCloudContextApiStop(name string) (CloudContextStatus, error) {
	status, err := contextpower.DescribeCloudContextStopProtection(core.Context{}, s.d.Store, name, s.d.ContextDeps)
	if err != nil {
		return CloudContextStatus{}, err
	}
	return cloudContextStatusToUI(status), nil
}

func (s *Service) SaveAWSCloudProviderAlias(input AWSCloudAliasInput) (CloudProviderStatus, error) {
	provider, err := providerconfig.SaveCloudProviderConfig(s.d.Store, model.CloudProviderConfig{
		Alias:         strings.TrimSpace(input.Alias),
		Provider:      configpkg.CloudProviderAWS,
		Username:      strings.TrimSpace(input.Username),
		AccountID:     strings.TrimSpace(input.AccountID),
		Profile:       strings.TrimSpace(input.Profile),
		SSORegion:     strings.TrimSpace(input.SSORegion),
		SSOStartURL:   strings.TrimSpace(input.SSOStartURL),
		OIDCIssuerURL: strings.TrimSpace(input.OIDCIssuerURL),
	})
	if err != nil {
		return CloudProviderStatus{}, err
	}
	return CloudProviderStatusToUI(providertoken.CloudProviderTokenStatus(provider, s.d.Cloud)), nil
}

func (s *Service) InitAWSCloudProvider(input AWSCloudAliasInput) (CloudProviderStatus, error) {
	if strings.TrimSpace(input.Username) != "" || strings.TrimSpace(input.AccountID) != "" {
		return s.SaveAWSCloudProviderAlias(input)
	}
	provider, err := providerlifecycle.InitAWSCloudProvider(core.Context{}, s.d.Store, model.InitAWSCloudProviderParams{
		Profile:       strings.TrimSpace(input.Profile),
		OIDCIssuerURL: strings.TrimSpace(input.OIDCIssuerURL),
	}, s.d.Cloud)
	if err != nil {
		return CloudProviderStatus{}, err
	}
	return CloudProviderStatusToUI(providertoken.CloudProviderTokenStatus(provider, s.d.Cloud)), nil
}

func (s *Service) LoginCloudProvider(alias string) (CloudProviderStatus, error) {
	status, err := providerlifecycle.LoginCloudProviderAlias(s.desktopCloudLoginContext(), s.d.Store, s.desktopCloudLoginParams(alias), s.d.Cloud)
	if err != nil {
		return CloudProviderStatus{}, err
	}
	return CloudProviderStatusToUI(status), nil
}

// desktopCloudLoginParams forces auth code + PKCE: the desktop has no surface to show
// a device-grant code, so auto's device-first ordering looked inert.
func (s *Service) desktopCloudLoginParams(alias string) model.CloudLoginParams {
	return model.CloudLoginParams{Alias: alias, Flow: providerlifecycle.ERunLoginFlowAuthCode}
}

// desktopCloudLoginContext sends the login's instructions to the message center instead of a nil Stdout.
func (s *Service) desktopCloudLoginContext() core.Context {
	return core.Context{Stdout: desktopLoginPromptWriter{s: s}}
}

type desktopLoginPromptWriter struct {
	s *Service
}

func (w desktopLoginPromptWriter) Write(p []byte) (int, error) {
	if message := strings.TrimSpace(string(p)); message != "" {
		w.s.notify("info", message)
	}
	return len(p), nil
}

func (s *Service) LogoutCloudProvider(alias string) (CloudProviderStatus, error) {
	status, err := providerlifecycle.LogoutCloudProviderAlias(core.Context{}, s.d.Store, model.CloudLoginParams{Alias: alias}, s.d.Cloud)
	if err != nil {
		return CloudProviderStatus{}, err
	}
	return CloudProviderStatusToUI(status), nil
}

// SwitchCloudProviderIdentity re-authenticates an alias as another account. Force
// bypasses the active-session short-circuit; a failed switch keeps the old identity.
func (s *Service) SwitchCloudProviderIdentity(alias string) (CloudProviderStatus, error) {
	params := s.desktopCloudLoginParams(alias)
	params.Force = true
	status, err := providerlifecycle.LoginCloudProviderAlias(s.desktopCloudLoginContext(), s.d.Store, params, s.d.Cloud)
	if err != nil {
		return CloudProviderStatus{}, err
	}
	return CloudProviderStatusToUI(status), nil
}

func (s *Service) SetupCloudProviderOIDC(alias string) (CloudProviderStatus, error) {
	status, _, err := providerlifecycle.SetupCloudProviderOIDC(core.Context{}, s.d.Store, model.CloudBearerParams{Alias: alias}, s.d.Cloud)
	if err != nil {
		return CloudProviderStatus{}, err
	}
	return CloudProviderStatusToUI(status), nil
}

func (s *Service) GetCloudProviderBearerToken(alias string) (CloudProviderBearerToken, error) {
	token, err := providertoken.CloudProviderBearerToken(core.Context{}, s.d.Store, model.CloudBearerParams{Alias: strings.TrimSpace(alias)}, s.d.Cloud)
	if err != nil {
		return CloudProviderBearerToken{}, err
	}
	provider, err := providerconfig.ResolveCloudProvider(s.d.Store, token.Alias)
	if err != nil {
		return CloudProviderBearerToken{}, err
	}
	return CloudProviderBearerToken{
		Alias:    token.Alias,
		Issuer:   token.Issuer,
		Token:    token.Token,
		Provider: CloudProviderStatusToUI(providertoken.CloudProviderTokenStatus(provider, s.d.Cloud)),
	}, nil
}

func (s *Service) LoadTenantConfig(tenant string) (TenantConfig, error) {
	tenant, err := uimodel.RequireTenant("loading tenant settings", tenant)
	if err != nil {
		return TenantConfig{}, err
	}

	config, _, err := s.d.Store.LoadTenantConfig(tenant)
	if err != nil {
		return TenantConfig{}, err
	}
	return s.tenantConfigToUI(config, tenant), nil
}

func (s *Service) SaveTenantConfig(config TenantConfig) (TenantConfig, error) {
	tenant, err := uimodel.RequireTenant("saving tenant settings", config.Name)
	if err != nil {
		return TenantConfig{}, err
	}

	existing, _, err := s.d.Store.LoadTenantConfig(tenant)
	if err != nil {
		return TenantConfig{}, err
	}
	updated := tenantConfigFromUI(config, existing)
	if _, err := providerconfig.ResolveTenantCloudProviderIssuers(s.d.Store, updated); err != nil {
		return TenantConfig{}, err
	}
	if err := s.d.Store.SaveTenantConfig(updated); err != nil {
		return TenantConfig{}, err
	}
	return s.tenantConfigToUI(updated, tenant), nil
}

func (s *Service) erunConfigToUI(config configpkg.ERunConfig) ERunConfig {
	return ERunConfig{
		DefaultTenant:  strings.TrimSpace(config.DefaultTenant),
		CloudProviders: CloudProviderStatusesToUI(s.statusesForCloudProviders(config.CloudProviders)),
		CloudContexts:  cloudContextStatusesToUI(statusesForCloudContexts(config.CloudContexts)),
		OpenRouter:     openRouterConfigToUI(config.OpenRouter),
		// Best-effort: an unreadable settings file must not block the dialog.
		OpenRouterDefaults: s.hostGatewayDefaults(),
	}
}

// hostGatewayDefaults reports this machine's Claude Code gateway, or nil; best-effort pre-fill.
func (s *Service) hostGatewayDefaults() *HostGatewayDefaults {
	defaults, err := s.LoadHostGatewayDefaults()
	if err != nil || defaults.BaseURL == "" {
		return nil
	}
	return &defaults
}

func openRouterConfigToUI(config *configpkg.OpenRouterConfig) *OpenRouterConfig {
	if config == nil {
		return nil
	}
	out := &OpenRouterConfig{
		BaseURL:      strings.TrimSpace(config.BaseURL),
		AuthTokenRef: strings.TrimSpace(config.AuthTokenRef),
		DefaultModel: strings.TrimSpace(config.DefaultModel),
	}
	for _, m := range config.Models {
		out.Models = append(out.Models, OpenRouterModel{ID: strings.TrimSpace(m.ID), Context: m.Context, RequiresReasoningEcho: m.RequiresReasoningEcho})
	}
	return out
}

func openRouterConfigFromUI(config *OpenRouterConfig) *configpkg.OpenRouterConfig {
	if config == nil {
		return nil
	}
	out := &configpkg.OpenRouterConfig{
		BaseURL:      strings.TrimSpace(config.BaseURL),
		AuthTokenRef: strings.TrimSpace(config.AuthTokenRef),
		DefaultModel: strings.TrimSpace(config.DefaultModel),
	}
	for _, m := range config.Models {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		out.Models = append(out.Models, configpkg.OpenRouterModel{ID: id, Context: m.Context, RequiresReasoningEcho: m.RequiresReasoningEcho})
	}
	return out
}

func validateGatewayCatalogModelIDs(catalog *configpkg.OpenRouterConfig) error {
	if catalog == nil {
		return nil
	}
	for _, m := range catalog.Models {
		if err := orchestrator.ValidateClaudeModelID(m.ID); err != nil {
			return fmt.Errorf("cannot save the gateway catalog: %w", err)
		}
	}
	if def := catalog.DefaultModel; def != "" {
		if err := orchestrator.ValidateClaudeModelID(def); err != nil {
			return fmt.Errorf("cannot save the gateway catalog: default %w", err)
		}
	}
	return nil
}

func (s *Service) tenantConfigToUI(config model.TenantConfig, fallbackName string) TenantConfig {
	name := strings.TrimSpace(config.Name)
	if name == "" {
		name = strings.TrimSpace(fallbackName)
	}
	result := TenantConfig{
		Name:                      name,
		DefaultEnvironment:        strings.TrimSpace(config.DefaultEnvironment),
		APIURL:                    strings.TrimSpace(config.APIURL),
		CloudProviderAliases:      append([]string(nil), config.CloudProviderAliases...),
		PrimaryCloudProviderAlias: strings.TrimSpace(config.PrimaryCloudProviderAlias),
		CloudProviders:            CloudProviderStatusesToUI(statusesForCloudProvidersFromStore(s.d.Store, s.d.Cloud)),
	}
	return result
}

func tenantConfigFromUI(config TenantConfig, existing model.TenantConfig) model.TenantConfig {
	existing.Name = strings.TrimSpace(config.Name)
	existing.DefaultEnvironment = strings.TrimSpace(config.DefaultEnvironment)
	existing.APIURL = strings.TrimSpace(config.APIURL)
	existing.CloudProviderAliases, existing.PrimaryCloudProviderAlias = configpkg.NormalizeTenantCloudProviderAliases(config.CloudProviderAliases, config.PrimaryCloudProviderAlias)
	return configpkg.NormalizeTenantConfig(existing)
}

func (s *Service) statusesForCloudProviders(providers []model.CloudProviderConfig) []model.CloudProviderStatus {
	statuses := make([]model.CloudProviderStatus, 0, len(providers))
	for _, provider := range providers {
		statuses = append(statuses, providertoken.CloudProviderTokenStatus(provider, s.d.Cloud))
	}
	return statuses
}

func CloudProviderStatusesToUI(statuses []model.CloudProviderStatus) []CloudProviderStatus {
	result := make([]CloudProviderStatus, 0, len(statuses))
	for _, status := range statuses {
		result = append(result, CloudProviderStatusToUI(status))
	}
	return result
}

func CloudProviderStatusToUI(status model.CloudProviderStatus) CloudProviderStatus {
	return CloudProviderStatus{
		Alias:         strings.TrimSpace(status.Alias),
		Provider:      strings.TrimSpace(status.Provider),
		Username:      strings.TrimSpace(status.Username),
		AccountID:     strings.TrimSpace(status.AccountID),
		Profile:       strings.TrimSpace(status.Profile),
		OIDCIssuerURL: providerconfig.CloudProviderOIDCIssuerURL(status.CloudProviderConfig),
		Status:        strings.TrimSpace(status.Status),
		Message:       strings.TrimSpace(status.Message),
	}
}

func statusesForCloudProvidersFromStore(store configpkg.CloudReadStore, deps cloud.CloudDependencies) []model.CloudProviderStatus {
	providers, err := providerconfig.ListCloudProviders(store)
	if err != nil {
		return nil
	}
	statuses := make([]model.CloudProviderStatus, 0, len(providers))
	for _, provider := range providers {
		statuses = append(statuses, providertoken.CloudProviderTokenStatus(provider, deps))
	}
	return statuses
}

func statusesForCloudContexts(contexts []model.CloudContextConfig) []model.CloudContextStatus {
	statuses := make([]model.CloudContextStatus, 0, len(contexts))
	for _, context := range contexts {
		statuses = append(statuses, model.CloudContextStatus{CloudContextConfig: configpkg.NormalizeCloudContextConfig(context)})
	}
	return statuses
}

func cloudContextStatusesToUI(statuses []model.CloudContextStatus) []CloudContextStatus {
	result := make([]CloudContextStatus, 0, len(statuses))
	for _, status := range statuses {
		result = append(result, cloudContextStatusToUI(status))
	}
	return result
}

func cloudContextStatusToUI(status model.CloudContextStatus) CloudContextStatus {
	context := configpkg.NormalizeCloudContextConfig(status.CloudContextConfig)
	return CloudContextStatus{
		Name:                strings.TrimSpace(context.Name),
		Provider:            strings.TrimSpace(context.Provider),
		CloudProviderAlias:  strings.TrimSpace(context.CloudProviderAlias),
		Region:              strings.TrimSpace(context.Region),
		InstanceID:          strings.TrimSpace(context.InstanceID),
		PublicIP:            strings.TrimSpace(context.PublicIP),
		InstanceType:        strings.TrimSpace(context.InstanceType),
		DiskType:            strings.TrimSpace(context.DiskType),
		DiskSizeGB:          context.DiskSizeGB,
		KubernetesContext:   strings.TrimSpace(context.KubernetesContext),
		Status:              strings.TrimSpace(status.Status),
		Message:             strings.TrimSpace(status.Message),
		StopProtection:      status.StopProtection,
		StopProtectionKnown: status.StopProtectionKnown,
	}
}
