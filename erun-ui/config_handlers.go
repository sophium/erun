package main

import (
	"errors"
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/orchestrator"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func (a *App) LoadERunConfig() (uiERunConfig, error) {
	config, _, err := a.deps.store.LoadERunConfig()
	if err != nil {
		return uiERunConfig{}, err
	}
	ui := a.erunConfigToUI(config)
	statuses, err := cloud.RefreshCloudContextStatuses(core.Context{}, a.deps.store, a.deps.cloudContextDeps)
	if err != nil {
		return uiERunConfig{}, err
	}
	a.applyCloudContextStatusesToCache(statuses)
	ui.CloudContexts = cloudContextStatusesToUI(statuses)
	return ui, nil
}

func (a *App) SaveERunConfig(config uiERunConfig) (uiERunConfig, error) {
	existing, _, err := a.deps.store.LoadERunConfig()
	if errors.Is(err, configpkg.ErrNotInitialized) {
		existing = configpkg.ERunConfig{}
	} else if err != nil {
		return uiERunConfig{}, err
	}
	// Preserve fields this dialog does not edit; only DefaultTenant changes here.
	updated := existing
	updated.DefaultTenant = strings.TrimSpace(config.DefaultTenant)
	// Replace the catalog only when sent, so other callers cannot drop it.
	if config.OpenRouter != nil {
		updated.OpenRouter = openRouterConfigFromUI(config.OpenRouter)
		if err := validateGatewayCatalogModelIDs(updated.OpenRouter); err != nil {
			return uiERunConfig{}, err
		}
	}
	if err := a.deps.store.SaveERunConfig(updated); err != nil {
		return uiERunConfig{}, err
	}
	return a.erunConfigToUI(updated), nil
}

func (a *App) LoadCloudProviderStatuses() ([]uiCloudProviderStatus, error) {
	statuses, err := cloud.ListCloudProviderStatuses(a.deps.store, a.deps.cloudDeps)
	if err != nil {
		return nil, err
	}
	return cloudProviderStatusesToUI(statuses), nil
}

func (a *App) LoadCloudContextStatuses() ([]uiCloudContextStatus, error) {
	statuses, err := cloud.RefreshCloudContextStatuses(core.Context{}, a.deps.store, a.deps.cloudContextDeps)
	if err != nil {
		return nil, err
	}
	a.applyCloudContextStatusesToCache(statuses)
	return cloudContextStatusesToUI(statuses), nil
}

func (a *App) InitCloudContext(input uiCloudContextInitInput) (uiCloudContextStatus, error) {
	status, err := cloud.InitCloudContext(core.Context{}, a.deps.store, model.InitCloudContextParams{
		Name:               strings.TrimSpace(input.Name),
		CloudProviderAlias: strings.TrimSpace(input.CloudProviderAlias),
		Region:             strings.TrimSpace(input.Region),
		InstanceType:       strings.TrimSpace(input.InstanceType),
		DiskType:           strings.TrimSpace(input.DiskType),
		DiskSizeGB:         input.DiskSizeGB,
	}, cloud.CloudContextDependencies{})
	if err != nil {
		return uiCloudContextStatus{}, err
	}
	a.setCloudContextStatusInCache(status.Name, status.Status)
	return cloudContextStatusToUI(status), nil
}

func (a *App) StopCloudContext(name string) (uiCloudContextStatus, error) {
	ctx := a.context()
	// Mark the stop before the AWS call so the reconnect gate sees it when the session
	// dies; clear it on failure so a stop error does not disable reconnect.
	a.markIntentionalStopForCloudContext(name)
	status, err := a.deps.stopCloudContext(ctx, name)
	if err != nil {
		a.clearIntentionalStopForCloudContext(name)
		return uiCloudContextStatus{}, err
	}
	a.setCloudContextStatusInCache(status.Name, status.Status)
	// Best-effort History entry; the stop already succeeded.
	a.recordManualStopForCloudContext(ctx, status.Name)
	return cloudContextStatusToUI(status), nil
}

