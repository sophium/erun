package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) LoadERunConfig() (uiERunConfig, error) {
	config, _, err := a.deps.store.LoadERunConfig()
	if err != nil {
		return uiERunConfig{}, err
	}
	return erunConfigToUI(config), nil
}

func (a *App) SaveERunConfig(config uiERunConfig) (uiERunConfig, error) {
	existing, _, err := a.deps.store.LoadERunConfig()
	if errors.Is(err, eruncommon.ErrNotInitialized) {
		existing = eruncommon.ERunConfig{}
	} else if err != nil {
		return uiERunConfig{}, err
	}
	updated := eruncommon.ERunConfig{
		DefaultTenant:  strings.TrimSpace(config.DefaultTenant),
		CloudProviders: existing.CloudProviders,
		CloudContexts:  existing.CloudContexts,
	}
	if err := a.deps.store.SaveERunConfig(updated); err != nil {
		return uiERunConfig{}, err
	}
	return erunConfigToUI(updated), nil
}

func (a *App) LoadCloudProviderStatuses() ([]uiCloudProviderStatus, error) {
	statuses, err := eruncommon.ListCloudProviderStatuses(a.deps.store, eruncommon.CloudDependencies{})
	if err != nil {
		return nil, err
	}
	return cloudProviderStatusesToUI(statuses), nil
}

func (a *App) LoadCloudContextStatuses() ([]uiCloudContextStatus, error) {
	statuses, err := eruncommon.ListCloudContextStatuses(a.deps.store)
	if err != nil {
		return nil, err
	}
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
	return cloudContextStatusToUI(status), nil
}

func (a *App) StopCloudContext(name string) (uiCloudContextStatus, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	status, err := a.deps.stopCloudContext(ctx, name)
	if err != nil {
		return uiCloudContextStatus{}, err
	}
	return cloudContextStatusToUI(status), nil
}

func (a *App) StartCloudContext(name string) (uiCloudContextStatus, error) {
	status, err := eruncommon.StartCloudContext(eruncommon.Context{}, a.deps.store, eruncommon.CloudContextParams{Name: name}, a.deps.cloudContextDeps)
	if err != nil {
		return uiCloudContextStatus{}, err
	}
	a.clearIdleStopsForCloudContext(status.Name)
	return cloudContextStatusToUI(status), nil
}

func (a *App) SaveAWSCloudProviderAlias(input uiAWSCloudAliasInput) (uiCloudProviderStatus, error) {
	provider, err := eruncommon.SaveCloudProviderConfig(a.deps.store, eruncommon.CloudProviderConfig{
		Alias:       strings.TrimSpace(input.Alias),
		Provider:    eruncommon.CloudProviderAWS,
		Username:    strings.TrimSpace(input.Username),
		AccountID:   strings.TrimSpace(input.AccountID),
		Profile:     strings.TrimSpace(input.Profile),
		SSORegion:   strings.TrimSpace(input.SSORegion),
		SSOStartURL: strings.TrimSpace(input.SSOStartURL),
	})
	if err != nil {
		return uiCloudProviderStatus{}, err
	}
	return cloudProviderStatusToUI(eruncommon.CloudProviderTokenStatus(provider, eruncommon.CloudDependencies{})), nil
}

func (a *App) InitAWSCloudProvider(input uiAWSCloudAliasInput) (uiCloudProviderStatus, error) {
	if strings.TrimSpace(input.Username) != "" || strings.TrimSpace(input.AccountID) != "" {
		return a.SaveAWSCloudProviderAlias(input)
	}
	provider, err := eruncommon.InitAWSCloudProvider(eruncommon.Context{}, a.deps.store, eruncommon.InitAWSCloudProviderParams{
		Profile: strings.TrimSpace(input.Profile),
	}, eruncommon.CloudDependencies{})
	if err != nil {
		return uiCloudProviderStatus{}, err
	}
	return cloudProviderStatusToUI(eruncommon.CloudProviderTokenStatus(provider, eruncommon.CloudDependencies{})), nil
}

