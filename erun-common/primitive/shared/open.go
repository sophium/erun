package shared

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

var (
	ErrDefaultTenantNotConfigured      = errors.New("default tenant is not configured")
	ErrDefaultEnvironmentNotConfigured = errors.New("default environment is not configured")
	ErrTenantNotFound                  = errors.New("no such tenant exists")
	ErrKubernetesContextNotConfigured  = errors.New("kubernetes context is not configured")
	ErrRepoPathNotConfigured           = errors.New("repo path is not configured")
	// ErrOpenTenantNotProvided is open's tenant-resolution failure for a call
	// path that is not allowed to infer one (UseDefaultTenant is false) —
	// distinct from ErrDefaultTenantNotConfigured, where inference was
	// permitted but resolved to nothing, because the two have different
	// recoveries (root AGENTS.md "Distinguish causes before writing copy").
	ErrOpenTenantNotProvided = errors.New("no tenant given for open")
	// ErrOpenEnvironmentNotProvided mirrors ErrOpenTenantNotProvided for the
	// environment half of open's target resolution.
	ErrOpenEnvironmentNotProvided = errors.New("no environment given for open")
)

type effectiveKubernetesContextResolver interface {
	ResolveEffectiveKubernetesContext(environment, configured string) string
}

type OpenParams struct {
	Tenant                string
	Environment           string
	UseDefaultTenant      bool
	UseDefaultEnvironment bool
	// Command is the operator-facing invocation of the command resolving this
	// target (for example "erun usage", "erun outputs list"), used only to name
	// that command's own recovery in a resolution failure. Empty means the
	// caller did not name itself: the failure then falls back to the
	// command-free wording rather than borrowing another command's name, so a
	// new command can adopt this path without inheriting a misleading one.
	Command string
	// CommandScopesTenantByFlag gates whether the recovery may name Command's
	// --tenant flag as the fix — see Context.CommandScopesTenantByFlag.
	CommandScopesTenantByFlag bool
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

func ResolveOpenWithFinder(store configpkg.OpenStore, findProjectRoot ProjectFinderFunc, params OpenParams) (configpkg.OpenResult, error) {
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

// ResolveOpenGateway reads the erun-level gateway catalog for a resolved
// target. A root config that cannot be read leaves it unset rather than failing
// the open: the catalog only changes how an AI session is routed, and the
// unreadable config itself is reported by whatever inspects the root config.
func ResolveOpenGateway(store configpkg.OpenStore) *configpkg.OpenRouterConfig {
	gateway, err := configpkg.ResolveOpenRouterConfig(store)
	if err != nil {
		return nil
	}
	return gateway
}

// tenantResolutionRemedy renders the second half of resolveOpenTenant's
// inference-permitted-but-unresolved error: that working-directory inference
// also came up empty, and what the operator can do about it.
//
// The recovery is stated for the command that actually failed, with that
// command's own flag, because it is the next thing the operator runs. A fixed
// command name here sends the reader to an operation they never invoked and
// did not need — `erun usage` failing must not report that `open` could not
// infer a tenant, since the reasonable readings of that are all wrong (a typo,
// a hidden delegation to open, or open being the fault).
//
// command is empty for a caller that did not name itself; the wording then
// keeps the disclosure and both remedies but names no operation, which stays
// correct rather than guessing. That is also what keeps this correct as new
// commands adopt the resolution path: naming yourself is what buys the
// command-specific recovery, and not naming yourself is never wrong.
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

func ResolveOpenTenant(store configpkg.OpenStore, findProjectRoot ProjectFinderFunc, params OpenParams) (string, error) {
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

func loadOpenTenantConfig(store configpkg.OpenStore, tenant string) (configpkg.TenantConfig, error) {
	tenantConfig, _, err := store.LoadTenantConfig(tenant)
	if errors.Is(err, configpkg.ErrNotInitialized) {
		return configpkg.TenantConfig{}, fmt.Errorf("%w: %s", ErrTenantNotFound, tenant)
	}
	if err != nil {
		return configpkg.TenantConfig{}, err
	}
	if tenantConfig.Name == "" {
		tenantConfig.Name = tenant
	}
	return tenantConfig, nil
}

func resolveOpenEnvironment(params OpenParams, tenantConfig configpkg.TenantConfig) (string, error) {
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
		// A runtime env with no mounted source worktree genuinely has no repo path
		// (RemoteWorktree's doc: "none for runtime") — a control-plane-provisioned
		// tenant with no project never has one to configure. Every other type has a
		// real worktree (hostPath for local-agent, a PVC for remote-agent, or a
		// runtime env's own opted-in mutable-source clone), so those still require it.
		if envConfig.Type == configpkg.EnvironmentTypeRuntime && !envConfig.MountsRuntimeSource() {
			return "", nil
		}
		return "", ErrRepoPathNotConfigured
	}
	// A remote/PVC-worktree env's repo path is a POSIX path inside the pod, not a
	// host path. filepath.Clean would rewrite "/home/erun/..." to "\home\erun\..."
	// on Windows, which then breaks the emitted `cd`/Set-Location (and any in-pod
	// use). Normalize with forward-slash `path` for remote envs; only local-agent
	// envs get the OS-native filepath.Clean.
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
	// A host env has no pod and no cluster at all, so it has no kubernetes
	// context to require — every other type deploys into one. Checked against
	// ResolvedType rather than the broader !HasPod() so a legacy env with an
	// unresolved type keeps requiring a context exactly as it did before host
	// existed.
	if envConfig.ResolvedType() == configpkg.EnvironmentTypeHost {
		return nil
	}
	if strings.TrimSpace(envConfig.KubernetesContext) == "" {
		return fmt.Errorf("%w: %s/%s", ErrKubernetesContextNotConfigured, tenant, environment)
	}
	return nil
}

func loadCurrentDirectoryTenant(store configpkg.OpenStore, findProjectRoot ProjectFinderFunc) (string, bool, error) {
	tenantLister, ok := store.(interface {
		ListTenantConfigs() ([]configpkg.TenantConfig, error)
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

// TenantResourcePrefix is the prefix a tenant's component-chart Kubernetes
// resources carry (<prefix>-postgres, <prefix>-api, <prefix>-powerdns, ...),
// matching the charts' `{{ .Values.tenant | default "erun" }}` scoping: the
// tenant name, or "erun" for the canonical product (empty tenant). Code that
// addresses those resources by name (API port-forward, PowerDNS exec) derives
// the name through this so it stays in lockstep with the charts.
func TenantResourcePrefix(tenant string) string {
	return strings.TrimSuffix(configpkg.RuntimeReleaseName(tenant), "-devops")
}
