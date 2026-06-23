package main

import (
	"context"
	"errors"
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
	return a.environmentConfigToUI(selection.Tenant, config, selection.Environment, ports)
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
	updated, err := a.persistEnvironmentConfig(selection, config, existing)
	if err != nil {
		return uiEnvironmentConfig{}, err
	}
	ports, err := eruncommon.ResolveEnvironmentLocalPorts(a.deps.store, selection.Tenant, selection.Environment)
	if err != nil {
		return uiEnvironmentConfig{}, err
	}
	return a.environmentConfigToUI(selection.Tenant, updated, selection.Environment, ports)
}

// persistEnvironmentConfig builds the updated env config from the edited UI
// values and existing config, routes the container-registry list to its
// owning store, applies a changed remote cloud alias, saves the env config,
// and reconciles the workspace-sync and cloud-credentials refreshers. The
// steps run in the same order and persist the same fields as before; it
// returns the saved config so the caller can render it back to the UI.
func (a *App) persistEnvironmentConfig(selection uiSelection, config uiEnvironmentConfig, existing eruncommon.EnvConfig) (eruncommon.EnvConfig, error) {
	updated, err := a.updatedEnvironmentConfig(config, existing)
	if err != nil {
		return eruncommon.EnvConfig{}, err
	}
	registries, err := uiToContainerRegistries(config.ContainerRegistries)
	if err != nil {
		return eruncommon.EnvConfig{}, err
	}
	if err := a.applyContainerRegistries(selection, &updated, registries); err != nil {
		return eruncommon.EnvConfig{}, err
	}
	if err := a.saveRemoteCloudAlias(selection, existing, updated); err != nil {
		return eruncommon.EnvConfig{}, err
	}
	if err := a.deps.store.SaveEnvConfig(selection.Tenant, updated); err != nil {
		return eruncommon.EnvConfig{}, err
	}
	a.reconcileWorkspaceSyncForSelection(selection, updated.SSHD.WorkspaceSync.Enabled)
	a.reconcileCloudCredentialsRefresherForSelection(selection, updated.HasAWSCloudAlias() && updated.RemoteWorktree())
	return updated, nil
}

// applyContainerRegistries persists the edited marked list to the place that
// drives resolution: project config (.erun/config.yaml) for local-agent envs,
// whose build/deploy resolvers read the project list, and the env config for
// remote/runtime envs, whose project config is not on the local machine.
func (a *App) applyContainerRegistries(selection uiSelection, updated *eruncommon.EnvConfig, registries eruncommon.ContainerRegistries) error {
	if updated.ResolvedType() == eruncommon.EnvironmentTypeLocalAgent {
		updated.ContainerRegistries = nil
		return a.saveEnvironmentProjectRegistries(selection.Tenant, *updated, registries)
	}
	updated.ContainerRegistries = registries.Clone()
	return nil
}