func (a *App) StartCloudContext(name string) (uiCloudContextStatus, error) {
	status, err := cloud.StartCloudContext(core.Context{}, a.deps.store, model.CloudContextParams{Name: name}, a.deps.cloudContextDeps)
	if err != nil {
		return uiCloudContextStatus{}, err
	}
	a.clearIdleStopsForCloudContext(status.Name)
	a.clearIntentionalStopForCloudContext(status.Name)
	a.setCloudContextStatusInCache(status.Name, status.Status)
	return cloudContextStatusToUI(status), nil
}

// DisableCloudContextApiStop turns on AWS stop protection.
func (a *App) DisableCloudContextApiStop(name string) (uiCloudContextStatus, error) {
	status, err := cloud.SetCloudContextStopProtection(core.Context{}, a.deps.store, model.CloudContextStopProtectionParams{
		Name:    name,
		Enabled: true,
	}, a.deps.cloudContextDeps)
	if err != nil {
		return uiCloudContextStatus{}, err
	}
	return cloudContextStatusToUI(status), nil
}

// EnableCloudContextApiStop turns AWS stop protection off.
func (a *App) EnableCloudContextApiStop(name string) (uiCloudContextStatus, error) {
	status, err := cloud.SetCloudContextStopProtection(core.Context{}, a.deps.store, model.CloudContextStopProtectionParams{
		Name:    name,
		Enabled: false,
	}, a.deps.cloudContextDeps)
	if err != nil {
		return uiCloudContextStatus{}, err
	}
	return cloudContextStatusToUI(status), nil
}

// DescribeCloudContextApiStop reads stop protection on demand; the bulk refresh skips it.
func (a *App) DescribeCloudContextApiStop(name string) (uiCloudContextStatus, error) {
	status, err := cloud.DescribeCloudContextStopProtection(core.Context{}, a.deps.store, name, a.deps.cloudContextDeps)
	if err != nil {
		return uiCloudContextStatus{}, err
	}
	return cloudContextStatusToUI(status), nil
}

