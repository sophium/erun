package eruncommon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ListStore interface {
	OpenStore
	ListTenantConfigs() ([]TenantConfig, error)
	ListEnvConfigs(string) ([]EnvConfig, error)
}

type ListResult struct {
	ConfigDirectory  string                     `json:"configDirectory,omitempty"`
	Defaults         ListDefaultsResult         `json:"defaults"`
	CurrentDirectory ListCurrentDirectoryResult `json:"currentDirectory"`
	CloudProviders   []CloudProviderStatus      `json:"cloudProviders,omitempty"`
	Tenants          []ListTenantResult         `json:"tenants,omitempty"`
	Orchestrators    []ListOrchestratorResult   `json:"orchestrators,omitempty"`
	// OrphanedSSHAliases are the host's own ~/.ssh/config Host blocks naming an
	// erun environment alias no configured environment claims. Nothing else in
	// erun looks at these: `sshd init` writes blocks and env deletion removes
	// the one it wrote, so a block left by a rename, a deleted environment or a
	// turned-off sshd survives indefinitely and can start resolving into
	// whichever environment inherits its local port.
	OrphanedSSHAliases []SSHOrphanedAlias `json:"orphanedSSHAliases,omitempty"`
}

// ListOrchestratorResult is the read-model view of one persisted
// OrchestratorConfig for `erun list`.
type ListOrchestratorResult struct {
	ID           string                      `json:"id"`
	Name         string                      `json:"name"`
	Environments []ListOrchestratorEnvResult `json:"environments,omitempty"`
	// Alias is the erun platform alias this orchestrator declares as its own,
	// empty when it declares none. Reported so the value is readable from the
	// terminal, and not only from the dialog that writes it: a field an
	// operator can set and cannot read back is the gap the operator-settable
	// registry exists to catch.
	Alias string `json:"alias,omitempty"`
	// Directories are the orchestrator's own: paths it works in that belong to
	// no environment at all. Reported so an orchestrator pointed only at
	// directories does not read as having no scope.
	Directories []string `json:"directories,omitempty"`
}

// ListOrchestratorEnvResult is the read-model view of one
// OrchestratorEnvConfig link, including its (possibly undeclared) Role.
type ListOrchestratorEnvResult struct {
	Tenant      string              `json:"tenant"`
	Environment string              `json:"environment"`
	Directory   string              `json:"directory,omitempty"`
	Role        OrchestratorEnvRole `json:"role,omitempty"`
}

type ListDefaultsResult struct {
	Tenant      string `json:"tenant,omitempty"`
	Environment string `json:"environment,omitempty"`
}

type ListCurrentDirectoryResult struct {
	Path             string                     `json:"path,omitempty"`
	Repo             string                     `json:"repo,omitempty"`
	ConfiguredTenant string                     `json:"configuredTenant,omitempty"`
	Effective        *ListEffectiveTargetResult `json:"effective,omitempty"`
	EffectiveError   string                     `json:"effectiveError,omitempty"`
}

type ListEffectiveTargetResult struct {
	Tenant             string                `json:"tenant"`
	Environment        string                `json:"environment"`
	Type               EnvironmentType       `json:"type,omitempty"`
	KubernetesContext  string                `json:"kubernetesContext"`
	CloudProviderAlias string                `json:"cloudProviderAlias,omitempty"`
	RepoPath           string                `json:"repoPath"`
	LocalRepoPath      string                `json:"localRepoPath,omitempty"`
	APIURL             string                `json:"apiUrl,omitempty"`
	LocalPorts         EnvironmentLocalPorts `json:"localPorts,omitempty"`
	SSH                ListSSHResult         `json:"ssh,omitempty"`
}

