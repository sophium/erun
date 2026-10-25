package list

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/envstore"
	"github.com/sophium/erun/erun-common/base/seam/projectroot"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/cloudcontext"
	"github.com/sophium/erun/erun-common/infra/cloud/providertoken"
	"github.com/sophium/erun/erun-common/primitive/deploy/runtimeusage"
	"github.com/sophium/erun/erun-common/primitive/deploy/runtimeversion"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-common/primitive/open/sshconn"
	"github.com/sophium/erun/erun-common/primitive/shared/opentarget"
)

type ListResult struct {
	ConfigDirectory  string                         `json:"configDirectory,omitempty"`
	Defaults         model.ListDefaultsResult       `json:"defaults"`
	CurrentDirectory ListCurrentDirectoryResult     `json:"currentDirectory"`
	CloudProviders   []model.CloudProviderStatus    `json:"cloudProviders,omitempty"`
	Tenants          []ListTenantResult             `json:"tenants,omitempty"`
	Orchestrators    []model.ListOrchestratorResult `json:"orchestrators,omitempty"`
	// OrphanedSSHAliases are ~/.ssh/config erun Host blocks no configured environment claims;
	// they can resolve into whichever environment inherits their local port.
	OrphanedSSHAliases []model.SSHOrphanedAlias `json:"orphanedSSHAliases,omitempty"`
}

type ListCurrentDirectoryResult struct {
	Path             string                     `json:"path,omitempty"`
	Repo             string                     `json:"repo,omitempty"`
	ConfiguredTenant string                     `json:"configuredTenant,omitempty"`
	Effective        *ListEffectiveTargetResult `json:"effective,omitempty"`
	EffectiveError   string                     `json:"effectiveError,omitempty"`
}

