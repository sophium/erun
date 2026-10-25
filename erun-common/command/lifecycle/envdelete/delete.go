package envdelete

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	"github.com/sophium/erun/erun-common/base/config/sshconfig"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/envstore"
	"github.com/sophium/erun/erun-common/base/seam/kubeapi"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/open/sshconn"
)

type DeleteEnvironmentResult struct {
	Tenant               string `json:"tenant"`
	Environment          string `json:"environment"`
	Remote               bool   `json:"remote"`
	Namespace            string `json:"namespace,omitempty"`
	KubernetesContext    string `json:"kubernetesContext,omitempty"`
	ConfigDir            string `json:"configDir"`
	NamespaceDeleteError string `json:"namespaceDeleteError,omitempty"`
	// RemovedSSHHostAlias names the ~/.ssh/config Host block this delete removed.
	RemovedSSHHostAlias string `json:"removedSshHostAlias,omitempty"`
}

func DeleteEnvironmentConfirmation(tenant, environment string) string {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return ""
	}
	return tenant + "-" + environment
}

func RunDeleteEnvironment(ctx core.Context, params model.DeleteEnvironmentParams, store envstore.DeleteStore, deleteNamespace kubeapi.NamespaceDeleterFunc) (DeleteEnvironmentResult, error) {
	store, deleteNamespace = normalizeDeleteEnvironmentDependencies(store, deleteNamespace)

	tenant := strings.TrimSpace(params.Tenant)
	environment := strings.TrimSpace(params.Environment)
	if err := core.ErrMissingTenantOrEnvironment("delete environment", tenant, environment); err != nil {
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
	if err := removeEnvironmentSSHConfigAlias(ctx, store, tenant, environment, &result); err != nil {
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

// removePortForwardStateFiles deletes the state file and log at canonical and legacy paths;
// a stale record would resolve to the next environment reusing the freed port.
func removePortForwardStateFiles(ctx core.Context, tenant, environment string) error {
	for _, kind := range kube.PortForwardStateKinds {
		if err := kube.RemovePortForwardRecord(ctx, kind, tenant, environment); err != nil {
			return err
		}
	}
	return nil
}

// A block that outlived its environment would connect to whichever env reuses the freed port.
// Only removed while no other environment derives the same alias.
func removeEnvironmentSSHConfigAlias(ctx core.Context, store envstore.DeleteStore, tenant, environment string, result *DeleteEnvironmentResult) error {
	alias := sshconn.SSHHostAlias(tenant, environment)
	if alias == "" {
		return nil
	}
	claimed, err := sshAliasClaimedByOtherEnvironment(store, tenant, environment, alias)
	if err != nil {
		return err
	}
	if claimed {
		ctx.Trace(fmt.Sprintf("delete: keeping ssh config block Host %s: another environment still derives that alias", alias))
		return nil
	}
	path, err := sshconfig.DefaultSSHConfigPath()
	if err != nil {
		return err
	}
	hasBlock, err := sshconfig.SSHConfigHasAlias(path, alias)
	if err != nil {
		return fmt.Errorf("read ssh config %s: %w", path, err)
	}
	if !hasBlock {
		return nil
	}

	ctx.Trace(fmt.Sprintf("delete: removing Host %s block from %s", alias, path))
	result.RemovedSSHHostAlias = alias
	if ctx.DryRun {
		return nil
	}
	if _, err := sshconfig.RemoveSSHConfigAlias(path, alias); err != nil {
		return fmt.Errorf("remove Host %s block from %s: %w", alias, path, err)
	}
	return nil
}

// Distinct names can sanitize to the same alias, so compare derived aliases.
func sshAliasClaimedByOtherEnvironment(store envstore.DeleteStore, tenant, environment, alias string) (bool, error) {
	environments, err := store.ListEnvConfigs(tenant)
	if err != nil {
		return false, err
	}
	environment = strings.TrimSpace(environment)
	for _, env := range environments {
		name := strings.TrimSpace(env.Name)
		if name == "" || name == environment {
			continue
		}
		if sshconn.SSHHostAlias(tenant, name) == alias {
			return true, nil
		}
	}
	return false, nil
}

func normalizeDeleteEnvironmentDependencies(store envstore.DeleteStore, deleteNamespace kubeapi.NamespaceDeleterFunc) (envstore.DeleteStore, kubeapi.NamespaceDeleterFunc) {
	if store == nil {
		store = config.ConfigStore{}
	}
	if deleteNamespace == nil {
		deleteNamespace = kube.DeleteKubernetesNamespace
	}
	return store, deleteNamespace
}

func deleteEnvironmentResult(tenant, environment string, envConfig config.EnvConfig, configPath string) DeleteEnvironmentResult {
	return DeleteEnvironmentResult{
		Tenant:            tenant,
		Environment:       environment,
		Remote:            envConfig.RemoteWorktree(),
		KubernetesContext: strings.TrimSpace(envConfig.KubernetesContext),
		ConfigDir:         filepath.Dir(configPath),
	}
}

func deleteRemoteEnvironmentNamespace(ctx core.Context, deleteNamespace kubeapi.NamespaceDeleterFunc, result *DeleteEnvironmentResult) error {
	result.Namespace = runtimetarget.KubernetesNamespaceName(result.Tenant, result.Environment)
	if err := ctx.RequireKubernetesContext(result.KubernetesContext); err != nil {
		return fmt.Errorf("delete environment %s/%s: %w", result.Tenant, result.Environment, err)
	}
	kube.TraceDeleteKubernetesNamespace(ctx, result.KubernetesContext, result.Namespace)
	if ctx.DryRun {
		return nil
	}
	if err := deleteNamespace(result.KubernetesContext, result.Namespace); err != nil {
		result.NamespaceDeleteError = err.Error()
	}
	return nil
}

func removeTenantWhenLastEnvironmentDeleted(ctx core.Context, store envstore.DeleteStore, tenant, deletedEnvironment string) error {
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

func clearDeletedDefaultTenant(ctx core.Context, store envstore.DeleteStore, deletedTenant string) error {
	toolConfig, _, err := store.LoadERunConfig()
	if err != nil {
		if errors.Is(err, config.ErrNotInitialized) {
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

func clearDeletedDefaultEnvironment(ctx core.Context, store envstore.DeleteStore, tenant, deletedEnvironment string) error {
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
