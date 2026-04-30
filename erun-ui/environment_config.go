package main

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

func (a *App) LoadEnvironmentConfig(selection uiSelection) (uiEnvironmentConfig, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return uiEnvironmentConfig{}, fmt.Errorf("tenant and environment are required")
	}

	config, _, err := a.deps.store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return uiEnvironmentConfig{}, err
	}
	ports, err := eruncommon.ResolveEnvironmentLocalPorts(a.deps.store, selection.Tenant, selection.Environment)
	if err != nil {
		return uiEnvironmentConfig{}, err
	}
	return a.environmentConfigToUI(config, selection.Environment, ports)
}

func (a *App) SaveEnvironmentConfig(selection uiSelection, config uiEnvironmentConfig) (uiEnvironmentConfig, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return uiEnvironmentConfig{}, fmt.Errorf("tenant and environment are required")
	}

	existing, _, err := a.deps.store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return uiEnvironmentConfig{}, err
	}
	updated, err := a.updatedEnvironmentConfig(config, existing)
	if err != nil {
		return uiEnvironmentConfig{}, err
	}
	if err := a.saveRemoteCloudAlias(selection, existing, updated); err != nil {
		return uiEnvironmentConfig{}, err
	}
	if err := a.deps.store.SaveEnvConfig(selection.Tenant, updated); err != nil {
		return uiEnvironmentConfig{}, err
	}
	ports, err := eruncommon.ResolveEnvironmentLocalPorts(a.deps.store, selection.Tenant, selection.Environment)
	if err != nil {
		return uiEnvironmentConfig{}, err
	}
	return a.environmentConfigToUI(updated, selection.Environment, ports)
}

func (a *App) updatedEnvironmentConfig(config uiEnvironmentConfig, existing eruncommon.EnvConfig) (eruncommon.EnvConfig, error) {
	updated := environmentConfigFromUI(config, existing)
	if _, err := updated.Idle.Resolve(); err != nil {
		return eruncommon.EnvConfig{}, err
	}
	if updated.Remote && strings.TrimSpace(updated.CloudProviderAlias) != "" {
		if _, ok, err := a.linkedCloudContext(updated); err != nil {
			return eruncommon.EnvConfig{}, err
		} else if ok {
			updated.ManagedCloud = true
		}
	}
	return updated, nil
}

