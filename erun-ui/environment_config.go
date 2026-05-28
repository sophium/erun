package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/wailsapp/wails/v2/pkg/runtime"
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
	registry := a.effectiveEnvironmentContainerRegistry(selection.Tenant, selection.Environment, config)
	return a.environmentConfigToUI(selection.Tenant, config, selection.Environment, registry, ports)
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
	a.reconcileWorkspaceSyncForSelection(selection, updated.SSHD.WorkspaceSync.Enabled)
	a.reconcileCloudCredentialsRefresherForSelection(selection, updated.RemoteHostCredentials && updated.RemoteWorktree())
	ports, err := eruncommon.ResolveEnvironmentLocalPorts(a.deps.store, selection.Tenant, selection.Environment)
	if err != nil {
		return uiEnvironmentConfig{}, err
	}
	registry := a.effectiveEnvironmentContainerRegistry(selection.Tenant, selection.Environment, updated)
	return a.environmentConfigToUI(selection.Tenant, updated, selection.Environment, registry, ports)
}

// SetEnvironmentAutoStart persists the desktop's auto-start preference for one
// environment. mode is "ask" (clear the override and prompt again on next
// open), "always" (start the linked cloud context without prompting), or
// "never" (skip auto-start and render the "Start environment" empty state).
// The setting only affects the desktop's openSelection branch; CLI users keep
// the unconditional preflight start.
func (a *App) SetEnvironmentAutoStart(selection uiSelection, mode string) (uiEnvironmentConfig, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return uiEnvironmentConfig{}, fmt.Errorf("tenant and environment are required")
	}
	value, err := parseAutoStartMode(mode)
	if err != nil {
		return uiEnvironmentConfig{}, err
	}
	existing, _, err := a.deps.store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return uiEnvironmentConfig{}, err
	}
	existing.AutoStart = value
	if err := a.deps.store.SaveEnvConfig(selection.Tenant, existing); err != nil {
		return uiEnvironmentConfig{}, err
	}
	ports, err := eruncommon.ResolveEnvironmentLocalPorts(a.deps.store, selection.Tenant, selection.Environment)
	if err != nil {
		return uiEnvironmentConfig{}, err
	}
	registry := a.effectiveEnvironmentContainerRegistry(selection.Tenant, selection.Environment, existing)
	return a.environmentConfigToUI(selection.Tenant, existing, selection.Environment, registry, ports)
}

func parseAutoStartMode(mode string) (*bool, error) {
	switch strings.TrimSpace(mode) {
	case "", "ask":
		return nil, nil
	case "always":
		v := true
		return &v, nil
	case "never":
		v := false
		return &v, nil
	}
	return nil, fmt.Errorf("invalid auto-start mode %q (expected ask, always, or never)", mode)
}

func (a *App) ChooseWorkspaceSyncLocalFolder(selection uiSelection, current string) (string, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return "", fmt.Errorf("tenant and environment are required")
	}
	if a.ctx == nil {
		return "", fmt.Errorf("application context is not ready")
	}
	defaultDirectory := strings.TrimSpace(current)
	if defaultDirectory == "" {
		defaultDirectory = resolveWorkspaceSyncDialogDefaultDirectory(a.deps.findProjectRoot)
	}
	if defaultDirectory != "" {
		if info, err := os.Stat(defaultDirectory); err != nil || !info.IsDir() {
			defaultDirectory = ""
		}
	}
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            fmt.Sprintf("Select local sync folder for %s/%s", selection.Tenant, selection.Environment),
		DefaultDirectory: defaultDirectory,
	})
}

func resolveWorkspaceSyncDialogDefaultDirectory(findProjectRoot eruncommon.ProjectFinderFunc) string {
	if findProjectRoot == nil {
		return ""
	}
	_, projectRoot, err := findProjectRoot()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(projectRoot)
}

func (a *App) updatedEnvironmentConfig(config uiEnvironmentConfig, existing eruncommon.EnvConfig) (eruncommon.EnvConfig, error) {
	updated := environmentConfigFromUI(config, existing)
	if _, err := updated.Idle.Resolve(); err != nil {
		return eruncommon.EnvConfig{}, err
	}
	if err := eruncommon.ValidateRuntimePodResources(updated.RuntimePod); err != nil {
		return eruncommon.EnvConfig{}, err
	}
	if err := a.validateWorkspaceSyncConfig(updated); err != nil {
		return eruncommon.EnvConfig{}, err
	}
	if updated.RemoteWorktree() && strings.TrimSpace(updated.CloudProviderAlias) != "" {
		if _, ok, err := a.linkedCloudContext(updated); err != nil {
			return eruncommon.EnvConfig{}, err
		} else if ok {
			updated.ManagedCloud = true
		}
	}
	return updated, nil
}

