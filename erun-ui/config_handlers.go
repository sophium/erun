package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

func (a *App) LoadERunConfig() (uiERunConfig, error) {
	config, _, err := a.deps.store.LoadERunConfig()
	if err != nil {
		return uiERunConfig{}, err
	}
	ui := a.erunConfigToUI(config)
	statuses, err := eruncommon.RefreshCloudContextStatuses(eruncommon.Context{}, a.deps.store, a.deps.cloudContextDeps)
	if err != nil {
		return uiERunConfig{}, err
	}
	a.applyCloudContextStatusesToCache(statuses)
	ui.CloudContexts = cloudContextStatusesToUI(statuses)
	return ui, nil
}

func (a *App) SaveERunConfig(config uiERunConfig) (uiERunConfig, error) {
	existing, _, err := a.deps.store.LoadERunConfig()
	if errors.Is(err, eruncommon.ErrNotInitialized) {
		existing = eruncommon.ERunConfig{}
	} else if err != nil {
		return uiERunConfig{}, err
	}
	// Preserve every field this dialog does not edit (cloud providers/contexts,
	// runtime registry, orchestrators) — a scoped save must not drop config the
	// user manages elsewhere. Only DefaultTenant is editable here.
	updated := existing
	updated.DefaultTenant = strings.TrimSpace(config.DefaultTenant)
	if err := a.deps.store.SaveERunConfig(updated); err != nil {
		return uiERunConfig{}, err
	}
	return a.erunConfigToUI(updated), nil
}

func (a *App) LoadCloudProviderStatuses() ([]uiCloudProviderStatus, error) {
	statuses, err := eruncommon.ListCloudProviderStatuses(a.deps.store, a.deps.cloudDeps)
	if err != nil {
		return nil, err
	}
	return cloudProviderStatusesToUI(statuses), nil
}

func (a *App) LoadCloudContextStatuses() ([]uiCloudContextStatus, error) {
	statuses, err := eruncommon.RefreshCloudContextStatuses(eruncommon.Context{}, a.deps.store, a.deps.cloudContextDeps)
	if err != nil {
		return nil, err
	}
	a.applyCloudContextStatusesToCache(statuses)
	return cloudContextStatusesToUI(statuses), nil
}

func (a *App) InitCloudContext(input uiCloudContextInitInput) (uiCloudContextStatus, error) {
	status, err := eruncommon.InitCloudContext(eruncommon.Context{}, a.deps.store, eruncommon.InitCloudContextParams{
		Name:               strings.TrimSpace(input.Name),
		CloudProviderAlias: strings.TrimSpace(input.CloudProviderAlias),
		Region:             strings.TrimSpace(input.Region),
		InstanceType:       strings.TrimSpace(input.InstanceType),
		DiskType:           strings.TrimSpace(input.DiskType),
		DiskSizeGB:         input.DiskSizeGB,
	}, eruncommon.CloudContextDependencies{})
	if err != nil {
		return uiCloudContextStatus{}, err
	}
	a.setCloudContextStatusInCache(status.Name, status.Status)
	return cloudContextStatusToUI(status), nil
}

func (a *App) StopCloudContext(name string) (uiCloudContextStatus, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	// Record Stop intent before the AWS call so shouldRespawnForCloudContext
	// already sees the marker by the time the kubectl session dies and
	// the reconnect loop fires. If the AWS call fails the cluster is
	// still up — clear the marker so a transient stop error does not
	// silently disable reconnect.
	a.markIntentionalStopForCloudContext(name)
	status, err := a.deps.stopCloudContext(ctx, name)
	if err != nil {
		a.clearIntentionalStopForCloudContext(name)
		return uiCloudContextStatus{}, err
	}
	a.setCloudContextStatusInCache(status.Name, status.Status)
	// Best-effort: surface the manual stop in each linked env's History
	// tab alongside the in-pod monitor's auto-stops. Failures are ignored
	// because the AWS stop already succeeded — the user-visible action is
	// complete.
	a.recordManualStopForCloudContext(ctx, status.Name)
	return cloudContextStatusToUI(status), nil
}

func (a *App) StartCloudContext(name string) (uiCloudContextStatus, error) {
	status, err := eruncommon.StartCloudContext(eruncommon.Context{}, a.deps.store, eruncommon.CloudContextParams{Name: name}, a.deps.cloudContextDeps)
	if err != nil {
		return uiCloudContextStatus{}, err
	}
	a.clearIdleStopsForCloudContext(status.Name)
	a.clearIntentionalStopForCloudContext(status.Name)
	a.setCloudContextStatusInCache(status.Name, status.Status)
	return cloudContextStatusToUI(status), nil
}