// saveEnvironmentProjectRegistries writes a local-agent env's marked list into
// the project's .erun/config.yaml as a per-env override (collapsing to the
// project default when they match). Fails clearly when the project root or a
// project-config store can't be resolved — a local-agent env's list has nowhere
// else to live.
func (a *App) saveEnvironmentProjectRegistries(tenant string, config eruncommon.EnvConfig, registries eruncommon.ContainerRegistries) error {
	projectRoot, store, ok := a.environmentProjectConfigStore(config)
	if !ok {
		return fmt.Errorf("cannot resolve the project root for %q; a local-agent environment's container registries are stored in its repo's .erun/config.yaml", strings.TrimSpace(config.Name))
	}
	projectConfig, _, err := store.LoadProjectConfig(projectRoot)
	if err != nil && !errors.Is(err, eruncommon.ErrNotInitialized) {
		return err
	}
	projectConfig.SetContainerRegistriesForEnvironment(environmentName(config.Name, config), registries)
	return store.SaveProjectConfig(projectRoot, projectConfig)
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
	return a.environmentConfigToUI(selection.Tenant, existing, selection.Environment, ports)
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

// ChooseLocalRepoPath opens a native directory picker for the env-init
// dialog's "Local repo path" field. local-agent envs mount this path into
// the agent pod as the worktree, so the user has to pick a real directory
// on this machine — typing absolute paths by hand is error-prone. Returns
// the empty string if the user cancels the dialog.
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

func (a *App) environmentConfigToUI(tenant string, config eruncommon.EnvConfig, fallbackName string, ports eruncommon.EnvironmentLocalPorts) (uiEnvironmentConfig, error) {
	name := strings.TrimSpace(config.Name)
	if name == "" {
		name = strings.TrimSpace(fallbackName)
	}
	syncStatus := a.workspaceSyncStatus(uiSelection{Tenant: tenant, Environment: name})
	registries := containerRegistriesToUI(a.effectiveEnvironmentContainerRegistries(tenant, fallbackName, config))
	ports = eruncommon.LocalPortsForResult(eruncommon.OpenResult{
		EnvConfig:  config,
		LocalPorts: ports,
	})
	workspaceSyncLocalPath := strings.TrimSpace(config.SSHD.WorkspaceSync.LocalPath)
	workspaceSyncEnabled := config.SSHD.WorkspaceSync.Enabled && workspaceSyncLocalPath != ""
	result := uiEnvironmentConfig{
		Name:                 name,
		Type:                 config.ResolvedType(),
		LocalRepoPath:        strings.TrimSpace(config.LocalRepoPath),
		RepoPath:             config.EffectiveLocalRepoPath(),
		KubernetesContext:    strings.TrimSpace(config.KubernetesContext),
		ContainerRegistries:  registries,
		CloudProviderAlias:   strings.TrimSpace(config.CloudProviderAlias),
		CloudProviderAliases: environmentCloudProviderAliases(a.deps.store, config.CloudProviderAlias),
		CloudAliasSlots:      environmentCloudAliasSlots(a.deps.store, config),
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
	}
	if cloudContext, ok, err := a.linkedCloudContext(config); err != nil {
		return uiEnvironmentConfig{}, err
	} else if ok {
		status := cloudContextStatusToUI(cloudContext)
		result.CloudContext = &status
	}
	return result, nil
}

// containerRegistriesToUI converts the resolved marked list to the desktop
// editor's row shape.
func containerRegistriesToUI(list eruncommon.ContainerRegistries) []uiContainerRegistryEntry {
	entries := make([]uiContainerRegistryEntry, 0, len(list))
	for _, entry := range list {
		roles := make([]string, 0, len(entry.Roles))
		for _, role := range entry.Roles {
			roles = append(roles, string(role))
		}
		entries = append(entries, uiContainerRegistryEntry{Registry: strings.TrimSpace(entry.Registry), Roles: roles})
	}
	return entries
}

// uiToContainerRegistries converts the desktop editor's rows back to a marked
// list, dropping blank registries, and validates the marker invariants when the
// list is non-empty so a bad list is rejected on save with an actionable error
// (an empty list clears the env's override and inherits the project default).
func uiToContainerRegistries(entries []uiContainerRegistryEntry) (eruncommon.ContainerRegistries, error) {
	list := make(eruncommon.ContainerRegistries, 0, len(entries))
	for _, entry := range entries {
		registry := strings.TrimSpace(entry.Registry)
		if registry == "" {
			continue
		}
		roles := make([]eruncommon.RegistryRole, 0, len(entry.Roles))
		for _, role := range entry.Roles {
			if trimmed := strings.TrimSpace(role); trimmed != "" {
				roles = append(roles, eruncommon.RegistryRole(trimmed))
			}
		}
		list = append(list, eruncommon.ContainerRegistryEntry{Registry: registry, Roles: roles})
	}
	if list.IsZero() {
		return nil, nil
	}
	if err := list.Validate(); err != nil {
		return nil, err
	}
	return list, nil
}

// effectiveEnvironmentContainerRegistries resolves the marked list the editor
// shows: the per-env list on the env config (remote/runtime envs) when set,
// otherwise the project's list resolved through the store. Mirrors the
// resolution order the build/deploy resolvers use, but goes through the store
// so the desktop (and its test stub) drive it.
func (a *App) effectiveEnvironmentContainerRegistries(tenant, environment string, config eruncommon.EnvConfig) eruncommon.ContainerRegistries {
	if !config.ContainerRegistries.IsZero() {
		return config.ContainerRegistries
	}
	projectConfig, ok := a.loadEnvironmentProjectConfig(tenant, config)
	if !ok {
		return nil
	}
	return projectConfig.ContainerRegistriesForEnvironment(environmentName(environment, config))
}

// loadEnvironmentProjectConfig loads the project config that backs an env's
// registry list (local-agent envs), resolving the project root from the tenant
// config then the env's repo path. ok is false when the project config can't be
// reached (no project root, store can't load project config, or load failed).
func (a *App) loadEnvironmentProjectConfig(tenant string, config eruncommon.EnvConfig) (eruncommon.ProjectConfig, bool) {
	projectRoot, store, ok := a.environmentProjectConfigStore(config)
	if !ok {
		return eruncommon.ProjectConfig{}, false
	}
	projectConfig, _, err := store.LoadProjectConfig(projectRoot)
	if err != nil {
		return eruncommon.ProjectConfig{}, false
	}
	return projectConfig, true
}

// environmentProjectConfigStore resolves the project root for an env and the
// project-config store, used to read and write a local-agent env's registry
// list in .erun/config.yaml.
func (a *App) environmentProjectConfigStore(config eruncommon.EnvConfig) (string, projectConfigStore, bool) {
	if a.deps.store == nil {
		return "", nil, false
	}
	// The env's own local repo path is the project root (#549: the path moved
	// off TenantConfig onto the env).
	projectRoot := strings.TrimSpace(config.EffectiveLocalRepoPath())
	if projectRoot == "" {
		return "", nil, false
	}
	store, ok := a.deps.store.(projectConfigStore)
	if !ok {
		return "", nil, false
	}
	return projectRoot, store, true
}

func environmentName(fallbackName string, config eruncommon.EnvConfig) string {
	if name := strings.TrimSpace(config.Name); name != "" {
		return name
	}
	return strings.TrimSpace(fallbackName)
}

// environmentCloudProviderTypes is the order cloud-alias slots render in: AWS
// first (the legacy primary), then Cloudflare. Adding a provider type to the
// list gives it its own env selector and sidebar login row.
var environmentCloudProviderTypes = []string{
	eruncommon.CloudProviderAWS,
	eruncommon.CloudProviderCloudflare,
}

// environmentCloudAliasSlots builds the per-provider-type cloud-alias view for
// one env: for each known provider type, the alias currently attached to the
// env for that type (from EnvConfig.ResolvedCloudAliases) plus the configured
// aliases of that type the operator can pick from. A type with no configured
// aliases and nothing attached is omitted so the env settings don't show an
// empty Cloudflare selector before any Cloudflare token exists. A currently
// attached alias whose provider config was deleted is still listed as an option
// so the operator can see and clear it (recognition over recall).
func environmentCloudAliasSlots(store eruncommon.CloudReadStore, config eruncommon.EnvConfig) []uiEnvironmentCloudAlias {
	optionsByType := configuredCloudAliasesByType(store)
	attached := config.ResolvedCloudAliases()
	slots := make([]uiEnvironmentCloudAlias, 0, len(environmentCloudProviderTypes))
	for _, providerType := range environmentCloudProviderTypes {
		options := append([]string(nil), optionsByType[providerType]...)
		current := strings.TrimSpace(attached[providerType])
		if current != "" && !cloudAliasListContains(options, current) {
			options = append([]string{current}, options...)
		}
		if current == "" && len(options) == 0 {
			continue
		}
		slots = append(slots, uiEnvironmentCloudAlias{
			Provider: providerType,
			Alias:    current,
			Options:  options,
		})
	}
	return slots
}

// configuredCloudAliasesByType groups every configured cloud provider alias by
// its provider type so each env slot can offer only the aliases that match.
func configuredCloudAliasesByType(store eruncommon.CloudReadStore) map[string][]string {
	grouped := make(map[string][]string)
	providers, err := eruncommon.ListCloudProviders(store)
	if err != nil {
		return grouped
	}
	for _, provider := range providers {
		alias := strings.TrimSpace(provider.Alias)
		providerType := strings.ToLower(strings.TrimSpace(provider.Provider))
		if alias == "" || providerType == "" {
			continue
		}
		if cloudAliasListContains(grouped[providerType], alias) {
			continue
		}
		grouped[providerType] = append(grouped[providerType], alias)
	}
	return grouped
}

func cloudAliasListContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
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
	applyEnvironmentCloudAliasSlots(&existing, config)
	// ContainerRegistries are written by SaveEnvironmentConfig, which routes the
	// marked list to project config (.erun/config.yaml) for local-agent envs and
	// to the env config for remote/runtime envs.
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
	if config.Type.IsValid() {
		existing.Type = config.Type
	}
	if localRepo := strings.TrimSpace(config.LocalRepoPath); localRepo != "" {
		existing.LocalRepoPath = localRepo
	}
	existing.AutoStart = copyBoolPtr(config.AutoStart)
	existing.AutoUpgrade = config.AutoUpgrade
	existing.DisableBuildScript = config.DisableBuildScript
	if eruncommon.IsValidUpgradeChannel(config.UpgradeChannel) {
		existing.UpgradeChannel = config.UpgradeChannel
	}
	return existing
}

