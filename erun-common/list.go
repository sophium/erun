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
	Tenant            string                `json:"tenant"`
	Environment       string                `json:"environment"`
	KubernetesContext string                `json:"kubernetesContext"`
	RepoPath          string                `json:"repoPath"`
	Snapshot          bool                  `json:"snapshot"`
	LocalPorts        EnvironmentLocalPorts `json:"localPorts,omitempty"`
	SSH               ListSSHResult         `json:"ssh,omitempty"`
}

type ListTenantResult struct {
	Name               string                  `json:"name"`
	DefaultEnvironment string                  `json:"defaultEnvironment,omitempty"`
	IsDefault          bool                    `json:"isDefault,omitempty"`
	IsEffective        bool                    `json:"isEffective,omitempty"`
	Environments       []ListEnvironmentResult `json:"environments,omitempty"`
}

type ListEnvironmentResult struct {
	Name              string                `json:"name"`
	KubernetesContext string                `json:"kubernetesContext,omitempty"`
	RepoPath          string                `json:"repoPath,omitempty"`
	Snapshot          bool                  `json:"snapshot"`
	LocalPorts        EnvironmentLocalPorts `json:"localPorts,omitempty"`
	IsDefault         bool                  `json:"isDefault,omitempty"`
	IsEffective       bool                  `json:"isEffective,omitempty"`
	SSH               ListSSHResult         `json:"ssh,omitempty"`
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
	result := ListResult{
		ConfigDirectory: strings.TrimSpace(configDir),
		Defaults: ListDefaultsResult{
			Tenant:      defaultTenant,
			Environment: defaultEnvironment,
		},
		CurrentDirectory: ListCurrentDirectoryResult{
			Path:             currentRepoPath,
			Repo:             currentRepoName,
			ConfiguredTenant: configuredTenantForRepo(currentRepoName, tenants),
		},
		Tenants: make([]ListTenantResult, 0, len(tenants)),
	}
	if configDirErr != nil {
		return ListResult{}, configDirErr
	}

	if effectiveErr != nil {
		result.CurrentDirectory.EffectiveError = effectiveErr.Error()
	} else {
		result.CurrentDirectory.Effective = &ListEffectiveTargetResult{
			Tenant:            effectiveResult.Tenant,
			Environment:       effectiveResult.Environment,
			KubernetesContext: strings.TrimSpace(effectiveResult.EnvConfig.KubernetesContext),
			RepoPath:          effectiveResult.RepoPath,
			Snapshot:          deployTargetSnapshotEnabled(effectiveResult, nil),
			LocalPorts:        LocalPortsForResult(effectiveResult),
			SSH:               listSSHResult(effectiveResult),
		}
	}

	portAllocations, err := ResolveAllEnvironmentLocalPorts(store)
	if err != nil {
		return ListResult{}, err
	}

	for _, tenant := range tenants {
		envs, err := store.ListEnvConfigs(tenant.Name)
		if err != nil {
			return ListResult{}, err
		}

		tenantResult := ListTenantResult{
			Name:               tenant.Name,
			DefaultEnvironment: tenant.DefaultEnvironment,
			IsDefault:          tenant.Name == defaultTenant,
			IsEffective:        effectiveErr == nil && tenant.Name == effectiveResult.Tenant,
			Environments:       make([]ListEnvironmentResult, 0, len(envs)),
		}
		for _, env := range envs {
			localPorts := portAllocations[environmentPortKey(tenant.Name, env.Name)]
			if localPorts.RangeStart == 0 {
				localPorts = DefaultEnvironmentLocalPorts()
				if env.SSHD.LocalPort > 0 {
					localPorts.SSH = env.SSHD.LocalPort
				}
			}
			tenantResult.Environments = append(tenantResult.Environments, ListEnvironmentResult{
				Name:              env.Name,
				KubernetesContext: strings.TrimSpace(env.KubernetesContext),
				RepoPath:          strings.TrimSpace(env.RepoPath),
				Snapshot:          env.SnapshotEnabled(),
				LocalPorts:        localPorts,
				IsDefault:         env.Name == tenant.DefaultEnvironment,
				IsEffective:       effectiveErr == nil && tenant.Name == effectiveResult.Tenant && env.Name == effectiveResult.Environment,
				SSH: listSSHResult(OpenResult{
					Tenant:      tenant.Name,
					Environment: env.Name,
					TenantConfig: TenantConfig{
						Name:        tenant.Name,
						ProjectRoot: tenant.ProjectRoot,
						Remote:      tenant.Remote,
					},
					EnvConfig:  env,
					LocalPorts: localPorts,
					RepoPath:   env.RepoPath,
				}),
			})
		}
		result.Tenants = append(result.Tenants, tenantResult)
	}

	return result, nil
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
