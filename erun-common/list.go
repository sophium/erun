package eruncommon

import (
	"errors"
	"fmt"
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
	KubernetesContext  string                `json:"kubernetesContext"`
	CloudProviderAlias string                `json:"cloudProviderAlias,omitempty"`
	RepoPath           string                `json:"repoPath"`
	APIURL             string                `json:"apiUrl,omitempty"`
	Snapshot           bool                  `json:"snapshot"`
	LocalPorts         EnvironmentLocalPorts `json:"localPorts,omitempty"`
	SSH                ListSSHResult         `json:"ssh,omitempty"`
}

type ListTenantResult struct {
	Name               string                  `json:"name"`
	DefaultEnvironment string                  `json:"defaultEnvironment,omitempty"`
	APIURL             string                  `json:"apiUrl,omitempty"`
	IsDefault          bool                    `json:"isDefault,omitempty"`
	IsEffective        bool                    `json:"isEffective,omitempty"`
	Environments       []ListEnvironmentResult `json:"environments,omitempty"`
}

type ListEnvironmentResult struct {
	Name               string                `json:"name"`
	APIURL             string                `json:"apiUrl,omitempty"`
	KubernetesContext  string                `json:"kubernetesContext,omitempty"`
	CloudProviderAlias string                `json:"cloudProviderAlias,omitempty"`
	RepoPath           string                `json:"repoPath,omitempty"`
	RuntimeVersion     string                `json:"runtimeVersion,omitempty"`
	Remote             bool                  `json:"remote,omitempty"`
	Snapshot           bool                  `json:"snapshot"`
	IsActive           bool                  `json:"isActive,omitempty"`
	LocalPorts         EnvironmentLocalPorts `json:"localPorts,omitempty"`
	IsDefault          bool                  `json:"isDefault,omitempty"`
	IsEffective        bool                  `json:"isEffective,omitempty"`
	SSH                ListSSHResult         `json:"ssh,omitempty"`
}

type ListSSHResult struct {
	Enabled       bool   `json:"enabled,omitempty"`
	HostAlias     string `json:"hostAlias,omitempty"`
	User          string `json:"user,omitempty"`
	LocalPort     int    `json:"localPort,omitempty"`
	WorkspacePath string `json:"workspacePath,omitempty"`
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
	cloudProviders, err := ListCloudProviderStatuses(store, CloudDependencies{})
	if err != nil {
		return ListResult{}, err
	}
	result.CloudProviders = cloudProviders

	for _, tenant := range tenants {
		tenantResult, err := listTenantResult(store, tenant, defaultTenant, effectiveResult, effectiveErr, portAllocations)
		if err != nil {
			return ListResult{}, err
		}
		result.Tenants = append(result.Tenants, tenantResult)
	}

	return result, nil
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
		KubernetesContext:  strings.TrimSpace(effective.EnvConfig.KubernetesContext),
		CloudProviderAlias: strings.TrimSpace(effective.EnvConfig.CloudProviderAlias),
		RepoPath:           effective.RepoPath,
		APIURL:             APIURLForListEnvironment(effective.TenantConfig, LocalPortsForResult(effective)),
		Snapshot:           deployTargetSnapshotEnabled(effective, nil),
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
		Name:               tenant.Name,
		DefaultEnvironment: tenant.DefaultEnvironment,
		APIURL:             strings.TrimSpace(tenant.APIURL),
		IsDefault:          tenant.Name == defaultTenant,
		IsEffective:        effectiveErr == nil && tenant.Name == effective.Tenant,
		Environments:       make([]ListEnvironmentResult, 0, len(envs)),
	}
	for _, env := range envs {
		result.Environments = append(result.Environments, listEnvironmentResult(store, tenant, env, effective, effectiveErr, portAllocations))
	}
	return result, nil
}

func listEnvironmentResult(store ListStore, tenant TenantConfig, env EnvConfig, effective OpenResult, effectiveErr error, portAllocations map[string]EnvironmentLocalPorts) ListEnvironmentResult {
	localPorts := listEnvironmentLocalPorts(tenant.Name, env, portAllocations)
	return ListEnvironmentResult{
		Name:               env.Name,
		APIURL:             APIURLForListEnvironment(tenant, localPorts),
		KubernetesContext:  strings.TrimSpace(env.KubernetesContext),
		CloudProviderAlias: strings.TrimSpace(env.CloudProviderAlias),
		RepoPath:           strings.TrimSpace(env.RepoPath),
		RuntimeVersion:     strings.TrimSpace(env.RuntimeVersion),
		Remote:             env.Remote,
		Snapshot:           env.SnapshotEnabled(),
		IsActive:           listEnvironmentIsActive(store, env),
		LocalPorts:         localPorts,
		IsDefault:          env.Name == tenant.DefaultEnvironment,
		IsEffective:        effectiveErr == nil && tenant.Name == effective.Tenant && env.Name == effective.Environment,
		SSH:                listSSHResult(listEnvironmentOpenResult(tenant, env, localPorts)),
	}
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

func listEnvironmentLocalPorts(tenant string, env EnvConfig, portAllocations map[string]EnvironmentLocalPorts) EnvironmentLocalPorts {
	localPorts := portAllocations[environmentPortKey(tenant, env.Name)]
	if localPorts.RangeStart != 0 {
		return localPorts
	}
	localPorts = DefaultEnvironmentLocalPorts()
	if env.SSHD.LocalPort > 0 {
		localPorts.SSH = env.SSHD.LocalPort
	}
	return localPorts
}

func listEnvironmentOpenResult(tenant TenantConfig, env EnvConfig, localPorts EnvironmentLocalPorts) OpenResult {
	return OpenResult{
		Tenant:      tenant.Name,
		Environment: env.Name,
		TenantConfig: TenantConfig{
			Name:        tenant.Name,
			ProjectRoot: tenant.ProjectRoot,
		},
		EnvConfig:  env,
		LocalPorts: localPorts,
		RepoPath:   env.RepoPath,
	}
}

func listEnvironmentIsActive(store CloudReadStore, env EnvConfig) bool {
	if !env.Remote {
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
	return ListSSHResult{
		Enabled:       true,
		HostAlias:     info.HostAlias,
		User:          info.User,
		LocalPort:     info.Port,
		WorkspacePath: info.WorkspacePath,
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
