package main

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/workspace"
	"github.com/sophium/erun/erun-common/primitive/open"
	"github.com/sophium/erun/erun-common/primitive/shared"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) LoadEnvironmentConfig(selection uiSelection) (uiEnvironmentConfig, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("load environment config", selection.Tenant, selection.Environment); err != nil {
		return uiEnvironmentConfig{}, err
	}

	config, _, err := a.deps.store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return uiEnvironmentConfig{}, err
	}
	ports, err := configpkg.ResolveEnvironmentLocalPorts(a.deps.store, selection.Tenant, selection.Environment)
	if err != nil {
		return uiEnvironmentConfig{}, err
	}
	return a.environmentConfigToUI(selection.Tenant, config, selection.Environment, ports)
}

func (a *App) SaveEnvironmentConfig(selection uiSelection, config uiEnvironmentConfig) (uiEnvironmentConfig, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("save environment config", selection.Tenant, selection.Environment); err != nil {
		return uiEnvironmentConfig{}, err
	}

	existing, _, err := a.deps.store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return uiEnvironmentConfig{}, err
	}
	updated, err := a.persistEnvironmentConfig(selection, config, existing)
	if err != nil {
		return uiEnvironmentConfig{}, err
	}
	ports, err := configpkg.ResolveEnvironmentLocalPorts(a.deps.store, selection.Tenant, selection.Environment)
	if err != nil {
		return uiEnvironmentConfig{}, err
	}
	return a.environmentConfigToUI(selection.Tenant, updated, selection.Environment, ports)
}

func (a *App) persistEnvironmentConfig(selection uiSelection, config uiEnvironmentConfig, existing configpkg.EnvConfig) (configpkg.EnvConfig, error) {
	updated, err := a.updatedEnvironmentConfig(config, existing)
	if err != nil {
		return configpkg.EnvConfig{}, err
	}
	registries, err := uiToContainerRegistries(config.ContainerRegistries)
	if err != nil {
		return configpkg.EnvConfig{}, err
	}
	if err := a.applyContainerRegistries(selection, &updated, registries); err != nil {
		return configpkg.EnvConfig{}, err
	}
	if err := a.saveRemoteCloudAlias(selection, existing, updated); err != nil {
		return configpkg.EnvConfig{}, err
	}
	if err := a.deps.store.SaveEnvConfig(selection.Tenant, updated); err != nil {
		return configpkg.EnvConfig{}, err
	}
	a.reconcileWorkspaceSyncForSelection(selection, updated.SSHD.WorkspaceSync.Enabled)
	a.reconcileCloudCredentialsRefresherForSelection(selection, updated.HasAWSCloudAlias())
	return updated, nil
}

// SetEnvironmentAutoStart persists the desktop's per-env auto-start preference:
// "ask" prompts again on next open, "always" starts the linked cloud context
// without prompting, "never" shows the "Start environment" empty state. Only
// the desktop honors it; CLI users always run the preflight start.
func (a *App) SetEnvironmentAutoStart(selection uiSelection, mode string) (uiEnvironmentConfig, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("set environment auto-start", selection.Tenant, selection.Environment); err != nil {
		return uiEnvironmentConfig{}, err
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
	ports, err := configpkg.ResolveEnvironmentLocalPorts(a.deps.store, selection.Tenant, selection.Environment)
	if err != nil {
		return uiEnvironmentConfig{}, err
	}
	return a.environmentConfigToUI(selection.Tenant, existing, selection.Environment, ports)
}

// parseAutoStartMode maps a mode to its stored preference; "ask" is the unset
// (nil) one.
func parseAutoStartMode(mode string) (*bool, error) {
	var value *bool
	switch strings.TrimSpace(mode) {
	case "", "ask":
	case "always":
		v := true
		value = &v
	case "never":
		v := false
		value = &v
	default:
		return nil, fmt.Errorf("invalid auto-start mode %q (expected ask, always, or never)", mode)
	}
	return value, nil
}

func (a *App) ChooseWorkspaceSyncLocalFolder(selection uiSelection, current string) (string, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("choose workspace-sync local folder", selection.Tenant, selection.Environment); err != nil {
		return "", err
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

func resolveWorkspaceSyncDialogDefaultDirectory(findProjectRoot shared.ProjectFinderFunc) string {
	if findProjectRoot == nil {
		return ""
	}
	_, projectRoot, err := findProjectRoot()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(projectRoot)
}

// ChooseLocalRepoPath opens a native directory picker for the env-init dialog's
// local repo path. A local-agent env mounts this path into the agent pod as its
// worktree, so it must be a real directory on this machine.
func (a *App) ChooseLocalRepoPath(current string) (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("application context is not ready")
	}
	defaultDirectory := strings.TrimSpace(current)
	if defaultDirectory != "" {
		if info, err := os.Stat(defaultDirectory); err != nil || !info.IsDir() {
			defaultDirectory = ""
		}
	}
	if defaultDirectory == "" {
		defaultDirectory = resolveWorkspaceSyncDialogDefaultDirectory(a.deps.findProjectRoot)
	}
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "Select local repo path",
		DefaultDirectory: defaultDirectory,
	})
}

