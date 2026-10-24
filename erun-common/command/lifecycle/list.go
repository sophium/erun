package lifecycle

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common"
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/open"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

type ListResult struct {
	ConfigDirectory  string                      `json:"configDirectory,omitempty"`
	Defaults         ListDefaultsResult          `json:"defaults"`
	CurrentDirectory ListCurrentDirectoryResult  `json:"currentDirectory"`
	CloudProviders   []cloud.CloudProviderStatus `json:"cloudProviders,omitempty"`
	Tenants          []ListTenantResult          `json:"tenants,omitempty"`
	Orchestrators    []ListOrchestratorResult    `json:"orchestrators,omitempty"`
	// OrphanedSSHAliases are the host's own ~/.ssh/config Host blocks naming an
	// erun environment alias no configured environment claims. Nothing else in
	// erun looks at these: `sshd init` writes blocks and env deletion removes
	// the one it wrote, so a block left by a rename, a deleted environment or a
	// turned-off sshd survives indefinitely and can start resolving into
	// whichever environment inherits its local port.
	OrphanedSSHAliases []eruncommon.SSHOrphanedAlias `json:"orphanedSSHAliases,omitempty"`
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
	// UseGateway, Model and Effort are the launch settings as recorded, not as
	// resolved: an empty Model or Effort reads as the default, which is what the
	// operator declared.
	UseGateway bool   `json:"useGateway,omitempty"`
	Model      string `json:"model,omitempty"`
	Effort     string `json:"effort,omitempty"`
	// Directories are the orchestrator's own: paths it works in that belong to
	// no environment at all. Reported so an orchestrator pointed only at
	// directories does not read as having no scope.
	Directories []string `json:"directories,omitempty"`
}

// ListOrchestratorEnvResult is the read-model view of one
// OrchestratorEnvConfig link, including its (possibly undeclared) Role.
type ListOrchestratorEnvResult struct {
	Tenant      string                        `json:"tenant"`
	Environment string                        `json:"environment"`
	Directory   string                        `json:"directory,omitempty"`
	Role        configpkg.OrchestratorEnvRole `json:"role,omitempty"`
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
	Tenant             string                          `json:"tenant"`
	Environment        string                          `json:"environment"`
	Type               configpkg.EnvironmentType       `json:"type,omitempty"`
	KubernetesContext  string                          `json:"kubernetesContext"`
	CloudProviderAlias string                          `json:"cloudProviderAlias,omitempty"`
	RepoPath           string                          `json:"repoPath"`
	LocalRepoPath      string                          `json:"localRepoPath,omitempty"`
	APIURL             string                          `json:"apiUrl,omitempty"`
	LocalPorts         configpkg.EnvironmentLocalPorts `json:"localPorts,omitempty"`
	SSH                eruncommon.ListSSHResult        `json:"ssh,omitempty"`
}

type ListTenantResult struct {
	Name                      string                             `json:"name"`
	DefaultEnvironment        string                             `json:"defaultEnvironment,omitempty"`
	APIURL                    string                             `json:"apiUrl,omitempty"`
	CloudProviderAliases      []string                           `json:"cloudProviderAliases,omitempty"`
	PrimaryCloudProviderAlias string                             `json:"primaryCloudProviderAlias,omitempty"`
	IsDefault                 bool                               `json:"isDefault,omitempty"`
	IsEffective               bool                               `json:"isEffective,omitempty"`
	Environments              []eruncommon.ListEnvironmentResult `json:"environments,omitempty"`
}

func ResolveListResult(store eruncommon.ListStore, findProjectRoot shared.ProjectFinderFunc, params shared.OpenParams) (ListResult, error) {
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

	effectiveResult, effectiveErr := open.ResolveOpen(store, params)
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
	cloudProviders, err := cloud.ListCloudProviderStatuses(store, cloud.DefaultCloudDependencies())
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
func listOrphanedSSHAliases(tenants []ListTenantResult) []eruncommon.SSHOrphanedAlias {
	entries, err := configpkg.ReadDefaultSSHHostEntries()
	if err != nil {
		return nil
	}
	environments := make([]eruncommon.SSHEnvironmentAlias, 0, 4)
	for _, tenant := range tenants {
		for _, env := range tenant.Environments {
			if !env.SSH.Enabled {
				continue
			}
			environments = append(environments, eruncommon.SSHEnvironmentAlias{
				Tenant:      tenant.Name,
				Environment: env.Name,
				Alias:       env.SSH.HostAlias,
				LocalPort:   env.SSH.LocalPort,
			})
		}
	}
	return eruncommon.FindOrphanedSSHAliases(entries, environments)
}

func newListResult(configDir, defaultTenant, defaultEnvironment, currentRepoName, currentRepoPath string, tenants []configpkg.TenantConfig) ListResult {
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
		APIURL:             eruncommon.APIURLForListEnvironment(effective.TenantConfig, configpkg.LocalPortsForResult(effective)),
		LocalPorts:         configpkg.LocalPortsForResult(effective),
		SSH:                eruncommon.NewListSSHResult(effective),
	}
	return current
}

func listTenantResult(store eruncommon.ListStore, tenant configpkg.TenantConfig, defaultTenant string, effective configpkg.OpenResult, effectiveErr error, portAllocations map[string]configpkg.EnvironmentLocalPorts) (ListTenantResult, error) {
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
		Environments:              make([]eruncommon.ListEnvironmentResult, 0, len(envs)),
	}
	for _, env := range envs {
		result.Environments = append(result.Environments, eruncommon.NewListEnvironmentResult(store, tenant, env, effective, effectiveErr, portAllocations))
	}
	return result, nil
}

func configuredTenantForRepo(repoName string, tenants []configpkg.TenantConfig) string {
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

func detectCurrentRepo(findProjectRoot shared.ProjectFinderFunc) (string, string, error) {
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

func loadListDefaultTenant(store eruncommon.ListStore) (string, error) {
	config, _, err := store.LoadERunConfig()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(config.DefaultTenant), nil
}

func loadListOrchestrators(store eruncommon.ListStore) ([]ListOrchestratorResult, error) {
	config, _, err := store.LoadERunConfig()
	if errors.Is(err, configpkg.ErrNotInitialized) {
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
			UseGateway:   orchestrator.UseGateway,
			Model:        strings.TrimSpace(orchestrator.Model),
			Effort:       strings.TrimSpace(orchestrator.Effort),
		})
	}
	return results, nil
}

func loadListDefaultEnvironment(store eruncommon.ListStore, tenant string) (string, error) {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return "", shared.ErrDefaultEnvironmentNotConfigured
	}
	config, _, err := store.LoadTenantConfig(tenant)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(config.DefaultEnvironment), nil
}