func (a *App) validateWorkspaceSyncConfig(config eruncommon.EnvConfig) error {
	if !config.SSHD.WorkspaceSync.Enabled {
		return nil
	}
	localPath := strings.TrimSpace(config.SSHD.WorkspaceSync.LocalPath)
	if localPath == "" {
		return fmt.Errorf("local sync folder is required when workspace sync is enabled")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ensureLocalWorkspaceSyncTarget(ctx, localPath); err != nil {
		return fmt.Errorf("local sync folder: %w", err)
	}
	return nil
}

func (a *App) saveRemoteCloudAlias(selection uiSelection, existing, updated eruncommon.EnvConfig) error {
	if !existing.RemoteWorktree() || strings.TrimSpace(updated.CloudProviderAlias) == strings.TrimSpace(existing.CloudProviderAlias) {
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

func (a *App) environmentConfigToUI(tenant string, config eruncommon.EnvConfig, fallbackName, effectiveContainerRegistry string, ports eruncommon.EnvironmentLocalPorts) (uiEnvironmentConfig, error) {
	name := strings.TrimSpace(config.Name)
	if name == "" {
		name = strings.TrimSpace(fallbackName)
	}
	syncStatus := a.workspaceSyncStatus(uiSelection{Tenant: tenant, Environment: name})
	containerRegistry := strings.TrimSpace(config.ContainerRegistry)
	if containerRegistry == "" {
		containerRegistry = strings.TrimSpace(effectiveContainerRegistry)
	}
	ports = eruncommon.LocalPortsForResult(eruncommon.OpenResult{
		EnvConfig:  config,
		LocalPorts: ports,
	})
	workspaceSyncLocalPath := strings.TrimSpace(config.SSHD.WorkspaceSync.LocalPath)
	workspaceSyncEnabled := config.SSHD.WorkspaceSync.Enabled && workspaceSyncLocalPath != ""
	result := uiEnvironmentConfig{
		Name:                 name,
		RepoPath:             strings.TrimSpace(config.RepoPath),
		KubernetesContext:    strings.TrimSpace(config.KubernetesContext),
		ContainerRegistry:    containerRegistry,
		CloudProviderAlias:   strings.TrimSpace(config.CloudProviderAlias),
		CloudProviderAliases: environmentCloudProviderAliases(a.deps.store, config.CloudProviderAlias),
		RuntimeVersion:       strings.TrimSpace(config.RuntimeVersion),
		RuntimePod:           runtimePodConfigToUI(config.RuntimePod),
		SSHD: uiSSHDConfig{
			Enabled:                    config.SSHD.Enabled,
			LocalPort:                  config.SSHD.LocalPort,
			PublicKeyPath:              strings.TrimSpace(config.SSHD.PublicKeyPath),
			WorkspaceSyncEnabled:       workspaceSyncEnabled,
			WorkspaceSyncLocalPath:     workspaceSyncLocalPath,
			WorkspaceSyncStatus:        syncStatus.Status,
			WorkspaceSyncStatusMessage: syncStatus.Message,
		},
		Idle: uiIdleConfig{
			Timeout:          idleConfigValue(config.Idle.Timeout, eruncommon.DefaultEnvironmentIdleTimeout.String()),
			WorkingHours:     idleConfigValue(config.Idle.WorkingHours, eruncommon.DefaultEnvironmentWorkingHours),
			IdleTrafficBytes: config.Idle.IdleTrafficBytes,
		},
		Claude:         claudeConfigToUI(config.Claude),
		ClaudeDefaults: claudeDefaultsForUI(),
		AITool:         strings.TrimSpace(config.AITool),
		LocalPorts: uiEnvironmentLocalPorts{
			RangeStart: ports.RangeStart,
			RangeEnd:   ports.RangeEnd,
			MCP:        ports.MCP,
			API:        ports.API,
			SSH:        ports.SSH,
			MCPStatus:  localPortStatus(ports.MCP),
			APIStatus:  localPortStatus(ports.API),
			SSHStatus:  localPortStatus(ports.SSH),
		},
		Remote:                config.Remote,
		Snapshot:              config.SnapshotEnabled(),
		AutoStart:             copyBoolPtr(config.AutoStart),
		RemoteHostCredentials: config.RemoteHostCredentials,
	}
	if cloudContext, ok, err := a.linkedCloudContext(config); err != nil {
		return uiEnvironmentConfig{}, err
	} else if ok {
		status := cloudContextStatusToUI(cloudContext)
		result.CloudContext = &status
	}
	return result, nil
}

func (a *App) effectiveEnvironmentContainerRegistry(tenant, environment string, config eruncommon.EnvConfig) string {
	if registry := strings.TrimSpace(config.ContainerRegistry); registry != "" {
		return registry
	}
	if a.deps.store == nil {
		return ""
	}
	tenantConfig, _, err := a.deps.store.LoadTenantConfig(strings.TrimSpace(tenant))
	if err != nil {
		return ""
	}
	projectRoot := strings.TrimSpace(tenantConfig.ProjectRoot)
	if projectRoot == "" {
		projectRoot = strings.TrimSpace(config.RepoPath)
	}
	if projectRoot == "" {
		return ""
	}
	loader, ok := a.deps.store.(projectConfigLoader)
	if !ok {
		return ""
	}
	projectConfig, _, err := loader.LoadProjectConfig(projectRoot)
	if err != nil {
		return ""
	}
	return projectConfig.ContainerRegistryForEnvironment(environmentName(environment, config))
}

func environmentName(fallbackName string, config eruncommon.EnvConfig) string {
	if name := strings.TrimSpace(config.Name); name != "" {
		return name
	}
	return strings.TrimSpace(fallbackName)
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
	existing.ContainerRegistry = strings.TrimSpace(config.ContainerRegistry)
	existing.RuntimePod = runtimePodConfigFromUI(config.RuntimePod)
	existing.SSHD.WorkspaceSync.Enabled = config.SSHD.WorkspaceSyncEnabled
	existing.SSHD.WorkspaceSync.LocalPath = strings.TrimSpace(config.SSHD.WorkspaceSyncLocalPath)
	existing.Idle = eruncommon.EnvironmentIdleConfig{
		Timeout:          strings.TrimSpace(config.Idle.Timeout),
		WorkingHours:     strings.TrimSpace(config.Idle.WorkingHours),
		IdleTrafficBytes: config.Idle.IdleTrafficBytes,
	}
	existing.Claude = claudeConfigFromUI(config.Claude)
	existing.AITool = strings.TrimSpace(config.AITool)
	existing.SetSnapshot(config.Snapshot)
	existing.AutoStart = copyBoolPtr(config.AutoStart)
	existing.RemoteHostCredentials = config.RemoteHostCredentials
	return existing
}

func claudeConfigToUI(config eruncommon.EnvironmentClaudeConfig) uiClaudeConfig {
	out := uiClaudeConfig{
		UseMantle:       copyBoolPtr(config.UseMantle),
		UseBedrock:      copyBoolPtr(config.UseBedrock),
		MaxOutputTokens: copyIntPtr(config.MaxOutputTokens),
	}
	if models := config.NormalizedModels(); len(models) > 0 {
		out.Models = models
	}
	return out
}

func claudeConfigFromUI(config uiClaudeConfig) eruncommon.EnvironmentClaudeConfig {
	models := []string(nil)
	if normalized := normalizeUIClaudeModels(config.Models); len(normalized) > 0 {
		models = normalized
	}
	return eruncommon.EnvironmentClaudeConfig{
		UseMantle:       copyBoolPtr(config.UseMantle),
		UseBedrock:      copyBoolPtr(config.UseBedrock),
		Models:          models,
		MaxOutputTokens: copyIntPtr(config.MaxOutputTokens),
	}
}

func claudeDefaultsForUI() uiClaudeDefaults {
	minTokens, maxTokens := eruncommon.ClaudeMaxOutputTokensRange()
	return uiClaudeDefaults{
		UseMantle:       eruncommon.DefaultClaudeUseMantle,
		UseBedrock:      eruncommon.DefaultClaudeUseBedrock,
		Models:          eruncommon.DefaultClaudeAvailableModels(),
		MaxOutputTokens: eruncommon.DefaultClaudeMaxOutputTokens,
		KnownModels:     eruncommon.KnownClaudeModels(),
		MinTokens:       minTokens,
		MaxTokens:       maxTokens,
	}
}

func normalizeUIClaudeModels(models []string) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		if m = strings.TrimSpace(m); m != "" {
			out = append(out, m)
		}
	}
	return out
}

func copyBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	v := *value
	return &v
}

