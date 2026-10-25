package settings

import (
	"fmt"
	"net"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/localports"
	"github.com/sophium/erun/erun-common/base/config/repopath"
	"github.com/sophium/erun/erun-common/base/config/runtimeresources"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/workspace"
	"github.com/sophium/erun/erun-common/primitive/open/ailaunch"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func (s *Service) LoadEnvironmentConfig(selection uimodel.Selection) (EnvironmentConfig, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("load environment config", selection.Tenant, selection.Environment); err != nil {
		return EnvironmentConfig{}, err
	}

	config, _, err := s.d.Store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return EnvironmentConfig{}, err
	}
	ports, err := localports.ResolveEnvironmentLocalPorts(s.d.Store, selection.Tenant, selection.Environment)
	if err != nil {
		return EnvironmentConfig{}, err
	}
	return s.environmentConfigToUI(selection.Tenant, config, selection.Environment, ports)
}

func (s *Service) SaveEnvironmentConfig(selection uimodel.Selection, config EnvironmentConfig) (EnvironmentConfig, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("save environment config", selection.Tenant, selection.Environment); err != nil {
		return EnvironmentConfig{}, err
	}

	existing, _, err := s.d.Store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return EnvironmentConfig{}, err
	}
	updated, err := s.persistEnvironmentConfig(selection, config, existing)
	if err != nil {
		return EnvironmentConfig{}, err
	}
	ports, err := localports.ResolveEnvironmentLocalPorts(s.d.Store, selection.Tenant, selection.Environment)
	if err != nil {
		return EnvironmentConfig{}, err
	}
	return s.environmentConfigToUI(selection.Tenant, updated, selection.Environment, ports)
}

func (s *Service) persistEnvironmentConfig(selection uimodel.Selection, config EnvironmentConfig, existing configpkg.EnvConfig) (configpkg.EnvConfig, error) {
	updated, err := s.updatedEnvironmentConfig(config, existing)
	if err != nil {
		return configpkg.EnvConfig{}, err
	}
	registries, err := uiToContainerRegistries(config.ContainerRegistries)
	if err != nil {
		return configpkg.EnvConfig{}, err
	}
	if err := s.applyContainerRegistries(selection, &updated, registries); err != nil {
		return configpkg.EnvConfig{}, err
	}
	if err := s.saveRemoteCloudAlias(selection, existing, updated); err != nil {
		return configpkg.EnvConfig{}, err
	}
	if err := s.d.Store.SaveEnvConfig(selection.Tenant, updated); err != nil {
		return configpkg.EnvConfig{}, err
	}
	s.d.ReconcileWorkspaceSync(selection, updated.SSHD.WorkspaceSync.Enabled)
	s.d.CloudStatuses.ReconcileRefresher(selection, updated.HasAWSCloudAlias())
	return updated, nil
}

// SetEnvironmentAutoStart persists the desktop-only auto-start preference.
func (s *Service) SetEnvironmentAutoStart(selection uimodel.Selection, mode string) (EnvironmentConfig, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("set environment auto-start", selection.Tenant, selection.Environment); err != nil {
		return EnvironmentConfig{}, err
	}
	value, err := parseAutoStartMode(mode)
	if err != nil {
		return EnvironmentConfig{}, err
	}
	existing, _, err := s.d.Store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return EnvironmentConfig{}, err
	}
	existing.AutoStart = value
	if err := s.d.Store.SaveEnvConfig(selection.Tenant, existing); err != nil {
		return EnvironmentConfig{}, err
	}
	ports, err := localports.ResolveEnvironmentLocalPorts(s.d.Store, selection.Tenant, selection.Environment)
	if err != nil {
		return EnvironmentConfig{}, err
	}
	return s.environmentConfigToUI(selection.Tenant, existing, selection.Environment, ports)
}

// parseAutoStartMode maps "ask" to nil.
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

func (s *Service) updatedEnvironmentConfig(config EnvironmentConfig, existing configpkg.EnvConfig) (configpkg.EnvConfig, error) {
	updated := environmentConfigFromUI(config, existing)
	// Shares ValidateEnvRepoPath with init so both accept exactly the same paths.
	if err := repopath.ValidateEnvRepoPath(updated.ResolvedType(), updated.LocalRepoPath); err != nil {
		return configpkg.EnvConfig{}, err
	}
	if _, err := updated.Idle.Resolve(); err != nil {
		return configpkg.EnvConfig{}, err
	}
	if err := runtimeresources.ValidateRuntimePodResources(updated.RuntimePod); err != nil {
		return configpkg.EnvConfig{}, err
	}
	if err := s.validateWorkspaceSyncConfig(updated); err != nil {
		return configpkg.EnvConfig{}, err
	}
	if updated.RemoteWorktree() && strings.TrimSpace(updated.CloudProviderAlias) != "" {
		if _, ok, err := s.LinkedCloudContext(updated); err != nil {
			return configpkg.EnvConfig{}, err
		} else if ok {
			updated.ManagedCloud = true
		}
	}
	return updated, nil
}