type ListTenantResult struct {
	Name                      string                  `json:"name"`
	DefaultEnvironment        string                  `json:"defaultEnvironment,omitempty"`
	APIURL                    string                  `json:"apiUrl,omitempty"`
	CloudProviderAliases      []string                `json:"cloudProviderAliases,omitempty"`
	PrimaryCloudProviderAlias string                  `json:"primaryCloudProviderAlias,omitempty"`
	IsDefault                 bool                    `json:"isDefault,omitempty"`
	IsEffective               bool                    `json:"isEffective,omitempty"`
	Environments              []ListEnvironmentResult `json:"environments,omitempty"`
}

type ListEnvironmentResult struct {
	Name                string              `json:"name"`
	Type                EnvironmentType     `json:"type,omitempty"`
	APIURL              string              `json:"apiUrl,omitempty"`
	KubernetesContext   string              `json:"kubernetesContext,omitempty"`
	CloudProviderAlias  string              `json:"cloudProviderAlias,omitempty"`
	RepoPath            string              `json:"repoPath,omitempty"`
	LocalRepoPath       string              `json:"localRepoPath,omitempty"`
	ContainerRegistries ContainerRegistries `json:"containerRegistries,omitempty"`
	RuntimeVersion      string              `json:"runtimeVersion,omitempty"`
	// RuntimeVersionLine names which release line RuntimeVersion's number
	// belongs to. Nil whenever RuntimeVersion itself is empty -- there is
	// nothing to annotate for an environment that has never deployed.
	RuntimeVersionLine *RuntimeVersionLine `json:"runtimeVersionLine,omitempty"`
	// ErunVersion is the erun version this environment's runtime chart carries
	// -- see ResolveErunVersion. Nil whenever it cannot be read from config
	// alone, including whenever RuntimeVersion itself is empty.
	ErunVersion *ErunVersion `json:"erunVersion,omitempty"`
	// RuntimeImageLineMismatch is set only when the environment's recorded and
	// last-observed runtime images name different release lines -- see
	// EnvConfig.RuntimeImageLineMismatch.
	RuntimeImageLineMismatch *RuntimeImageLineMismatchResult `json:"runtimeImageLineMismatch,omitempty"`
	RuntimePod               RuntimePodResources             `json:"runtimePod,omitempty"`
	// Sizing is the environment's standing recommendation, derived from the usage
	// history the in-pod monitor retains. Nil where erun has never observed this
	// environment — which is every environment seen from a host other than its
	// own runtime container, since the history is written by the container that
	// produced it.
	Sizing       *RuntimeSizingRecommendation `json:"sizing,omitempty"`
	ManagedCloud bool                         `json:"managedCloud,omitempty"`
	// Hosted is the platform row this environment corresponds to, when it has
	// one. Nil means the environment is not marked as hosted, which is the
	// normal state: most environments never reach the platform.
	Hosted             *HostedEnvironment      `json:"hosted,omitempty"`
	DisableBuildScript bool                    `json:"disableBuildScript,omitempty"`
	PlatformAccount    bool                    `json:"platformAccount,omitempty"`
	AITool             string                  `json:"aiTool,omitempty"`
	Claude             EnvironmentClaudeConfig `json:"claude,omitempty"`
	Idle               EnvironmentIdleConfig   `json:"idle,omitempty"`
	Deploy             EnvironmentDeployConfig `json:"deploy,omitempty"`
	IsActive           bool                    `json:"isActive,omitempty"`
	LocalPorts         EnvironmentLocalPorts   `json:"localPorts,omitempty"`
	IsDefault          bool                    `json:"isDefault,omitempty"`
	IsEffective        bool                    `json:"isEffective,omitempty"`
	SSH                ListSSHResult           `json:"ssh,omitempty"`
	AutoStart          *bool                   `json:"autoStart,omitempty"`
}

// RuntimeImageLineMismatchResult is the list read-model view of
// EnvConfig.RuntimeImageLineMismatch, present only when it reports a real
// disagreement -- an environment with no recorded history, or one whose
// recorded and observed images agree, has nothing to surface.
type RuntimeImageLineMismatchResult struct {
	RecordedLine string `json:"recordedLine"`
	ObservedLine string `json:"observedLine"`
}