// applyEnvironmentCloudAliasSlots writes the edited per-provider-type cloud
// aliases back onto the env config. Each slot routes by its provider type with
// the same semantics as erun-common's SetEnvironmentCloudProviderAlias: the AWS
// alias stays in the legacy CloudProviderAlias scalar (so every existing AWS
// reader — saveRemoteCloudAlias, linkedCloudContext, the credential refresher —
// is byte-for-byte unchanged), and every other type lives in the per-type
// CloudProviderAliases map. An empty slot value clears that type's attachment
// (the "— None —" option). When the UI sends no slots (older callers, the
// AWS-only single-selector path), the legacy scalar is applied directly so
// behavior is preserved.
func applyEnvironmentCloudAliasSlots(existing *eruncommon.EnvConfig, config uiEnvironmentConfig) {
	if len(config.CloudAliasSlots) == 0 {
		existing.CloudProviderAlias = strings.TrimSpace(config.CloudProviderAlias)
		return
	}
	for _, slot := range config.CloudAliasSlots {
		providerType := strings.ToLower(strings.TrimSpace(slot.Provider))
		alias := strings.TrimSpace(slot.Alias)
		if providerType == "" || providerType == eruncommon.CloudProviderAWS {
			existing.CloudProviderAlias = alias
			continue
		}
		if alias == "" {
			delete(existing.CloudProviderAliases, providerType)
			continue
		}
		if existing.CloudProviderAliases == nil {
			existing.CloudProviderAliases = make(map[string]string)
		}
		existing.CloudProviderAliases[providerType] = alias
	}
}

func claudeConfigToUI(config eruncommon.EnvironmentClaudeConfig) uiClaudeConfig {
	out := uiClaudeConfig{
		UseMantle:       copyBoolPtr(config.UseMantle),
		UseBedrock:      copyBoolPtr(config.UseBedrock),
		MaxOutputTokens: copyIntPtr(config.MaxOutputTokens),
		Effort:          copyStringPtr(config.Effort),
		DefaultModel:    copyStringPtr(config.DefaultModel),
		VerboseDebug:    config.VerboseDebug,
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
		Effort:          copyStringPtr(config.Effort),
		DefaultModel:    copyStringPtr(config.DefaultModel),
		VerboseDebug:    config.VerboseDebug,
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
		Effort:          defaultClaudeEffort,
		EffortLevels:    claudeEffortLevelOptions(),
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

func copyStringPtr(value *string) *string {
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
