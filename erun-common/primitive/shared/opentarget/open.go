package opentarget

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
)

var (
	ErrDefaultTenantNotConfigured      = errors.New("default tenant is not configured")
	ErrDefaultEnvironmentNotConfigured = errors.New("default environment is not configured")
	ErrTenantNotFound                  = errors.New("no such tenant exists")
	ErrKubernetesContextNotConfigured  = errors.New("kubernetes context is not configured")
	ErrRepoPathNotConfigured           = errors.New("repo path is not configured")
	// ErrOpenTenantNotProvided: inference was not permitted, unlike ErrDefaultTenantNotConfigured.
	ErrOpenTenantNotProvided = errors.New("no tenant given for open")
	// ErrOpenEnvironmentNotProvided is the environment counterpart of ErrOpenTenantNotProvided.
	ErrOpenEnvironmentNotProvided = errors.New("no environment given for open")
)

type effectiveKubernetesContextResolver interface {
	ResolveEffectiveKubernetesContext(environment, configured string) string
}

func LoadOpenDefaultTenant(store configpkg.OpenStore) (string, error) {
	toolConfig, _, err := store.LoadERunConfig()
	if errors.Is(err, configpkg.ErrNotInitialized) {
		return "", ErrDefaultTenantNotConfigured
	}
	if err != nil {
		return "", err
	}
	if toolConfig.DefaultTenant == "" {
		return "", ErrDefaultTenantNotConfigured
	}
	return toolConfig.DefaultTenant, nil
}

func ResolveOpenWithFinder(store configpkg.OpenStore, findProjectRoot seam.ProjectFinderFunc, params model.OpenParams) (configpkg.OpenResult, error) {
	if store == nil {
		return configpkg.OpenResult{}, fmt.Errorf("store is required")
	}

	tenant, err := ResolveOpenTenant(store, findProjectRoot, params)
	if err != nil {
		return configpkg.OpenResult{}, err
	}
	tenantConfig, err := loadOpenTenantConfig(store, tenant)
	if err != nil {
		return configpkg.OpenResult{}, err
	}
	environment, err := resolveOpenEnvironment(params, tenantConfig)
	if err != nil {
		return configpkg.OpenResult{}, err
	}
	envConfig, err := loadOpenEnvConfig(store, tenant, environment)
	if err != nil {
		return configpkg.OpenResult{}, err
	}
	repoPath, err := resolveOpenRepoPath(envConfig)
	if err != nil {
		return configpkg.OpenResult{}, err
	}
	if err := validateOpenTarget(tenant, environment, repoPath, envConfig); err != nil {
		return configpkg.OpenResult{}, err
	}
	localPorts, err := configpkg.EnvironmentLocalPortsForTarget(store, tenant, envConfig)
	if err != nil {
		return configpkg.OpenResult{}, err
	}

	return configpkg.OpenResult{
		Tenant:       tenant,
		Environment:  environment,
		TenantConfig: tenantConfig,
		EnvConfig:    envConfig,
		LocalPorts:   localPorts,
		RepoPath:     repoPath,
		Title:        tenant + "-" + environment,
		Gateway:      ResolveOpenGateway(store),
	}, nil
}

// ResolveOpenGateway reads the erun-level gateway catalog; an unreadable root config leaves it unset.
func ResolveOpenGateway(store configpkg.OpenStore) *configpkg.OpenRouterConfig {
	gateway, err := configpkg.ResolveOpenRouterConfig(store)
	if err != nil {
		return nil
	}
	return gateway
}

// tenantResolutionRemedy names the command that actually failed and its own flag, or no
// command when the caller did not name itself.
func tenantResolutionRemedy(command string, scopesTenantByFlag bool) string {
	const (
		cause    = "one could not be inferred from the working directory either"
		setState = "or run `erun init --tenant <name> --set-default-tenant` to set a default"
	)
	if command == "" {
		return fmt.Sprintf("%s — pass a tenant explicitly, %s", cause, setState)
	}
	if !scopesTenantByFlag {
		return fmt.Sprintf("%s — pass a tenant explicitly to `%s`, %s", cause, command, setState)
	}
	return fmt.Sprintf("%s — pass a tenant explicitly with `%s --tenant <name>`, %s", cause, command, setState)
}

func ResolveOpenTenant(store configpkg.OpenStore, findProjectRoot seam.ProjectFinderFunc, params model.OpenParams) (string, error) {
	tenant := params.Tenant
	if tenant == "" && params.UseDefaultTenant {
		currentTenant, ok, err := loadCurrentDirectoryTenant(store, findProjectRoot)
		if err != nil {
			return "", err
		}
		if ok {
			tenant = currentTenant
		}
	}
	if tenant == "" && params.UseDefaultTenant {
		resolved, err := LoadOpenDefaultTenant(store)
		if err != nil {
			if errors.Is(err, ErrDefaultTenantNotConfigured) {
				return "", fmt.Errorf("%w, and %s", err, tenantResolutionRemedy(params.Command, params.CommandScopesTenantByFlag))
			}
			return "", err
		}
		return resolved, nil
	}
	if tenant == "" {
		return "", fmt.Errorf("%w: this call path does not fall back to the working directory or a configured default tenant — pass a tenant explicitly", ErrOpenTenantNotProvided)
	}
	return tenant, nil
}