type ListSSHResult struct {
	Enabled                bool   `json:"enabled,omitempty"`
	HostAlias              string `json:"hostAlias,omitempty"`
	User                   string `json:"user,omitempty"`
	LocalPort              int    `json:"localPort,omitempty"`
	WorkspacePath          string `json:"workspacePath,omitempty"`
	PublicKeyPath          string `json:"publicKeyPath,omitempty"`
	WorkspaceSyncEnabled   bool   `json:"workspaceSyncEnabled,omitempty"`
	WorkspaceSyncLocalPath string `json:"workspaceSyncLocalPath,omitempty"`
}

func ResolveListResult(store ListStore, findProjectRoot ProjectFinderFunc, params OpenParams) (ListResult, error) {
	if store == nil {
		return ListResult{}, fmt.Errorf("store is required")
	}

	defaultTenant, _ := loadListDefaultTenant(store)
	defaultEnvironment, _ := loadListDefaultEnvironment(store, defaultTenant)

	tenants, err := store.ListTenantConfigs()
	if err != nil {
		return ListResult{}, err
	}

	currentRepoName, currentRepoPath, err := detectCurrentRepo(findProjectRoot)
	if err != nil {
		return ListResult{}, err
	}

	effectiveResult, effectiveErr := ResolveOpen(store, params)
	configDir, configDirErr := ERunConfigDir()
	if configDirErr != nil {
		return ListResult{}, configDirErr
	}
	result := newListResult(configDir, defaultTenant, defaultEnvironment, currentRepoName, currentRepoPath, tenants)
	result.CurrentDirectory = listCurrentDirectoryResult(result.CurrentDirectory, effectiveResult, effectiveErr)

	portAllocations, err := ResolveAllEnvironmentLocalPorts(store)
	if err != nil {
		return ListResult{}, err
	}
	cloudProviders, err := ListCloudProviderStatuses(store, DefaultCloudDependencies())
	if err != nil {
		return ListResult{}, err
	}
	result.CloudProviders = cloudProviders

	orchestrators, err := loadListOrchestrators(store)
	if err != nil {
		return ListResult{}, err
	}
	result.Orchestrators = orchestrators

	for _, tenant := range tenants {
		tenantResult, err := listTenantResult(store, tenant, defaultTenant, effectiveResult, effectiveErr, portAllocations)
		if err != nil {
			return ListResult{}, err
		}
		result.Tenants = append(result.Tenants, tenantResult)
	}

	result.OrphanedSSHAliases = listOrphanedSSHAliases(result.Tenants)

	return result, nil
}

// listOrphanedSSHAliases reports the host's stale erun ssh aliases against the
// environments just resolved. A config that cannot be read is silence, not a
// failure: the aliases say something about a file the operator may never have
// had, and nothing about the environments this command exists to print.
func listOrphanedSSHAliases(tenants []ListTenantResult) []SSHOrphanedAlias {
	entries, err := ReadDefaultSSHHostEntries()
	if err != nil {
		return nil
	}
	environments := make([]SSHEnvironmentAlias, 0, 4)
	for _, tenant := range tenants {
		for _, env := range tenant.Environments {
			if !env.SSH.Enabled {
				continue
			}
			environments = append(environments, SSHEnvironmentAlias{
				Tenant:      tenant.Name,
				Environment: env.Name,
				Alias:       env.SSH.HostAlias,
				LocalPort:   env.SSH.LocalPort,
			})
		}
	}
	return FindOrphanedSSHAliases(entries, environments)
}

func newListResult(configDir, defaultTenant, defaultEnvironment, currentRepoName, currentRepoPath string, tenants []TenantConfig) ListResult {
	return ListResult{
		ConfigDirectory: strings.TrimSpace(configDir),
		Defaults:        ListDefaultsResult{Tenant: defaultTenant, Environment: defaultEnvironment},
		CurrentDirectory: ListCurrentDirectoryResult{
			Path:             currentRepoPath,
			Repo:             currentRepoName,
			ConfiguredTenant: configuredTenantForRepo(currentRepoName, tenants),
		},
		Tenants: make([]ListTenantResult, 0, len(tenants)),
	}
}