func copyIntPtr(value *int) *int {
	if value == nil {
		return nil
	}
	v := *value
	return &v
}

func runtimePodConfigToUI(config eruncommon.RuntimePodResources) uiRuntimePodConfig {
	config = eruncommon.NormalizeRuntimePodResources(config)
	return uiRuntimePodConfig{
		CPU:    strings.TrimSpace(config.CPU),
		Memory: strings.TrimSpace(config.Memory),
	}
}

func runtimePodConfigFromUI(config uiRuntimePodConfig) eruncommon.RuntimePodResources {
	return eruncommon.NormalizeRuntimePodResources(eruncommon.RuntimePodResources{
		CPU:    strings.TrimSpace(config.CPU),
		Memory: strings.TrimSpace(config.Memory),
	})
}

func idleConfigValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

// linkedCloudContext returns the cloud context backing the supplied
// env, with its Status field populated from the in-memory cache the
// background poller maintains. The persisted config no longer carries
// Status, so callers that need a current value must consult the cache;
// a missing cache entry surfaces as Status="" and callers must treat
// that as "not yet observed."
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
			status.Status = a.cloudContextStatus(context.Name)
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
	a.clearIdleStopsForCloudContext(status.Name)
	a.setCloudContextStatusInCache(status.Name, status.Status)
	a.emitAppStatus(fmt.Sprintf("Cloud context %s is running. Opening environment...", cloudContextDisplayName(status)), true)
	return status, true, nil
}

func (a *App) stopCloudContext(name string) (eruncommon.CloudContextStatus, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	status, err := a.deps.stopCloudContext(ctx, name)
	if err != nil {
		return status, err
	}
	a.setCloudContextStatusInCache(status.Name, status.Status)
	return status, nil
}