type ListEffectiveTargetResult struct {
	Tenant             string                      `json:"tenant"`
	Environment        string                      `json:"environment"`
	Type               configpkg.EnvironmentType   `json:"type,omitempty"`
	KubernetesContext  string                      `json:"kubernetesContext"`
	CloudProviderAlias string                      `json:"cloudProviderAlias,omitempty"`
	RepoPath           string                      `json:"repoPath"`
	LocalRepoPath      string                      `json:"localRepoPath,omitempty"`
	APIURL             string                      `json:"apiUrl,omitempty"`
	LocalPorts         model.EnvironmentLocalPorts `json:"localPorts,omitempty"`
	SSH                model.ListSSHResult         `json:"ssh,omitempty"`
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

func ResolveListResult(store envstore.ListStore, findProjectRoot projectroot.ProjectFinderFunc, params model.OpenParams) (ListResult, error) {
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

	effectiveResult, effectiveErr := resolve.ResolveOpen(store, params)
	configDir, configDirErr := configpkg.ERunConfigDir()
	if configDirErr != nil {
		return ListResult{}, configDirErr
	}
	result := newListResult(configDir, defaultTenant, defaultEnvironment, currentRepoName, currentRepoPath, tenants)
	result.CurrentDirectory = listCurrentDirectoryResult(result.CurrentDirectory, effectiveResult, effectiveErr)

	portAllocations, err := configpkg.ResolveAllEnvironmentLocalPorts(store)
	if err != nil {
		return ListResult{}, err
	}
	cloudProviders, err := providertoken.ListCloudProviderStatuses(store, cloud.DefaultCloudDependencies())
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

// An unreadable ssh config is not a failure of this command.
func listOrphanedSSHAliases(tenants []ListTenantResult) []model.SSHOrphanedAlias {
	entries, err := configpkg.ReadDefaultSSHHostEntries()
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

func newListResult(configDir, defaultTenant, defaultEnvironment, currentRepoName, currentRepoPath string, tenants []model.TenantConfig) ListResult {
	return ListResult{
		ConfigDirectory: strings.TrimSpace(configDir),
		Defaults:        model.ListDefaultsResult{Tenant: defaultTenant, Environment: defaultEnvironment},
		CurrentDirectory: ListCurrentDirectoryResult{
			Path:             currentRepoPath,
			Repo:             currentRepoName,
			ConfiguredTenant: configuredTenantForRepo(currentRepoName, tenants),
		},
		Tenants: make([]ListTenantResult, 0, len(tenants)),
	}
}

func listCurrentDirectoryResult(current ListCurrentDirectoryResult, effective configpkg.OpenResult, effectiveErr error) ListCurrentDirectoryResult {
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
		APIURL:             APIURLForListEnvironment(effective.TenantConfig, configpkg.LocalPortsForResult(effective)),
		LocalPorts:         configpkg.LocalPortsForResult(effective),
		SSH:                NewListSSHResult(effective),
	}
	return current
}

func listTenantResult(store envstore.ListStore, tenant model.TenantConfig, defaultTenant string, effective configpkg.OpenResult, effectiveErr error, portAllocations map[string]model.EnvironmentLocalPorts) (ListTenantResult, error) {
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
		result.Environments = append(result.Environments, NewListEnvironmentResult(store, tenant, env, effective, effectiveErr, portAllocations))
	}
	return result, nil
}

func configuredTenantForRepo(repoName string, tenants []model.TenantConfig) string {
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

func detectCurrentRepo(findProjectRoot projectroot.ProjectFinderFunc) (string, string, error) {
	if findProjectRoot == nil {
		findProjectRoot = configpkg.FindProjectRoot
	}
	name, path, err := findProjectRoot()
	if err == nil {
		return strings.TrimSpace(name), filepath.Clean(path), nil
	}
	if errors.Is(err, configpkg.ErrNotInGitRepository) {
		return "", "", nil
	}
	if strings.Contains(err.Error(), configpkg.ErrNotInGitRepository.Error()) {
		return "", "", nil
	}
	return "", "", err
}

func loadListDefaultTenant(store envstore.ListStore) (string, error) {
	config, _, err := store.LoadERunConfig()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(config.DefaultTenant), nil
}

func loadListOrchestrators(store envstore.ListStore) ([]model.ListOrchestratorResult, error) {
	config, _, err := store.LoadERunConfig()
	if errors.Is(err, configpkg.ErrNotInitialized) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	results := make([]model.ListOrchestratorResult, 0, len(config.Orchestrators))
	for _, orchestrator := range config.Orchestrators {
		envs := make([]model.ListOrchestratorEnvResult, 0, len(orchestrator.Environments))
		for _, env := range orchestrator.Environments {
			envs = append(envs, model.ListOrchestratorEnvResult(env))
		}
		directories := make([]string, 0, len(orchestrator.Directories))
		for _, dir := range orchestrator.Directories {
			if path := strings.TrimSpace(dir.Directory); path != "" {
				directories = append(directories, path)
			}
		}
		results = append(results, model.ListOrchestratorResult{
			ID:           orchestrator.ID,
			Name:         orchestrator.Name,
			Environments: envs,
			Directories:  directories,
			Alias:        strings.TrimSpace(orchestrator.Alias),
			UseGateway:   orchestrator.UseGateway,
			Model:        strings.TrimSpace(orchestrator.Model),
			Effort:       strings.TrimSpace(orchestrator.Effort),
		})
	}
	return results, nil
}

func loadListDefaultEnvironment(store envstore.ListStore, tenant string) (string, error) {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return "", opentarget.ErrDefaultEnvironmentNotConfigured
	}
	config, _, err := store.LoadTenantConfig(tenant)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(config.DefaultEnvironment), nil
}

type ListEnvironmentResult struct {
	Name                string                        `json:"name"`
	Type                configpkg.EnvironmentType     `json:"type,omitempty"`
	APIURL              string                        `json:"apiUrl,omitempty"`
	KubernetesContext   string                        `json:"kubernetesContext,omitempty"`
	CloudProviderAlias  string                        `json:"cloudProviderAlias,omitempty"`
	RepoPath            string                        `json:"repoPath,omitempty"`
	LocalRepoPath       string                        `json:"localRepoPath,omitempty"`
	ContainerRegistries configpkg.ContainerRegistries `json:"containerRegistries,omitempty"`
	RuntimeVersion      string                        `json:"runtimeVersion,omitempty"`
	// RuntimeVersionLine is nil when RuntimeVersion is empty.
	RuntimeVersionLine *model.RuntimeVersionLine `json:"runtimeVersionLine,omitempty"`
	// ErunVersion is nil when it cannot be read from config alone.
	ErunVersion *model.ErunVersion `json:"erunVersion,omitempty"`
	// RuntimeImageLineMismatch is set only when recorded and observed images name different lines.
	RuntimeImageLineMismatch *model.RuntimeImageLineMismatchResult `json:"runtimeImageLineMismatch,omitempty"`
	RuntimePod               model.RuntimePodResources             `json:"runtimePod,omitempty"`
	// Sizing is nil unless this host is the environment's own runtime container, which writes the history.
	Sizing       *model.RuntimeSizingRecommendation `json:"sizing,omitempty"`
	ManagedCloud bool                               `json:"managedCloud,omitempty"`
	// Hosted is nil unless the environment is marked as hosted.
	Hosted             *configpkg.HostedEnvironment      `json:"hosted,omitempty"`
	DisableBuildScript bool                              `json:"disableBuildScript,omitempty"`
	PlatformAccount    bool                              `json:"platformAccount,omitempty"`
	AITool             string                            `json:"aiTool,omitempty"`
	Claude             configpkg.EnvironmentClaudeConfig `json:"claude,omitempty"`
	Idle               configpkg.EnvironmentIdleConfig   `json:"idle,omitempty"`
	Deploy             configpkg.EnvironmentDeployConfig `json:"deploy,omitempty"`
	IsActive           bool                              `json:"isActive,omitempty"`
	LocalPorts         model.EnvironmentLocalPorts       `json:"localPorts,omitempty"`
	IsDefault          bool                              `json:"isDefault,omitempty"`
	IsEffective        bool                              `json:"isEffective,omitempty"`
	SSH                model.ListSSHResult               `json:"ssh,omitempty"`
	AutoStart          *bool                             `json:"autoStart,omitempty"`
}

func NewListEnvironmentResult(store envstore.ListStore, tenant model.TenantConfig, env configpkg.EnvConfig, effective configpkg.OpenResult, effectiveErr error, portAllocations map[string]model.EnvironmentLocalPorts) ListEnvironmentResult {
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
		ContainerRegistries:      configpkg.EffectiveEnvironmentContainerRegistries(env),
		RuntimeVersion:           strings.TrimSpace(env.RuntimeVersion),
		RuntimeVersionLine:       runtimeVersionLine,
		ErunVersion:              runtimeversion.ResolveErunVersion(env, runtimeVersionLine),
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
		SSH:                      NewListSSHResult(listEnvironmentOpenResult(tenant, env, localPorts)),
		AutoStart:                copyAutoStartPtr(env.AutoStart),
	}
}