func listCurrentDirectoryResult(current ListCurrentDirectoryResult, effective OpenResult, effectiveErr error) ListCurrentDirectoryResult {
	if effectiveErr != nil {
		current.EffectiveError = effectiveErr.Error()
		return current
	}
	current.Effective = &ListEffectiveTargetResult{
		Tenant:             effective.Tenant,
		Environment:        effective.Environment,
		Type:               effective.EnvConfig.ResolvedType(),
		KubernetesContext:  strings.TrimSpace(effective.EnvConfig.KubernetesContext),
		CloudProviderAlias: strings.TrimSpace(effective.EnvConfig.CloudProviderAlias),
		RepoPath:           effective.RepoPath,
		LocalRepoPath:      strings.TrimSpace(effective.EnvConfig.LocalRepoPath),
		APIURL:             APIURLForListEnvironment(effective.TenantConfig, LocalPortsForResult(effective)),
		LocalPorts:         LocalPortsForResult(effective),
		SSH:                listSSHResult(effective),
	}
	return current
}

func listTenantResult(store ListStore, tenant TenantConfig, defaultTenant string, effective OpenResult, effectiveErr error, portAllocations map[string]EnvironmentLocalPorts) (ListTenantResult, error) {
	envs, err := store.ListEnvConfigs(tenant.Name)
	if err != nil {
		return ListTenantResult{}, err
	}
	result := ListTenantResult{
		Name:                      tenant.Name,
		DefaultEnvironment:        tenant.DefaultEnvironment,
		APIURL:                    strings.TrimSpace(tenant.APIURL),
		CloudProviderAliases:      append([]string(nil), tenant.CloudProviderAliases...),
		PrimaryCloudProviderAlias: strings.TrimSpace(tenant.PrimaryCloudProviderAlias),
		IsDefault:                 tenant.Name == defaultTenant,
		IsEffective:               effectiveErr == nil && tenant.Name == effective.Tenant,
		Environments:              make([]ListEnvironmentResult, 0, len(envs)),
	}
	for _, env := range envs {
		result.Environments = append(result.Environments, listEnvironmentResult(store, tenant, env, effective, effectiveErr, portAllocations))
	}
	return result, nil
}

func listEnvironmentResult(store ListStore, tenant TenantConfig, env EnvConfig, effective OpenResult, effectiveErr error, portAllocations map[string]EnvironmentLocalPorts) ListEnvironmentResult {
	localPorts := listEnvironmentLocalPorts(tenant.Name, env, portAllocations)
	runtimeVersionLine := listRuntimeVersionLine(tenant.Name, env)
	return ListEnvironmentResult{
		Name:                     env.Name,
		Type:                     env.ResolvedType(),
		APIURL:                   APIURLForListEnvironment(tenant, localPorts),
		KubernetesContext:        strings.TrimSpace(env.KubernetesContext),
		CloudProviderAlias:       strings.TrimSpace(env.CloudProviderAlias),
		RepoPath:                 env.EffectiveLocalRepoPath(),
		LocalRepoPath:            strings.TrimSpace(env.LocalRepoPath),
		ContainerRegistries:      EffectiveEnvironmentContainerRegistries(env),
		RuntimeVersion:           strings.TrimSpace(env.RuntimeVersion),
		RuntimeVersionLine:       runtimeVersionLine,
		ErunVersion:              ResolveErunVersion(env, runtimeVersionLine),
		RuntimeImageLineMismatch: listRuntimeImageLineMismatch(env),
		RuntimePod:               env.RuntimePod,
		Sizing:                   EnvironmentRuntimeSizing(tenant.Name, env),
		ManagedCloud:             env.ManagedCloud,
		Hosted:                   listHostedEnvironment(env),
		DisableBuildScript:       env.DisableBuildScript,
		PlatformAccount:          env.PlatformAccount,
		AITool:                   strings.TrimSpace(env.AITool),
		Claude:                   env.Claude,
		Idle:                     env.Idle,
		Deploy:                   env.Deploy,
		IsActive:                 listEnvironmentIsActive(store, env),
		LocalPorts:               localPorts,
		IsDefault:                env.Name == tenant.DefaultEnvironment,
		IsEffective:              effectiveErr == nil && tenant.Name == effective.Tenant && env.Name == effective.Environment,
		SSH:                      listSSHResult(listEnvironmentOpenResult(tenant, env, localPorts)),
		AutoStart:                copyAutoStartPtr(env.AutoStart),
	}
}