func (s *Service) validateWorkspaceSyncConfig(config configpkg.EnvConfig) error {
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

func (s *Service) environmentConfigToUI(tenant string, config configpkg.EnvConfig, fallbackName string, ports model.EnvironmentLocalPorts) (EnvironmentConfig, error) {
	name := strings.TrimSpace(config.Name)
	if name == "" {
		name = strings.TrimSpace(fallbackName)
	}
	syncStatus, syncMessage := s.d.WorkspaceSyncStatus(uimodel.Selection{Tenant: tenant, Environment: name})
	effectiveRegistries, registriesInherited := s.EffectiveContainerRegistries(tenant, fallbackName, config)
	registries := containerRegistriesToUI(effectiveRegistries)
	ports = localports.LocalPortsForResult(configpkg.OpenResult{
		EnvConfig:  config,
		LocalPorts: ports,
	})
	workspaceSyncLocalPath := strings.TrimSpace(config.SSHD.WorkspaceSync.LocalPath)
	workspaceSyncEnabled := config.SSHD.WorkspaceSync.Enabled && workspaceSyncLocalPath != ""
	gateway := s.ResolveOpenRouterConfig()
	result := EnvironmentConfig{
		Name:                         name,
		Hosted:                       hostedEnvironmentToUI(config),
		Type:                         config.ResolvedType(),
		LocalRepoPath:                strings.TrimSpace(config.LocalRepoPath),
		RepoPath:                     config.EffectiveLocalRepoPath(),
		KubernetesContext:            strings.TrimSpace(config.KubernetesContext),
		ContainerRegistries:          registries,
		ContainerRegistriesInherited: registriesInherited,
		CloudProviderAlias:           strings.TrimSpace(config.CloudProviderAlias),
		CloudProviderAliases:         environmentCloudProviderAliases(s.d.Store, config.CloudProviderAlias),
		CloudAliasSlots:              environmentCloudAliasSlots(s.d.Store, config),
		RuntimeVersion:               strings.TrimSpace(config.RuntimeVersion),
		RuntimeChart:                 strings.TrimSpace(config.RuntimeChart),
		RuntimeRegistry:              strings.TrimSpace(config.RuntimeRegistry),
		ImagePullSecrets:             uimodel.TrimmedNonEmpty(config.ImagePullSecrets),
		RuntimePod:                   runtimePodConfigToUI(config.RuntimePod),
		SSHD: SSHDConfig{
			Enabled:                    config.SSHD.Enabled,
			LocalPort:                  config.SSHD.LocalPort,
			PublicKeyPath:              strings.TrimSpace(config.SSHD.PublicKeyPath),
			WorkspaceSyncEnabled:       workspaceSyncEnabled,
			WorkspaceSyncLocalPath:     workspaceSyncLocalPath,
			WorkspaceSyncStatus:        syncStatus,
			WorkspaceSyncStatusMessage: syncMessage,
		},
		Idle: IdleConfig{
			Timeout:          idleConfigValue(config.Idle.Timeout, configpkg.DefaultEnvironmentIdleTimeout.String()),
			WorkingHours:     idleConfigValue(config.Idle.WorkingHours, configpkg.DefaultEnvironmentWorkingHours),
			IdleTrafficBytes: config.Idle.IdleTrafficBytes,
		},
		Claude:            claudeConfigToUI(config.Claude),
		ClaudeDefaults:    claudeDefaultsForUI(gateway),
		AITool:            strings.TrimSpace(config.AITool),
		AILaunchSignature: ailaunch.AISessionLaunchCommand(config.AITool, config.Claude, gateway, tenant, name),
		LocalPorts: EnvironmentLocalPorts{
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
	if cloudContext, ok, err := s.LinkedCloudContext(config); err != nil {
		return EnvironmentConfig{}, err
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

func localPortStatus(port int) PortStatus {
	if port <= 0 {
		return PortStatus{Status: "Not assigned"}
	}
	if !CanConnectLocalTCP(port) {
		return PortStatus{Status: "No"}
	}
	return PortStatus{Available: true, Status: "Yes"}
}

// CanConnectLocalTCP is the local-port probe the editor shows; main also uses it as
// the production port check.
func CanConnectLocalTCP(port int) bool {
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

func environmentConfigFromUI(config EnvironmentConfig, existing configpkg.EnvConfig) configpkg.EnvConfig {
	existing.Name = strings.TrimSpace(config.Name)
	applyEnvironmentCloudAliasSlots(&existing, config)
	// ContainerRegistries are routed by env type in SaveEnvironmentConfig.
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
	existing.RuntimeChart = strings.TrimSpace(config.RuntimeChart)
	// An empty list clears the pull secrets rather than keeping a stale one.
	existing.RuntimeRegistry = strings.TrimSpace(config.RuntimeRegistry)
	existing.ImagePullSecrets = uimodel.TrimmedNonEmpty(config.ImagePullSecrets)
	existing.PlatformAccount = config.PlatformAccount
	existing.MountSource = config.MountSource
	existing.RepoURL = strings.TrimSpace(config.RepoURL)
	// Preserve the sibling deploy.timeout; an empty slice clears the saved default.
	existing.Deploy.Components = trimmedComponentNames(config.DeployComponents)
	if configpkg.IsValidUpgradeChannel(config.UpgradeChannel) {
		existing.UpgradeChannel = config.UpgradeChannel
	}
	return existing
}

func trimmedComponentNames(names []string) []string {
	return uimodel.TrimmedNonEmpty(names)
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

func runtimePodConfigToUI(config model.RuntimePodResources) RuntimePodConfig {
	config = runtimeresources.NormalizeRuntimePodResources(config)
	return RuntimePodConfig{
		CPU:    strings.TrimSpace(config.CPU),
		Memory: strings.TrimSpace(config.Memory),
	}
}

func runtimePodConfigFromUI(config RuntimePodConfig) model.RuntimePodResources {
	return runtimeresources.NormalizeRuntimePodResources(model.RuntimePodResources{
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