// DisableCloudContextApiStop turns on AWS stop protection — the recovery
// lever an operator reaches for when auto-stop is racing against an
// unhealthy env.
func (a *App) DisableCloudContextApiStop(name string) (uiCloudContextStatus, error) {
	status, err := eruncommon.SetCloudContextStopProtection(eruncommon.Context{}, a.deps.store, eruncommon.CloudContextStopProtectionParams{
		Name:    name,
		Enabled: true,
	}, a.deps.cloudContextDeps)
	if err != nil {
		return uiCloudContextStatus{}, err
	}
	return cloudContextStatusToUI(status), nil
}

// EnableCloudContextApiStop turns AWS stop protection back off so normal
// auto-stop and manual stop resume.
func (a *App) EnableCloudContextApiStop(name string) (uiCloudContextStatus, error) {
	status, err := eruncommon.SetCloudContextStopProtection(eruncommon.Context{}, a.deps.store, eruncommon.CloudContextStopProtectionParams{
		Name:    name,
		Enabled: false,
	}, a.deps.cloudContextDeps)
	if err != nil {
		return uiCloudContextStatus{}, err
	}
	return cloudContextStatusToUI(status), nil
}

// DescribeCloudContextApiStop reads live AWS stop-protection state on
// demand: the bulk status refresh deliberately skips it to avoid an AWS
// round-trip per env on every poll, so the titlebar lock toggle reads it
// lazily instead.
func (a *App) DescribeCloudContextApiStop(name string) (uiCloudContextStatus, error) {
	status, err := eruncommon.DescribeCloudContextStopProtection(eruncommon.Context{}, a.deps.store, name, a.deps.cloudContextDeps)
	if err != nil {
		return uiCloudContextStatus{}, err
	}
	return cloudContextStatusToUI(status), nil
}