// listHostedEnvironment returns the env's hosted marker as a pointer, so an
// environment that carries none reports absent rather than as an all-zero
// marker a reader would have to know to ignore.
func listHostedEnvironment(env EnvConfig) *HostedEnvironment {
	marker, ok := HostedEnvironmentFromConfig(env)
	if !ok {
		return nil
	}
	return &marker
}

// listRuntimeVersionLine wraps ResolveRuntimeVersionLine, but only when there
// is a RuntimeVersion to annotate at all -- an environment that has never
// deployed has no version, and "undetermined" would misread as "deployed,
// but the line is unknown" rather than "never deployed".
func listRuntimeVersionLine(tenant string, env EnvConfig) *RuntimeVersionLine {
	if strings.TrimSpace(env.RuntimeVersion) == "" {
		return nil
	}
	line := ResolveRuntimeVersionLine(tenant, env)
	return &line
}

// listRuntimeImageLineMismatch wraps EnvConfig.RuntimeImageLineMismatch, only
// surfacing a result when it reports a real disagreement.
func listRuntimeImageLineMismatch(env EnvConfig) *RuntimeImageLineMismatchResult {
	recordedLine, observedLine, mismatched := env.RuntimeImageLineMismatch()
	if !mismatched {
		return nil
	}
	return &RuntimeImageLineMismatchResult{RecordedLine: recordedLine, ObservedLine: observedLine}
}

// EnvironmentRuntimeSizing attaches the standing recommendation when there is
// one. A read failure is silence rather than an error: every caller of this
// (`erun list`, `resize`, the `usage` MCP tool) treats sizing as advisory and
// must not fail over it.
func EnvironmentRuntimeSizing(tenant string, env EnvConfig) *RuntimeSizingRecommendation {
	history, err := LoadRuntimeUsageHistory(tenant, env.Name)
	if err != nil {
		return nil
	}
	recommendation, ok := RecommendRuntimeSizing(RuntimeSizingParams{History: history, Ceiling: env.NamespaceQuota})
	if !ok {
		return nil
	}
	return &recommendation
}

func copyAutoStartPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	v := *value
	return &v
}