func (a *App) saveRemoteCloudAlias(selection uiSelection, existing, updated eruncommon.EnvConfig) error {
	if !existing.Remote || strings.TrimSpace(updated.CloudProviderAlias) == strings.TrimSpace(existing.CloudProviderAlias) {
		return nil
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := a.ensureMCPAvailable(ctx, result); err != nil {
		return err
	}
	_, err = a.deps.setRemoteCloudAlias(ctx, mcpEndpointForOpenResult(result), selection.Tenant, selection.Environment, updated.CloudProviderAlias)
	return err
}

func (a *App) environmentConfigToUI(config eruncommon.EnvConfig, fallbackName string, ports eruncommon.EnvironmentLocalPorts) (uiEnvironmentConfig, error) {
	name := strings.TrimSpace(config.Name)
	if name == "" {
		name = strings.TrimSpace(fallbackName)
	}
	ports = eruncommon.LocalPortsForResult(eruncommon.OpenResult{
		EnvConfig:  config,
		LocalPorts: ports,
	})
	result := uiEnvironmentConfig{
		Name:                 name,
		RepoPath:             strings.TrimSpace(config.RepoPath),
		KubernetesContext:    strings.TrimSpace(config.KubernetesContext),
		ContainerRegistry:    strings.TrimSpace(config.ContainerRegistry),
		CloudProviderAlias:   strings.TrimSpace(config.CloudProviderAlias),
		CloudProviderAliases: environmentCloudProviderAliases(a.deps.store, config.CloudProviderAlias),
		RuntimeVersion:       strings.TrimSpace(config.RuntimeVersion),
		SSHD: uiSSHDConfig{
			Enabled:       config.SSHD.Enabled,
			LocalPort:     config.SSHD.LocalPort,
			PublicKeyPath: strings.TrimSpace(config.SSHD.PublicKeyPath),
		},
		Idle: uiIdleConfig{
			Timeout:          idleConfigValue(config.Idle.Timeout, eruncommon.DefaultEnvironmentIdleTimeout.String()),
			WorkingHours:     idleConfigValue(config.Idle.WorkingHours, eruncommon.DefaultEnvironmentWorkingHours),
			IdleTrafficBytes: config.Idle.IdleTrafficBytes,
		},
		LocalPorts: uiEnvironmentLocalPorts{
			RangeStart: ports.RangeStart,
			RangeEnd:   ports.RangeEnd,
			MCP:        ports.MCP,
			SSH:        ports.SSH,
			MCPStatus:  localPortStatus(ports.MCP),
			SSHStatus:  localPortStatus(ports.SSH),
		},
		Remote:   config.Remote,
		Snapshot: config.SnapshotEnabled(),
	}
	if cloudContext, ok, err := a.linkedCloudContext(config); err != nil {
		return uiEnvironmentConfig{}, err
	} else if ok {
		status := cloudContextStatusToUI(cloudContext)
		result.CloudContext = &status
	}
	return result, nil
}

func environmentCloudProviderAliases(store eruncommon.CloudReadStore, current string) []string {
	providers, err := eruncommon.ListCloudProviders(store)
	if err != nil {
		return nil
	}
	current = strings.TrimSpace(current)
	aliases := make([]string, 0, len(providers)+1)
	seen := make(map[string]struct{}, len(providers)+1)
	for _, provider := range providers {
		alias := strings.TrimSpace(provider.Alias)
		if alias == "" {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		aliases = append(aliases, alias)
		seen[alias] = struct{}{}
	}
	if current != "" {
		if _, ok := seen[current]; !ok {
			aliases = append([]string{current}, aliases...)
		}
	}
	return aliases
}

func localPortStatus(port int) uiPortStatus {
	if port <= 0 {
		return uiPortStatus{Status: "Not assigned"}
	}
	if !canConnectLocalTCP(port) {
		return uiPortStatus{Status: "No"}
	}
	return uiPortStatus{Available: true, Status: "Yes"}
}

func canConnectLocalTCP(port int) bool {
	if port <= 0 {
		return false
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func environmentConfigFromUI(config uiEnvironmentConfig, existing eruncommon.EnvConfig) eruncommon.EnvConfig {
	existing.Name = strings.TrimSpace(config.Name)
	existing.CloudProviderAlias = strings.TrimSpace(config.CloudProviderAlias)
	existing.Idle = eruncommon.EnvironmentIdleConfig{
		Timeout:          strings.TrimSpace(config.Idle.Timeout),
		WorkingHours:     strings.TrimSpace(config.Idle.WorkingHours),
		IdleTrafficBytes: config.Idle.IdleTrafficBytes,
	}
	existing.SetSnapshot(config.Snapshot)
	return existing
}

func idleConfigValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func (a *App) linkedCloudContext(config eruncommon.EnvConfig) (eruncommon.CloudContextStatus, bool, error) {
	cloudProviderAlias := strings.TrimSpace(config.CloudProviderAlias)
	kubernetesContext := strings.TrimSpace(config.KubernetesContext)
	if kubernetesContext == "" {
		return eruncommon.CloudContextStatus{}, false, nil
	}
	statuses, err := eruncommon.ListCloudContextStatuses(a.deps.store)
	if err != nil {
		return eruncommon.CloudContextStatus{}, false, err
	}
	for _, status := range statuses {
		context := eruncommon.NormalizeCloudContextConfig(status.CloudContextConfig)
		if cloudProviderAlias != "" && strings.TrimSpace(context.CloudProviderAlias) != cloudProviderAlias {
			continue
		}
		if strings.TrimSpace(context.KubernetesContext) == kubernetesContext || strings.TrimSpace(context.Name) == kubernetesContext {
			status.CloudContextConfig = context
			return status, true, nil
		}
	}
	return eruncommon.CloudContextStatus{}, false, nil
}

func (a *App) ensureLinkedCloudContextRunning(config eruncommon.EnvConfig) (eruncommon.CloudContextStatus, bool, error) {
	status, ok, err := a.linkedCloudContext(config)
	if err != nil || !ok {
		return status, ok, err
	}
	if strings.TrimSpace(status.Status) == eruncommon.CloudContextStatusRunning {
		a.emitAppStatus(fmt.Sprintf("Cloud context %s is running. Opening environment...", cloudContextDisplayName(status)), true)
		return status, true, nil
	}
	a.emitAppStatus(fmt.Sprintf("Starting cloud context %s and waiting for Kubernetes access...", cloudContextDisplayName(status)), true)
	status, err = eruncommon.StartCloudContext(eruncommon.Context{}, a.deps.store, eruncommon.CloudContextParams{Name: status.Name}, a.deps.cloudContextDeps)
	if err != nil {
		return eruncommon.CloudContextStatus{}, true, err
	}
	a.emitAppStatus(fmt.Sprintf("Cloud context %s is running. Opening environment...", cloudContextDisplayName(status)), true)
	return status, true, nil
}

func (a *App) stopCloudContext(name string) (eruncommon.CloudContextStatus, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return a.deps.stopCloudContext(ctx, name)
}