func (a *App) updatedEnvironmentConfig(config uiEnvironmentConfig, existing configpkg.EnvConfig) (configpkg.EnvConfig, error) {
	updated := environmentConfigFromUI(config, existing)
	// The repository path is the one field the desktop runs on the same machine
	// as, so it is refused here — with the reason env-init gives for the same
	// value — rather than written and left to surface at the next deploy as a
	// failure of the mount. Both callers share ValidateEnvRepoPath, so the
	// dialog accepts exactly the paths init accepts.
	if err := configpkg.ValidateEnvRepoPath(updated.ResolvedType(), updated.LocalRepoPath); err != nil {
		return configpkg.EnvConfig{}, err
	}
	if _, err := updated.Idle.Resolve(); err != nil {
		return configpkg.EnvConfig{}, err
	}
	if err := configpkg.ValidateRuntimePodResources(updated.RuntimePod); err != nil {
		return configpkg.EnvConfig{}, err
	}
	if err := a.validateWorkspaceSyncConfig(updated); err != nil {
		return configpkg.EnvConfig{}, err
	}
	if updated.RemoteWorktree() && strings.TrimSpace(updated.CloudProviderAlias) != "" {
		if _, ok, err := a.linkedCloudContext(updated); err != nil {
			return configpkg.EnvConfig{}, err
		} else if ok {
			updated.ManagedCloud = true
		}
	}
	return updated, nil
}

func (a *App) validateWorkspaceSyncConfig(config configpkg.EnvConfig) error {
	if !config.SSHD.WorkspaceSync.Enabled {
		return nil
	}
	localPath := strings.TrimSpace(config.SSHD.WorkspaceSync.LocalPath)
	if localPath == "" {
		return fmt.Errorf("local sync folder is required when workspace sync is enabled")
	}
	if err := workspace.EnsureLocalWorkspaceSyncTarget(localPath); err != nil {
		return fmt.Errorf("local sync folder: %w", err)
	}
	return nil
}

func (a *App) environmentConfigToUI(tenant string, config configpkg.EnvConfig, fallbackName string, ports configpkg.EnvironmentLocalPorts) (uiEnvironmentConfig, error) {
	name := strings.TrimSpace(config.Name)
	if name == "" {
		name = strings.TrimSpace(fallbackName)
	}
	syncStatus := a.workspaceSyncStatus(uiSelection{Tenant: tenant, Environment: name})
	effectiveRegistries, registriesInherited := a.effectiveEnvironmentContainerRegistries(tenant, fallbackName, config)
	registries := containerRegistriesToUI(effectiveRegistries)
	ports = configpkg.LocalPortsForResult(configpkg.OpenResult{
		EnvConfig:  config,
		LocalPorts: ports,
	})
	workspaceSyncLocalPath := strings.TrimSpace(config.SSHD.WorkspaceSync.LocalPath)
	workspaceSyncEnabled := config.SSHD.WorkspaceSync.Enabled && workspaceSyncLocalPath != ""
	gateway := a.resolveOpenRouterConfig()
	result := uiEnvironmentConfig{
		Name:                         name,
		Hosted:                       hostedEnvironmentToUI(config),
		Type:                         config.ResolvedType(),
		LocalRepoPath:                strings.TrimSpace(config.LocalRepoPath),
		RepoPath:                     config.EffectiveLocalRepoPath(),
		KubernetesContext:            strings.TrimSpace(config.KubernetesContext),
		ContainerRegistries:          registries,
		ContainerRegistriesInherited: registriesInherited,
		CloudProviderAlias:           strings.TrimSpace(config.CloudProviderAlias),
		CloudProviderAliases:         environmentCloudProviderAliases(a.deps.store, config.CloudProviderAlias),
		CloudAliasSlots:              environmentCloudAliasSlots(a.deps.store, config),
		RuntimeVersion:               strings.TrimSpace(config.RuntimeVersion),
		RuntimeChart:                 strings.TrimSpace(config.RuntimeChart),
		RuntimeRegistry:              strings.TrimSpace(config.RuntimeRegistry),
		ImagePullSecrets:             trimmedNonEmpty(config.ImagePullSecrets),
		RuntimePod:                   runtimePodConfigToUI(config.RuntimePod),
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
			Timeout:          idleConfigValue(config.Idle.Timeout, configpkg.DefaultEnvironmentIdleTimeout.String()),
			WorkingHours:     idleConfigValue(config.Idle.WorkingHours, configpkg.DefaultEnvironmentWorkingHours),
			IdleTrafficBytes: config.Idle.IdleTrafficBytes,
		},
		Claude:            claudeConfigToUI(config.Claude),
		ClaudeDefaults:    claudeDefaultsForUI(gateway),
		AITool:            strings.TrimSpace(config.AITool),
		AILaunchSignature: open.AISessionLaunchCommand(config.AITool, config.Claude, gateway, tenant, name),
		LocalPorts: uiEnvironmentLocalPorts{
			RangeStart:          ports.RangeStart,
			RangeEnd:            ports.RangeEnd,
			MCP:                 ports.MCP,
			API:                 ports.API,
			SSH:                 ports.SSH,
			ContributeApp:       ports.ContributeApp,
			MCPStatus:           localPortStatus(ports.MCP),
			APIStatus:           localPortStatus(ports.API),
			SSHStatus:           localPortStatus(ports.SSH),
			ContributeAppStatus: localPortStatus(ports.ContributeApp),
		},
		AutoStart:          copyBoolPtr(config.AutoStart),
		AutoUpgrade:        config.AutoUpgrade,
		UpgradeChannel:     config.ResolvedUpgradeChannel(),
		DisableBuildScript: config.DisableBuildScript,
		PlatformAccount:    config.PlatformAccount,
		MountSource:        config.MountSource,
		RepoURL:            strings.TrimSpace(config.RepoURL),
		DeployComponents:   append([]string(nil), config.Deploy.Components...),
	}
	if cloudContext, ok, err := a.linkedCloudContext(config); err != nil {
		return uiEnvironmentConfig{}, err
	} else if ok {
		status := cloudContextStatusToUI(cloudContext)
		result.CloudContext = &status
	}
	return result, nil
}