func APIURLForListEnvironment(tenant TenantConfig, localPorts EnvironmentLocalPorts) string {
	if apiURL := strings.TrimSpace(tenant.APIURL); apiURL != "" {
		return apiURL
	}
	port := localPorts.API
	if port <= 0 {
		port = APIServicePort
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// listEnvironmentLocalPorts resolves the ports this process should report for
// one environment: the config-derived allocation, overridden by the chart's
// injected ERUN_*_PORT values when this process is that environment's own
// runtime pod -- see overlayInjectedRuntimeLocalPorts.
func listEnvironmentLocalPorts(tenant string, env EnvConfig, portAllocations map[string]EnvironmentLocalPorts) EnvironmentLocalPorts {
	localPorts := portAllocations[environmentPortKey(tenant, env.Name)]
	if env.SSHD.LocalPort > 0 {
		localPorts.SSH = env.SSHD.LocalPort
	}
	return overlayInjectedRuntimeLocalPorts(localPorts, os.Getenv, tenant, env.Name)
}

func listEnvironmentOpenResult(tenant TenantConfig, env EnvConfig, localPorts EnvironmentLocalPorts) OpenResult {
	return OpenResult{
		Tenant:      tenant.Name,
		Environment: env.Name,
		TenantConfig: TenantConfig{
			Name: tenant.Name,
		},
		EnvConfig:  env,
		LocalPorts: localPorts,
		RepoPath:   env.EffectiveLocalRepoPath(),
	}
}

func listEnvironmentIsActive(store CloudReadStore, env EnvConfig) bool {
	if !env.RemoteWorktree() {
		return false
	}
	status, ok, err := findCloudContextForKubernetesContext(store, env.KubernetesContext)
	if err != nil || !ok {
		return false
	}
	return strings.TrimSpace(status.Status) == CloudContextStatusRunning
}

func listSSHResult(result OpenResult) ListSSHResult {
	if !result.EnvConfig.SSHD.Enabled {
		return ListSSHResult{}
	}

	info := SSHConnectionInfoForResult(result)
	sync := result.EnvConfig.SSHD.WorkspaceSync
	return ListSSHResult{
		Enabled:                true,
		HostAlias:              info.HostAlias,
		User:                   info.User,
		LocalPort:              info.Port,
		WorkspacePath:          info.WorkspacePath,
		PublicKeyPath:          strings.TrimSpace(result.EnvConfig.SSHD.PublicKeyPath),
		WorkspaceSyncEnabled:   sync.Enabled,
		WorkspaceSyncLocalPath: strings.TrimSpace(sync.LocalPath),
	}
}

func configuredTenantForRepo(repoName string, tenants []TenantConfig) string {
	repoName = strings.TrimSpace(repoName)
	if repoName == "" {
		return ""
	}
	for _, tenant := range tenants {
		if strings.TrimSpace(tenant.Name) == repoName {
			return tenant.Name
		}
	}
	return ""
}

func detectCurrentRepo(findProjectRoot ProjectFinderFunc) (string, string, error) {
	if findProjectRoot == nil {
		findProjectRoot = FindProjectRoot
	}
	name, path, err := findProjectRoot()
	if err == nil {
		return strings.TrimSpace(name), filepath.Clean(path), nil
	}
	if errors.Is(err, ErrNotInGitRepository) {
		return "", "", nil
	}
	if strings.Contains(err.Error(), ErrNotInGitRepository.Error()) {
		return "", "", nil
	}
	return "", "", err
}

func loadListDefaultTenant(store ListStore) (string, error) {
	config, _, err := store.LoadERunConfig()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(config.DefaultTenant), nil
}

func loadListOrchestrators(store ListStore) ([]ListOrchestratorResult, error) {
	config, _, err := store.LoadERunConfig()
	if errors.Is(err, ErrNotInitialized) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	results := make([]ListOrchestratorResult, 0, len(config.Orchestrators))
	for _, orchestrator := range config.Orchestrators {
		envs := make([]ListOrchestratorEnvResult, 0, len(orchestrator.Environments))
		for _, env := range orchestrator.Environments {
			envs = append(envs, ListOrchestratorEnvResult(env))
		}
		directories := make([]string, 0, len(orchestrator.Directories))
		for _, dir := range orchestrator.Directories {
			if path := strings.TrimSpace(dir.Directory); path != "" {
				directories = append(directories, path)
			}
		}
		results = append(results, ListOrchestratorResult{
			ID:           orchestrator.ID,
			Name:         orchestrator.Name,
			Environments: envs,
			Directories:  directories,
			Alias:        strings.TrimSpace(orchestrator.Alias),
		})
	}
	return results, nil
}

func loadListDefaultEnvironment(store ListStore, tenant string) (string, error) {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return "", ErrDefaultEnvironmentNotConfigured
	}
	config, _, err := store.LoadTenantConfig(tenant)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(config.DefaultEnvironment), nil
}