func listHostedEnvironment(env configpkg.EnvConfig) *configpkg.HostedEnvironment {
	marker, ok := configpkg.HostedEnvironmentFromConfig(env)
	if !ok {
		return nil
	}
	return &marker
}

// A never-deployed environment has no line, not an "undetermined" one.
func listRuntimeVersionLine(tenant string, env configpkg.EnvConfig) *model.RuntimeVersionLine {
	if strings.TrimSpace(env.RuntimeVersion) == "" {
		return nil
	}
	line := runtimeversion.ResolveRuntimeVersionLine(tenant, env)
	return &line
}

func listRuntimeImageLineMismatch(env configpkg.EnvConfig) *model.RuntimeImageLineMismatchResult {
	recordedLine, observedLine, mismatched := env.RuntimeImageLineMismatch()
	if !mismatched {
		return nil
	}
	return &model.RuntimeImageLineMismatchResult{RecordedLine: recordedLine, ObservedLine: observedLine}
}

// EnvironmentRuntimeSizing returns the standing recommendation; sizing is advisory, so read failures are nil.
func EnvironmentRuntimeSizing(tenant string, env configpkg.EnvConfig) *model.RuntimeSizingRecommendation {
	history, err := runtimeusage.LoadRuntimeUsageHistory(tenant, env.Name)
	if err != nil {
		return nil
	}
	recommendation, ok := runtimeusage.RecommendRuntimeSizing(runtimeusage.RuntimeSizingParams{History: history, Ceiling: env.NamespaceQuota})
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

func APIURLForListEnvironment(tenant model.TenantConfig, localPorts model.EnvironmentLocalPorts) string {
	if apiURL := strings.TrimSpace(tenant.APIURL); apiURL != "" {
		return apiURL
	}
	port := localPorts.API
	if port <= 0 {
		port = configpkg.APIServicePort
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// Injected ERUN_*_PORT values override config when this process is the environment's own runtime pod.
func listEnvironmentLocalPorts(tenant string, env configpkg.EnvConfig, portAllocations map[string]model.EnvironmentLocalPorts) model.EnvironmentLocalPorts {
	localPorts := portAllocations[configpkg.EnvironmentPortKey(tenant, env.Name)]
	if env.SSHD.LocalPort > 0 {
		localPorts.SSH = env.SSHD.LocalPort
	}
	return configpkg.OverlayInjectedRuntimeLocalPorts(localPorts, os.Getenv, tenant, env.Name)
}

func listEnvironmentOpenResult(tenant model.TenantConfig, env configpkg.EnvConfig, localPorts model.EnvironmentLocalPorts) configpkg.OpenResult {
	return configpkg.OpenResult{
		Tenant:      tenant.Name,
		Environment: env.Name,
		TenantConfig: model.TenantConfig{
			Name: tenant.Name,
		},
		EnvConfig:  env,
		LocalPorts: localPorts,
		RepoPath:   env.EffectiveLocalRepoPath(),
	}
}

func listEnvironmentIsActive(store configpkg.CloudReadStore, env configpkg.EnvConfig) bool {
	if !env.RemoteWorktree() {
		return false
	}
	status, ok, err := configpkg.FindCloudContextForKubernetesContext(store, env.KubernetesContext)
	if err != nil || !ok {
		return false
	}
	return strings.TrimSpace(status.Status) == cloudcontext.CloudContextStatusRunning
}

func NewListSSHResult(result configpkg.OpenResult) model.ListSSHResult {
	if !result.EnvConfig.SSHD.Enabled {
		return model.ListSSHResult{}
	}

	info := sshconn.SSHConnectionInfoForResult(result)
	sync := result.EnvConfig.SSHD.WorkspaceSync
	return model.ListSSHResult{
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