func (a *App) SaveAWSCloudProviderAlias(input uiAWSCloudAliasInput) (uiCloudProviderStatus, error) {
	provider, err := eruncommon.SaveCloudProviderConfig(a.deps.store, eruncommon.CloudProviderConfig{
		Alias:         strings.TrimSpace(input.Alias),
		Provider:      eruncommon.CloudProviderAWS,
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
	return cloudProviderStatusToUI(eruncommon.CloudProviderTokenStatus(provider, a.deps.cloudDeps)), nil
}

func (a *App) InitAWSCloudProvider(input uiAWSCloudAliasInput) (uiCloudProviderStatus, error) {
	if strings.TrimSpace(input.Username) != "" || strings.TrimSpace(input.AccountID) != "" {
		return a.SaveAWSCloudProviderAlias(input)
	}
	provider, err := eruncommon.InitAWSCloudProvider(eruncommon.Context{}, a.deps.store, eruncommon.InitAWSCloudProviderParams{
		Profile:       strings.TrimSpace(input.Profile),
		OIDCIssuerURL: strings.TrimSpace(input.OIDCIssuerURL),
	}, a.deps.cloudDeps)
	if err != nil {
		return uiCloudProviderStatus{}, err
	}
	return cloudProviderStatusToUI(eruncommon.CloudProviderTokenStatus(provider, a.deps.cloudDeps)), nil
}

func (a *App) LoginCloudProvider(alias string) (uiCloudProviderStatus, error) {
	status, err := eruncommon.LoginCloudProviderAlias(eruncommon.Context{}, a.deps.store, eruncommon.CloudLoginParams{Alias: alias}, a.deps.cloudDeps)
	if err != nil {
		return uiCloudProviderStatus{}, err
	}
	return cloudProviderStatusToUI(status), nil
}

func (a *App) LogoutCloudProvider(alias string) (uiCloudProviderStatus, error) {
	status, err := eruncommon.LogoutCloudProviderAlias(eruncommon.Context{}, a.deps.store, eruncommon.CloudLoginParams{Alias: alias}, a.deps.cloudDeps)
	if err != nil {
		return uiCloudProviderStatus{}, err
	}
	return cloudProviderStatusToUI(status), nil
}

func (a *App) SetupCloudProviderOIDC(alias string) (uiCloudProviderStatus, error) {
	status, _, err := eruncommon.SetupCloudProviderOIDC(eruncommon.Context{}, a.deps.store, eruncommon.CloudBearerParams{Alias: alias}, a.deps.cloudDeps)
	if err != nil {
		return uiCloudProviderStatus{}, err
	}
	return cloudProviderStatusToUI(status), nil
}

func (a *App) GetCloudProviderBearerToken(alias string) (uiCloudProviderBearerToken, error) {
	token, err := eruncommon.CloudProviderBearerToken(eruncommon.Context{}, a.deps.store, eruncommon.CloudBearerParams{Alias: strings.TrimSpace(alias)}, a.deps.cloudDeps)
	if err != nil {
		return uiCloudProviderBearerToken{}, err
	}
	provider, err := eruncommon.ResolveCloudProvider(a.deps.store, token.Alias)
	if err != nil {
		return uiCloudProviderBearerToken{}, err
	}
	return uiCloudProviderBearerToken{
		Alias:    token.Alias,
		Issuer:   token.Issuer,
		Token:    token.Token,
		Provider: cloudProviderStatusToUI(eruncommon.CloudProviderTokenStatus(provider, a.deps.cloudDeps)),
	}, nil
}

func (a *App) LoadTenantConfig(tenant string) (uiTenantConfig, error) {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return uiTenantConfig{}, fmt.Errorf("tenant is required")
	}

	config, _, err := a.deps.store.LoadTenantConfig(tenant)
	if err != nil {
		return uiTenantConfig{}, err
	}
	return a.tenantConfigToUI(config, tenant), nil
}

func (a *App) SaveTenantConfig(config uiTenantConfig) (uiTenantConfig, error) {
	tenant := strings.TrimSpace(config.Name)
	if tenant == "" {
		return uiTenantConfig{}, fmt.Errorf("tenant is required")
	}

	existing, _, err := a.deps.store.LoadTenantConfig(tenant)
	if err != nil {
		return uiTenantConfig{}, err
	}
	updated := tenantConfigFromUI(config, existing)
	if _, err := eruncommon.ResolveTenantCloudProviderIssuers(a.deps.store, updated); err != nil {
		return uiTenantConfig{}, err
	}
	if err := a.deps.store.SaveTenantConfig(updated); err != nil {
		return uiTenantConfig{}, err
	}
	return a.tenantConfigToUI(updated, tenant), nil
}

func (a *App) erunConfigToUI(config eruncommon.ERunConfig) uiERunConfig {
	return uiERunConfig{
		DefaultTenant:  strings.TrimSpace(config.DefaultTenant),
		CloudProviders: cloudProviderStatusesToUI(a.statusesForCloudProviders(config.CloudProviders)),
		CloudContexts:  cloudContextStatusesToUI(statusesForCloudContexts(config.CloudContexts)),
	}
}

func (a *App) tenantConfigToUI(config eruncommon.TenantConfig, fallbackName string) uiTenantConfig {
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

func tenantConfigFromUI(config uiTenantConfig, existing eruncommon.TenantConfig) eruncommon.TenantConfig {
	existing.Name = strings.TrimSpace(config.Name)
	existing.DefaultEnvironment = strings.TrimSpace(config.DefaultEnvironment)
	existing.APIURL = strings.TrimSpace(config.APIURL)
	existing.CloudProviderAliases, existing.PrimaryCloudProviderAlias = eruncommon.NormalizeTenantCloudProviderAliases(config.CloudProviderAliases, config.PrimaryCloudProviderAlias)
	return eruncommon.NormalizeTenantConfig(existing)
}

func (a *App) statusesForCloudProviders(providers []eruncommon.CloudProviderConfig) []eruncommon.CloudProviderStatus {
	statuses := make([]eruncommon.CloudProviderStatus, 0, len(providers))
	for _, provider := range providers {
		statuses = append(statuses, eruncommon.CloudProviderTokenStatus(provider, a.deps.cloudDeps))
	}
	return statuses
}

func cloudProviderStatusesToUI(statuses []eruncommon.CloudProviderStatus) []uiCloudProviderStatus {
	result := make([]uiCloudProviderStatus, 0, len(statuses))
	for _, status := range statuses {
		result = append(result, cloudProviderStatusToUI(status))
	}
	return result
}

func cloudProviderStatusToUI(status eruncommon.CloudProviderStatus) uiCloudProviderStatus {
	return uiCloudProviderStatus{
		Alias:         strings.TrimSpace(status.Alias),
		Provider:      strings.TrimSpace(status.Provider),
		Username:      strings.TrimSpace(status.Username),
		AccountID:     strings.TrimSpace(status.AccountID),
		Profile:       strings.TrimSpace(status.Profile),
		OIDCIssuerURL: eruncommon.CloudProviderOIDCIssuerURL(status.CloudProviderConfig),
		Status:        strings.TrimSpace(status.Status),
		Message:       strings.TrimSpace(status.Message),
	}
}

func statusesForCloudProvidersFromStore(store eruncommon.CloudReadStore, deps eruncommon.CloudDependencies) []eruncommon.CloudProviderStatus {
	providers, err := eruncommon.ListCloudProviders(store)
	if err != nil {
		return nil
	}
	statuses := make([]eruncommon.CloudProviderStatus, 0, len(providers))
	for _, provider := range providers {
		statuses = append(statuses, eruncommon.CloudProviderTokenStatus(provider, deps))
	}
	return statuses
}

func statusesForCloudContexts(contexts []eruncommon.CloudContextConfig) []eruncommon.CloudContextStatus {
	statuses := make([]eruncommon.CloudContextStatus, 0, len(contexts))
	for _, context := range contexts {
		statuses = append(statuses, eruncommon.CloudContextStatus{CloudContextConfig: eruncommon.NormalizeCloudContextConfig(context)})
	}
	return statuses
}

func cloudContextStatusesToUI(statuses []eruncommon.CloudContextStatus) []uiCloudContextStatus {
	result := make([]uiCloudContextStatus, 0, len(statuses))
	for _, status := range statuses {
		result = append(result, cloudContextStatusToUI(status))
	}
	return result
}

func cloudContextStatusToUI(status eruncommon.CloudContextStatus) uiCloudContextStatus {
	context := eruncommon.NormalizeCloudContextConfig(status.CloudContextConfig)
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

func (a *App) emitAppStatus(message string, busy bool) {
	if strings.TrimSpace(message) == "" {
		return
	}
	a.emit(appStatusEvent, appStatusPayload{Message: message, Busy: busy})
}

// emitAppNotification pushes a transient toast-style notification.
// Use this for one-shot info/success events that should not linger in
// the titlebar after the state they describe has moved on (e.g. the
// idle-stop success line). Errors and long-running busy indicators
// still belong on emitAppStatus so their pill stays readable until the
// user dismisses or replaces it.
func (a *App) emitAppNotification(kind, message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	a.emit(appNotificationEvent, appNotificationPayload{Kind: kind, Message: message})
}

// notificationSourceRuntimeUnreachable tags the "Could not reach the runtime …"
// warning so the deploy lifecycle can clear it. Must match the
// string the frontend compares in dismissNotificationForEnv.
const notificationSourceRuntimeUnreachable = "runtime-unreachable"

// notificationSourceForwardStale tags the "…/… is unreachable: its port-forward
// …" warning the activity sweep posts once a bounded repair has failed, so the
// same lifecycle clear retires it when the forward starts carrying traffic
// again. Kept apart from notificationSourceRuntimeUnreachable because the two
// describe different failures: that one is a reconnect that could not run, this
// one is a reconnect that ran and did not help.
const notificationSourceForwardStale = "port-forward-stale"

// notificationSourceDeployFailed tags the "Deploy of …/… failed" error so the
// same lifecycle clear retires it once a new deploy for the env starts or the
// runtime becomes reachable.
const notificationSourceDeployFailed = "deploy-failed"

// emitEnvNotification posts a notification tagged with the env it describes and
// a stable source, so a later emitClearEnvNotification for the same
// (source, tenant, environment) can dismiss it. Use it for env-scoped, state-
// backed notifications (not one-shot toasts).
func (a *App) emitEnvNotification(kind, tenant, environment, source, message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	a.emit(appNotificationEvent, appNotificationPayload{
		Kind:        kind,
		Message:     message,
		Tenant:      tenant,
		Environment: environment,
		Source:      source,
	})
}

// emitClearEnvNotification asks the frontend to dismiss a notification posted by
// emitEnvNotification, but only when its source/tenant/environment all match.
// Fired when the state a notification described has moved on: a
// deploy for the env starts, or the runtime becomes reachable again.
func (a *App) emitClearEnvNotification(tenant, environment, source string) {
	a.emit(appNotificationClearEvent, appNotificationClearPayload{
		Tenant:      tenant,
		Environment: environment,
		Source:      source,
	})
}

func cloudContextDisplayName(status eruncommon.CloudContextStatus) string {
	if name := strings.TrimSpace(status.KubernetesContext); name != "" {
		return name
	}
	return strings.TrimSpace(status.Name)
}
