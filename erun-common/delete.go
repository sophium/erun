package eruncommon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// portForwardStateKinds are every kind of forward `erun open` can record for
// one environment. Deleting the environment must clear all three, or whichever
// one is skipped outlives the environment it describes.
var portForwardStateKinds = []string{"mcp", "api", "sshd"}

type (
	NamespaceDeleterFunc func(string, string) error
)

type DeleteStore interface {
	LoadTenantConfig(string) (TenantConfig, string, error)
	SaveTenantConfig(TenantConfig) error
	DeleteTenantConfig(string) error
	LoadERunConfig() (ERunConfig, string, error)
	SaveERunConfig(ERunConfig) error
	LoadEnvConfig(string, string) (EnvConfig, string, error)
	ListEnvConfigs(string) ([]EnvConfig, error)
	DeleteEnvConfig(string, string) error
}

type DeleteEnvironmentParams struct {
	Tenant      string
	Environment string
}

type DeleteEnvironmentResult struct {
	Tenant               string `json:"tenant"`
	Environment          string `json:"environment"`
	Remote               bool   `json:"remote"`
	Namespace            string `json:"namespace,omitempty"`
	KubernetesContext    string `json:"kubernetesContext,omitempty"`
	ConfigDir            string `json:"configDir"`
	NamespaceDeleteError string `json:"namespaceDeleteError,omitempty"`
}

func DeleteEnvironmentConfirmation(tenant, environment string) string {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return ""
	}
	return tenant + "-" + environment
}

func RunDeleteEnvironment(ctx Context, params DeleteEnvironmentParams, store DeleteStore, deleteNamespace NamespaceDeleterFunc) (DeleteEnvironmentResult, error) {
	store, deleteNamespace = normalizeDeleteEnvironmentDependencies(store, deleteNamespace)

	tenant := strings.TrimSpace(params.Tenant)
	environment := strings.TrimSpace(params.Environment)
	if err := errMissingTenantOrEnvironment("delete environment", tenant, environment); err != nil {
		return DeleteEnvironmentResult{}, err
	}

	envConfig, configPath, err := store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return DeleteEnvironmentResult{}, err
	}
	result := deleteEnvironmentResult(tenant, environment, envConfig, configPath)

	if envConfig.RemoteWorktree() {
		if err := deleteRemoteEnvironmentNamespace(ctx, deleteNamespace, &result); err != nil {
			return result, err
		}
	}

	ctx.TraceCommand("", "rm", "-rf", result.ConfigDir)
	if err := removePortForwardStateFiles(ctx, tenant, environment); err != nil {
		return result, err
	}
	if ctx.DryRun {
		return result, nil
	}

	if err := store.DeleteEnvConfig(tenant, environment); err != nil {
		return result, err
	}
	if err := removeTenantWhenLastEnvironmentDeleted(ctx, store, tenant, environment); err != nil {
		return result, err
	}

	return result, nil
}

// removePortForwardStateFiles deletes every port-forward state file this
// environment could have. Without it, the local port range the file names
// keeps getting freed and reissued to whichever environment is created next,
// while the deleted environment's file still claims it — so a stale record
// resolves to a live forward that belongs to somebody else instead of reading
// as "no forward" the way a missing file does.
func removePortForwardStateFiles(ctx Context, tenant, environment string) error {
	for _, kind := range portForwardStateKinds {
		path, err := PortForwardStatePath(kind, tenant, environment)
		if err != nil {
			return err
		}
		if _, statErr := os.Stat(path); statErr != nil {
			continue
		}
		ctx.TraceCommand("", "rm", "-f", path)
		if ctx.DryRun {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func normalizeDeleteEnvironmentDependencies(store DeleteStore, deleteNamespace NamespaceDeleterFunc) (DeleteStore, NamespaceDeleterFunc) {
	if store == nil {
		store = ConfigStore{}
	}
	if deleteNamespace == nil {
		deleteNamespace = DeleteKubernetesNamespace
	}
	return store, deleteNamespace
}

func deleteEnvironmentResult(tenant, environment string, envConfig EnvConfig, configPath string) DeleteEnvironmentResult {
	return DeleteEnvironmentResult{
		Tenant:            tenant,
		Environment:       environment,
		Remote:            envConfig.RemoteWorktree(),
		KubernetesContext: strings.TrimSpace(envConfig.KubernetesContext),
		ConfigDir:         filepath.Dir(configPath),
	}
}

func deleteRemoteEnvironmentNamespace(ctx Context, deleteNamespace NamespaceDeleterFunc, result *DeleteEnvironmentResult) error {
	result.Namespace = KubernetesNamespaceName(result.Tenant, result.Environment)
	if err := ctx.RequireKubernetesContext(result.KubernetesContext); err != nil {
		return fmt.Errorf("delete environment %s/%s: %w", result.Tenant, result.Environment, err)
	}
	TraceDeleteKubernetesNamespace(ctx, result.KubernetesContext, result.Namespace)
	if ctx.DryRun {
		return nil
	}
	if err := deleteNamespace(result.KubernetesContext, result.Namespace); err != nil {
		result.NamespaceDeleteError = err.Error()
	}
	return nil
}

func removeTenantWhenLastEnvironmentDeleted(ctx Context, store DeleteStore, tenant, deletedEnvironment string) error {
	envs, err := store.ListEnvConfigs(tenant)
	if err != nil {
		return err
	}
	for _, env := range envs {
		if strings.TrimSpace(env.Name) != "" && strings.TrimSpace(env.Name) != deletedEnvironment {
			return clearDeletedDefaultEnvironment(ctx, store, tenant, deletedEnvironment)
		}
	}

	ctx.TraceCommand("", "rm", "-rf", "tenant-config:"+tenant)
	if err := store.DeleteTenantConfig(tenant); err != nil {
		return err
	}
	return clearDeletedDefaultTenant(ctx, store, tenant)
}

func clearDeletedDefaultTenant(ctx Context, store DeleteStore, deletedTenant string) error {
	toolConfig, _, err := store.LoadERunConfig()
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
			return nil
		}
		return err
	}
	if strings.TrimSpace(toolConfig.DefaultTenant) != deletedTenant {
		return nil
	}
	toolConfig.DefaultTenant = ""
	ctx.TraceCommand("", "write-yaml", "erun-config")
	return store.SaveERunConfig(toolConfig)
}

func clearDeletedDefaultEnvironment(ctx Context, store DeleteStore, tenant, deletedEnvironment string) error {
	tenantConfig, _, err := store.LoadTenantConfig(tenant)
	if err != nil {
		return err
	}
	if tenantConfig.Name == "" {
		tenantConfig.Name = tenant
	}
	if strings.TrimSpace(tenantConfig.DefaultEnvironment) != deletedEnvironment {
		return nil
	}

	nextDefault := ""
	envs, err := store.ListEnvConfigs(tenant)
	if err != nil {
		return err
	}
	for _, env := range envs {
		name := strings.TrimSpace(env.Name)
		if name != "" && name != deletedEnvironment {
			nextDefault = name
			break
		}
	}

	tenantConfig.DefaultEnvironment = nextDefault
	ctx.TraceCommand("", "write-yaml", "tenant-config:"+tenant)
	return store.SaveTenantConfig(tenantConfig)
}