func loadOpenTenantConfig(store configpkg.OpenStore, tenant string) (model.TenantConfig, error) {
	tenantConfig, _, err := store.LoadTenantConfig(tenant)
	if errors.Is(err, configpkg.ErrNotInitialized) {
		return model.TenantConfig{}, fmt.Errorf("%w: %s", ErrTenantNotFound, tenant)
	}
	if err != nil {
		return model.TenantConfig{}, err
	}
	if tenantConfig.Name == "" {
		tenantConfig.Name = tenant
	}
	return tenantConfig, nil
}

func resolveOpenEnvironment(params model.OpenParams, tenantConfig model.TenantConfig) (string, error) {
	environment := params.Environment
	if environment == "" && params.UseDefaultEnvironment {
		environment = tenantConfig.DefaultEnvironment
		if environment == "" {
			return "", fmt.Errorf("%w for %s, and open has no environment to fall back to — pass an environment explicitly, or run `erun init --tenant %s --environment <name>` to set one", ErrDefaultEnvironmentNotConfigured, tenantConfig.Name, tenantConfig.Name)
		}
	}
	if environment == "" {
		return "", fmt.Errorf("%w: this call path does not fall back to %s's default environment — pass an environment explicitly", ErrOpenEnvironmentNotProvided, tenantConfig.Name)
	}
	return environment, nil
}

func loadOpenEnvConfig(store configpkg.OpenStore, tenant, environment string) (configpkg.EnvConfig, error) {
	envConfig, _, err := store.LoadEnvConfig(tenant, environment)
	if errors.Is(err, configpkg.ErrNotInitialized) {
		return configpkg.EnvConfig{}, fmt.Errorf("%w: %s", configpkg.ErrEnvironmentNotFound, environment)
	}
	if err != nil {
		return configpkg.EnvConfig{}, err
	}
	if envConfig.Name == "" {
		envConfig.Name = environment
	}
	if resolver, ok := store.(effectiveKubernetesContextResolver); ok {
		envConfig.KubernetesContext = resolver.ResolveEffectiveKubernetesContext(environment, envConfig.KubernetesContext)
	}
	return envConfig, nil
}

func resolveOpenRepoPath(envConfig configpkg.EnvConfig) (string, error) {
	repoPath := envConfig.EffectiveLocalRepoPath()
	if repoPath == "" {
		// A runtime env with no mounted source has no repo path; every other type has a worktree.
		if envConfig.Type == configpkg.EnvironmentTypeRuntime && !envConfig.MountsRuntimeSource() {
			return "", nil
		}
		return "", ErrRepoPathNotConfigured
	}
	// A remote worktree path is a POSIX path in the pod; filepath.Clean would break it on Windows.
	if envConfig.RemoteWorktree() {
		return path.Clean(repoPath), nil
	}
	return filepath.Clean(repoPath), nil
}

func validateOpenTarget(tenant, environment, repoPath string, envConfig configpkg.EnvConfig) error {
	if !envConfig.RemoteWorktree() {
		info, err := os.Stat(repoPath)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("%q is not a directory", repoPath)
		}
	}
	// A host env has no cluster. ResolvedType, not !HasPod(), keeps legacy untyped envs requiring a context.
	if envConfig.ResolvedType() == configpkg.EnvironmentTypeHost {
		return nil
	}
	if strings.TrimSpace(envConfig.KubernetesContext) == "" {
		return fmt.Errorf("%w: %s/%s", ErrKubernetesContextNotConfigured, tenant, environment)
	}
	return nil
}

func loadCurrentDirectoryTenant(store configpkg.OpenStore, findProjectRoot seam.ProjectFinderFunc) (string, bool, error) {
	tenantLister, ok := store.(interface {
		ListTenantConfigs() ([]model.TenantConfig, error)
	})
	if !ok {
		return "", false, nil
	}

	if findProjectRoot == nil {
		findProjectRoot = configpkg.FindProjectRoot
	}

	tenant, _, err := findProjectRoot()
	if errors.Is(err, configpkg.ErrNotInGitRepository) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return "", false, nil
	}

	tenants, err := tenantLister.ListTenantConfigs()
	if errors.Is(err, configpkg.ErrNotInitialized) {
		return "", false, nil
	} else if err != nil {
		return "", false, err
	}
	for _, config := range tenants {
		if strings.TrimSpace(config.Name) == tenant {
			return tenant, true, nil
		}
	}
	return "", false, nil
}

// TenantResourcePrefix is a tenant's component-chart resource prefix: the tenant, or "erun"
// when empty, matching the charts' `{{ .Values.tenant | default "erun" }}`.
func TenantResourcePrefix(tenant string) string {
	return strings.TrimSuffix(configpkg.RuntimeReleaseName(tenant), "-devops")
}