func (a *App) LoginCloudProvider(alias string) (uiCloudProviderStatus, error) {
	status, err := eruncommon.LoginCloudProviderAlias(eruncommon.Context{}, a.deps.store, eruncommon.CloudLoginParams{Alias: alias}, eruncommon.CloudDependencies{})
	if err != nil {
		return uiCloudProviderStatus{}, err
	}
	return cloudProviderStatusToUI(status), nil
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
	return tenantConfigToUI(config, tenant), nil
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
	if err := a.deps.store.SaveTenantConfig(updated); err != nil {
		return uiTenantConfig{}, err
	}
	return tenantConfigToUI(updated, tenant), nil
}

func erunConfigToUI(config eruncommon.ERunConfig) uiERunConfig {
	return uiERunConfig{
		DefaultTenant:  strings.TrimSpace(config.DefaultTenant),
		CloudProviders: cloudProviderStatusesToUI(statusesForCloudProviders(config.CloudProviders)),
		CloudContexts:  cloudContextStatusesToUI(statusesForCloudContexts(config.CloudContexts)),
	}
}

func tenantConfigToUI(config eruncommon.TenantConfig, fallbackName string) uiTenantConfig {
	name := strings.TrimSpace(config.Name)
	if name == "" {
		name = strings.TrimSpace(fallbackName)
	}
	result := uiTenantConfig{
		Name:               name,
		DefaultEnvironment: strings.TrimSpace(config.DefaultEnvironment),
		APIURL:             strings.TrimSpace(config.APIURL),
	}
	return result
}

func tenantConfigFromUI(config uiTenantConfig, existing eruncommon.TenantConfig) eruncommon.TenantConfig {
	existing.Name = strings.TrimSpace(config.Name)
	existing.DefaultEnvironment = strings.TrimSpace(config.DefaultEnvironment)
	existing.APIURL = strings.TrimSpace(config.APIURL)
	return existing
}

func statusesForCloudProviders(providers []eruncommon.CloudProviderConfig) []eruncommon.CloudProviderStatus {
	statuses := make([]eruncommon.CloudProviderStatus, 0, len(providers))
	for _, provider := range providers {
		statuses = append(statuses, eruncommon.CloudProviderTokenStatus(provider, eruncommon.CloudDependencies{}))
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
		Alias:     strings.TrimSpace(status.Alias),
		Provider:  strings.TrimSpace(status.Provider),
		Username:  strings.TrimSpace(status.Username),
		AccountID: strings.TrimSpace(status.AccountID),
		Profile:   strings.TrimSpace(status.Profile),
		Status:    strings.TrimSpace(status.Status),
		Message:   strings.TrimSpace(status.Message),
	}
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
		Name:               strings.TrimSpace(context.Name),
		Provider:           strings.TrimSpace(context.Provider),
		CloudProviderAlias: strings.TrimSpace(context.CloudProviderAlias),
		Region:             strings.TrimSpace(context.Region),
		InstanceID:         strings.TrimSpace(context.InstanceID),
		PublicIP:           strings.TrimSpace(context.PublicIP),
		InstanceType:       strings.TrimSpace(context.InstanceType),
		DiskType:           strings.TrimSpace(context.DiskType),
		DiskSizeGB:         context.DiskSizeGB,
		KubernetesContext:  strings.TrimSpace(context.KubernetesContext),
		Status:             strings.TrimSpace(context.Status),
		Message:            strings.TrimSpace(status.Message),
	}
}

func (a *App) emitAppStatus(message string, busy bool) {
	if a.ctx == nil || strings.TrimSpace(message) == "" {
		return
	}
	runtime.EventsEmit(a.ctx, appStatusEvent, appStatusPayload{Message: message, Busy: busy})
}

func cloudContextDisplayName(status eruncommon.CloudContextStatus) string {
	if name := strings.TrimSpace(status.KubernetesContext); name != "" {
		return name
	}
	return strings.TrimSpace(status.Name)
}