func (a *App) SaveAWSCloudProviderAlias(input uiAWSCloudAliasInput) (uiCloudProviderStatus, error) {
	provider, err := cloud.SaveCloudProviderConfig(a.deps.store, model.CloudProviderConfig{
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
		return uiCloudProviderStatus{}, err
	}
	return cloudProviderStatusToUI(cloud.CloudProviderTokenStatus(provider, a.deps.cloudDeps)), nil
}

func (a *App) InitAWSCloudProvider(input uiAWSCloudAliasInput) (uiCloudProviderStatus, error) {
	if strings.TrimSpace(input.Username) != "" || strings.TrimSpace(input.AccountID) != "" {
		return a.SaveAWSCloudProviderAlias(input)
	}
	provider, err := cloud.InitAWSCloudProvider(core.Context{}, a.deps.store, model.InitAWSCloudProviderParams{
		Profile:       strings.TrimSpace(input.Profile),
		OIDCIssuerURL: strings.TrimSpace(input.OIDCIssuerURL),
	}, a.deps.cloudDeps)
	if err != nil {
		return uiCloudProviderStatus{}, err
	}
	return cloudProviderStatusToUI(cloud.CloudProviderTokenStatus(provider, a.deps.cloudDeps)), nil
}

func (a *App) LoginCloudProvider(alias string) (uiCloudProviderStatus, error) {
	status, err := cloud.LoginCloudProviderAlias(a.desktopCloudLoginContext(), a.deps.store, a.desktopCloudLoginParams(alias), a.deps.cloudDeps)
	if err != nil {
		return uiCloudProviderStatus{}, err
	}
	return cloudProviderStatusToUI(status), nil
}

// desktopCloudLoginParams forces auth code + PKCE: the desktop has no surface to show
// a device-grant code, so auto's device-first ordering looked inert.
func (a *App) desktopCloudLoginParams(alias string) model.CloudLoginParams {
	return model.CloudLoginParams{Alias: alias, Flow: cloud.ERunLoginFlowAuthCode}
}

// desktopCloudLoginContext sends the login's instructions to the message center instead of a nil Stdout.
func (a *App) desktopCloudLoginContext() core.Context {
	return core.Context{Stdout: desktopLoginPromptWriter{app: a}}
}

type desktopLoginPromptWriter struct {
	app *App
}

func (w desktopLoginPromptWriter) Write(p []byte) (int, error) {
	if message := strings.TrimSpace(string(p)); message != "" {
		w.app.emitAppNotification("info", message)
	}
	return len(p), nil
}

func (a *App) LogoutCloudProvider(alias string) (uiCloudProviderStatus, error) {
	status, err := cloud.LogoutCloudProviderAlias(core.Context{}, a.deps.store, model.CloudLoginParams{Alias: alias}, a.deps.cloudDeps)
	if err != nil {
		return uiCloudProviderStatus{}, err
	}
	return cloudProviderStatusToUI(status), nil
}

// SwitchCloudProviderIdentity re-authenticates an alias as another account. Force
// bypasses the active-session short-circuit; a failed switch keeps the old identity.
func (a *App) SwitchCloudProviderIdentity(alias string) (uiCloudProviderStatus, error) {
	params := a.desktopCloudLoginParams(alias)
	params.Force = true
	status, err := cloud.LoginCloudProviderAlias(a.desktopCloudLoginContext(), a.deps.store, params, a.deps.cloudDeps)
	if err != nil {
		return uiCloudProviderStatus{}, err
	}
	return cloudProviderStatusToUI(status), nil
}

func (a *App) SetupCloudProviderOIDC(alias string) (uiCloudProviderStatus, error) {
	status, _, err := cloud.SetupCloudProviderOIDC(core.Context{}, a.deps.store, model.CloudBearerParams{Alias: alias}, a.deps.cloudDeps)
	if err != nil {
		return uiCloudProviderStatus{}, err
	}
	return cloudProviderStatusToUI(status), nil
}

func (a *App) GetCloudProviderBearerToken(alias string) (uiCloudProviderBearerToken, error) {
	token, err := cloud.CloudProviderBearerToken(core.Context{}, a.deps.store, model.CloudBearerParams{Alias: strings.TrimSpace(alias)}, a.deps.cloudDeps)
	if err != nil {
		return uiCloudProviderBearerToken{}, err
	}
	provider, err := cloud.ResolveCloudProvider(a.deps.store, token.Alias)
	if err != nil {
		return uiCloudProviderBearerToken{}, err
	}
	return uiCloudProviderBearerToken{
		Alias:    token.Alias,
		Issuer:   token.Issuer,
		Token:    token.Token,
		Provider: cloudProviderStatusToUI(cloud.CloudProviderTokenStatus(provider, a.deps.cloudDeps)),
	}, nil
}

func (a *App) LoadTenantConfig(tenant string) (uiTenantConfig, error) {
	tenant, err := uimodel.RequireTenant("loading tenant settings", tenant)
	if err != nil {
		return uiTenantConfig{}, err
	}

	config, _, err := a.deps.store.LoadTenantConfig(tenant)
	if err != nil {
		return uiTenantConfig{}, err
	}
	return a.tenantConfigToUI(config, tenant), nil
}

func (a *App) SaveTenantConfig(config uiTenantConfig) (uiTenantConfig, error) {
	tenant, err := uimodel.RequireTenant("saving tenant settings", config.Name)
	if err != nil {
		return uiTenantConfig{}, err
	}

	existing, _, err := a.deps.store.LoadTenantConfig(tenant)
	if err != nil {
		return uiTenantConfig{}, err
	}
	updated := tenantConfigFromUI(config, existing)
	if _, err := cloud.ResolveTenantCloudProviderIssuers(a.deps.store, updated); err != nil {
		return uiTenantConfig{}, err
	}
	if err := a.deps.store.SaveTenantConfig(updated); err != nil {
		return uiTenantConfig{}, err
	}
	return a.tenantConfigToUI(updated, tenant), nil
}

func (a *App) erunConfigToUI(config configpkg.ERunConfig) uiERunConfig {
	return uiERunConfig{
		DefaultTenant:  strings.TrimSpace(config.DefaultTenant),
		CloudProviders: cloudProviderStatusesToUI(a.statusesForCloudProviders(config.CloudProviders)),
		CloudContexts:  cloudContextStatusesToUI(statusesForCloudContexts(config.CloudContexts)),
		OpenRouter:     openRouterConfigToUI(config.OpenRouter),
		// Best-effort: an unreadable settings file must not block the dialog.
		OpenRouterDefaults: a.hostGatewayDefaults(),
	}
}

// hostGatewayDefaults reports this machine's Claude Code gateway, or nil; best-effort pre-fill.
func (a *App) hostGatewayDefaults() *uiHostGatewayDefaults {
	defaults, err := a.LoadHostGatewayDefaults()
	if err != nil || defaults.BaseURL == "" {
		return nil
	}
	return &defaults
}

func openRouterConfigToUI(config *configpkg.OpenRouterConfig) *uiOpenRouterConfig {
	if config == nil {
		return nil
	}
	out := &uiOpenRouterConfig{
		BaseURL:      strings.TrimSpace(config.BaseURL),
		AuthTokenRef: strings.TrimSpace(config.AuthTokenRef),
		DefaultModel: strings.TrimSpace(config.DefaultModel),
	}
	for _, m := range config.Models {
		out.Models = append(out.Models, uiOpenRouterModel{ID: strings.TrimSpace(m.ID), Context: m.Context, RequiresReasoningEcho: m.RequiresReasoningEcho})
	}
	return out
}

func openRouterConfigFromUI(config *uiOpenRouterConfig) *configpkg.OpenRouterConfig {
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

func (a *App) tenantConfigToUI(config model.TenantConfig, fallbackName string) uiTenantConfig {
	name := strings.TrimSpace(config.Name)
	if name == "" {
		name = strings.TrimSpace(fallbackName)
	}
	result := uiTenantConfig{
		Name:                      name,
		DefaultEnvironment:        strings.TrimSpace(config.DefaultEnvironment),
		APIURL:                    strings.TrimSpace(config.APIURL),
		CloudProviderAliases:      append([]string(nil), config.CloudProviderAliases...),
		PrimaryCloudProviderAlias: strings.TrimSpace(config.PrimaryCloudProviderAlias),
		CloudProviders:            cloudProviderStatusesToUI(statusesForCloudProvidersFromStore(a.deps.store, a.deps.cloudDeps)),
	}
	return result
}

func tenantConfigFromUI(config uiTenantConfig, existing model.TenantConfig) model.TenantConfig {
	existing.Name = strings.TrimSpace(config.Name)
	existing.DefaultEnvironment = strings.TrimSpace(config.DefaultEnvironment)
	existing.APIURL = strings.TrimSpace(config.APIURL)
	existing.CloudProviderAliases, existing.PrimaryCloudProviderAlias = configpkg.NormalizeTenantCloudProviderAliases(config.CloudProviderAliases, config.PrimaryCloudProviderAlias)
	return configpkg.NormalizeTenantConfig(existing)
}

func (a *App) statusesForCloudProviders(providers []model.CloudProviderConfig) []model.CloudProviderStatus {
	statuses := make([]model.CloudProviderStatus, 0, len(providers))
	for _, provider := range providers {
		statuses = append(statuses, cloud.CloudProviderTokenStatus(provider, a.deps.cloudDeps))
	}
	return statuses
}

func cloudProviderStatusesToUI(statuses []model.CloudProviderStatus) []uiCloudProviderStatus {
	result := make([]uiCloudProviderStatus, 0, len(statuses))
	for _, status := range statuses {
		result = append(result, cloudProviderStatusToUI(status))
	}
	return result
}

func cloudProviderStatusToUI(status model.CloudProviderStatus) uiCloudProviderStatus {
	return uiCloudProviderStatus{
		Alias:         strings.TrimSpace(status.Alias),
		Provider:      strings.TrimSpace(status.Provider),
		Username:      strings.TrimSpace(status.Username),
		AccountID:     strings.TrimSpace(status.AccountID),
		Profile:       strings.TrimSpace(status.Profile),
		OIDCIssuerURL: cloud.CloudProviderOIDCIssuerURL(status.CloudProviderConfig),
		Status:        strings.TrimSpace(status.Status),
		Message:       strings.TrimSpace(status.Message),
	}
}

func statusesForCloudProvidersFromStore(store configpkg.CloudReadStore, deps cloud.CloudDependencies) []model.CloudProviderStatus {
	providers, err := cloud.ListCloudProviders(store)
	if err != nil {
		return nil
	}
	statuses := make([]model.CloudProviderStatus, 0, len(providers))
	for _, provider := range providers {
		statuses = append(statuses, cloud.CloudProviderTokenStatus(provider, deps))
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

func cloudContextStatusesToUI(statuses []model.CloudContextStatus) []uiCloudContextStatus {
	result := make([]uiCloudContextStatus, 0, len(statuses))
	for _, status := range statuses {
		result = append(result, cloudContextStatusToUI(status))
	}
	return result
}

func cloudContextStatusToUI(status model.CloudContextStatus) uiCloudContextStatus {
	context := configpkg.NormalizeCloudContextConfig(status.CloudContextConfig)
	return uiCloudContextStatus{
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

// emitAppStatus narrates a busy indicator; outcomes go through notifications instead.
func (a *App) emitAppStatus(message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	a.emit(uimodel.AppStatusEvent, uimodel.AppStatusPayload{Message: message, Busy: true})
}

// emitAppNotification pushes a toast with no env/orchestrator identity.
func (a *App) emitAppNotification(kind, message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	a.emit(uimodel.AppNotificationEvent, uimodel.AppNotificationPayload{Kind: kind, Message: message})
}

// notificationSourceRuntimeUnreachable must match dismissNotificationForEnv in the frontend.
const notificationSourceRuntimeUnreachable = "runtime-unreachable"

// notificationSourceForwardOutage tags a port-forward repair that ran and did not help,
// unlike notificationSourceRuntimeUnreachable.
const notificationSourceForwardOutage = "port-forward-outage"

const notificationSourceDeployFailed = "deploy-failed"

// notificationSourceOrchestratorEdgeUnreachable is posted per linked env, carrying that env's deploy action.
const notificationSourceOrchestratorEdgeUnreachable = "orchestrator-edge-unreachable"

const notificationSourceMCPUnreachable = "mcp-unreachable"

const notificationSourceCredentialRefreshFailed = "credential-refresh-failed"

const notificationSourceWorkspaceSyncNoPath = "workspace-sync-no-path"

const notificationSourceWorkspaceSyncFailed = "workspace-sync-failed"

// notificationActionDeploy opens the tagged env's deploy dialog; "" means no action.
const notificationActionDeploy = "deploy"

const notificationActionRestartOrchestrator = "restart-orchestrator"

// notificationActionInstallAndRestartOrchestrator also links the CLI install docs.
const notificationActionInstallAndRestartOrchestrator = "install-and-restart-orchestrator"

// emitEnvNotification posts an env-scoped notification that emitClearEnvNotification
// can dismiss by (source, tenant, environment).
func (a *App) emitEnvNotification(kind, tenant, environment, source, message, action string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	a.emit(uimodel.AppNotificationEvent, uimodel.AppNotificationPayload{
		Kind:        kind,
		Message:     message,
		Tenant:      tenant,
		Environment: environment,
		Source:      source,
		Action:      action,
	})
}

// emitOrchestratorNotification tags the orchestrator so the frontend can act on it.
func (a *App) emitOrchestratorNotification(kind, orchestratorID, message, action string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	a.emit(uimodel.AppNotificationEvent, uimodel.AppNotificationPayload{
		Kind:           kind,
		Message:        message,
		OrchestratorID: orchestratorID,
		Action:         action,
	})
}

// emitClearEnvNotification dismisses an emitEnvNotification matching source, tenant, and environment.
func (a *App) emitClearEnvNotification(tenant, environment, source string) {
	a.emit(uimodel.AppNotificationClearEvent, uimodel.AppNotificationClearPayload{
		Tenant:      tenant,
		Environment: environment,
		Source:      source,
	})
}

func cloudContextDisplayName(status model.CloudContextStatus) string {
	if name := strings.TrimSpace(status.KubernetesContext); name != "" {
		return name
	}
	return strings.TrimSpace(status.Name)
}