func environmentName(fallbackName string, config configpkg.EnvConfig) string {
	if name := strings.TrimSpace(config.Name); name != "" {
		return name
	}
	return strings.TrimSpace(fallbackName)
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

func environmentConfigFromUI(config uiEnvironmentConfig, existing configpkg.EnvConfig) configpkg.EnvConfig {
	existing.Name = strings.TrimSpace(config.Name)
	applyEnvironmentCloudAliasSlots(&existing, config)
	// ContainerRegistries are intentionally not set here; SaveEnvironmentConfig
	// routes the marked list to the right store by env type.
	existing.RuntimePod = runtimePodConfigFromUI(config.RuntimePod)
	existing.SSHD.WorkspaceSync.Enabled = config.SSHD.WorkspaceSyncEnabled
	existing.SSHD.WorkspaceSync.LocalPath = strings.TrimSpace(config.SSHD.WorkspaceSyncLocalPath)
	existing.Idle = configpkg.EnvironmentIdleConfig{
		Timeout:          strings.TrimSpace(config.Idle.Timeout),
		WorkingHours:     strings.TrimSpace(config.Idle.WorkingHours),
		IdleTrafficBytes: config.Idle.IdleTrafficBytes,
	}
	existing.Claude = claudeConfigFromUI(config.Claude)
	existing.AITool = strings.TrimSpace(config.AITool)
	if config.Type.IsValid() {
		existing.Type = config.Type
	}
	if localRepo := strings.TrimSpace(config.LocalRepoPath); localRepo != "" {
		existing.LocalRepoPath = localRepo
	}
	existing.AutoStart = copyBoolPtr(config.AutoStart)
	existing.AutoUpgrade = config.AutoUpgrade
	existing.DisableBuildScript = config.DisableBuildScript
	// The chart the env rides is the operator's standing statement of one of the
	// four deploy coordinates; a redeploy installs it, so saving it is enough --
	// nothing derives it.
	existing.RuntimeChart = strings.TrimSpace(config.RuntimeChart)
	// Both are pull-time coordinates the operator states once: the registry
	// erun's own chart comes from, and the secrets the pod pulls its image
	// with. An empty list clears the secrets rather than keeping a stale one.
	existing.RuntimeRegistry = strings.TrimSpace(config.RuntimeRegistry)
	existing.ImagePullSecrets = trimmedNonEmpty(config.ImagePullSecrets)
	existing.PlatformAccount = config.PlatformAccount
	existing.MountSource = config.MountSource
	existing.RepoURL = strings.TrimSpace(config.RepoURL)
	// Preserve the sibling deploy.timeout; only the saved component selection is
	// edited here. An empty slice clears the saved default (omitempty), reverting
	// the env to the repo plan / runtime-only default.
	existing.Deploy.Components = trimmedComponentNames(config.DeployComponents)
	if configpkg.IsValidUpgradeChannel(config.UpgradeChannel) {
		existing.UpgradeChannel = config.UpgradeChannel
	}
	return existing
}

func trimmedComponentNames(names []string) []string {
	return trimmedNonEmpty(names)
}

// trimmedNonEmpty drops blanks so an omitempty field clears rather than
// persisting a list of empty strings the operator never entered.
func trimmedNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, raw := range values {
		if value := strings.TrimSpace(raw); value != "" {
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil
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

func copyStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	v := *value
	return &v
}

func runtimePodConfigToUI(config configpkg.RuntimePodResources) uiRuntimePodConfig {
	config = configpkg.NormalizeRuntimePodResources(config)
	return uiRuntimePodConfig{
		CPU:    strings.TrimSpace(config.CPU),
		Memory: strings.TrimSpace(config.Memory),
	}
}

func runtimePodConfigFromUI(config uiRuntimePodConfig) configpkg.RuntimePodResources {
	return configpkg.NormalizeRuntimePodResources(configpkg.RuntimePodResources{
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
