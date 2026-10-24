package lifecycle

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/open"
)

type (
	NamespaceDeleterFunc func(string, string) error
)

type DeleteStore interface {
	LoadTenantConfig(string) (config.TenantConfig, string, error)
	SaveTenantConfig(config.TenantConfig) error
	DeleteTenantConfig(string) error
	LoadERunConfig() (config.ERunConfig, string, error)
	SaveERunConfig(config.ERunConfig) error
	LoadEnvConfig(string, string) (config.EnvConfig, string, error)
	ListEnvConfigs(string) ([]config.EnvConfig, error)
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
	// RemovedSSHHostAlias names the ~/.ssh/config Host block this delete
	// removed, so a caller can report the local-state cleanup instead of
	// leaving the operator to wonder whether the alias outlived the env.
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

func RunDeleteEnvironment(ctx core.Context, params DeleteEnvironmentParams, store DeleteStore, deleteNamespace NamespaceDeleterFunc) (DeleteEnvironmentResult, error) {
	store, deleteNamespace = normalizeDeleteEnvironmentDependencies(store, deleteNamespace)

	tenant := strings.TrimSpace(params.Tenant)
	environment := strings.TrimSpace(params.Environment)
	if err := model.ErrMissingTenantOrEnvironment("delete environment", tenant, environment); err != nil {
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

// removePortForwardStateFiles deletes every port-forward record this
// environment could have — the state file and the log beside it, at the
// canonical location and at any legacy one it was never migrated from. Without it,
// the local port range the file names keeps getting freed and reissued to
// whichever environment is created next, while the deleted environment's file
// still claims it — so a stale record resolves to a live forward that belongs
// to somebody else instead of reading as "no forward" the way a missing file
// does.
//
// The log is removed with the record it belongs to, which is the whole of the
// difference between a forward tree that tracks the environments it describes
// and one that outlives them: a state file reclaimed without its log leaves a
// file that nothing else in erun will ever open, restart, or rotate again, for
// an environment that no longer exists to reopen it.
func removePortForwardStateFiles(ctx core.Context, tenant, environment string) error {
	for _, kind := range kube.PortForwardStateKinds {
		if err := kube.RemovePortForwardRecord(ctx, kind, tenant, environment); err != nil {
			return err
		}
	}
	return nil
}

// removeEnvironmentSSHConfigAlias removes the ~/.ssh/config Host block that
// `erun sshd init` wrote for this environment, for the same reason
// removePortForwardStateFiles removes the state file: the block names a local
// port that this delete frees for whichever environment is created next, so a
// block that outlives its environment does not fail — it succeeds against a
// live pod the operator did not name. Only the alias this environment derives
// is touched, and only while no other configured environment derives it, so a
// block a live environment legitimately owns survives.
func removeEnvironmentSSHConfigAlias(ctx core.Context, store DeleteStore, tenant, environment string, result *DeleteEnvironmentResult) error {
	alias := open.SSHHostAlias(tenant, environment)
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
	path, err := config.DefaultSSHConfigPath()
	if err != nil {
		return err
	}
	hasBlock, err := config.SSHConfigHasAlias(path, alias)
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
	if _, err := config.RemoveSSHConfigAlias(path, alias); err != nil {
		return fmt.Errorf("remove Host %s block from %s: %w", alias, path, err)
	}
	return nil
}

// sshAliasClaimedByOtherEnvironment reports whether an environment other than
// the one being deleted still derives alias. Distinct environment names can
// sanitize to the same alias, so the check is on the derived alias, not the
// name, or deleting one would strip the other's block.
func sshAliasClaimedByOtherEnvironment(store DeleteStore, tenant, environment, alias string) (bool, error) {
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
		if open.SSHHostAlias(tenant, name) == alias {
			return true, nil
		}
	}
	return false, nil
}

func normalizeDeleteEnvironmentDependencies(store DeleteStore, deleteNamespace NamespaceDeleterFunc) (DeleteStore, NamespaceDeleterFunc) {
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

func deleteRemoteEnvironmentNamespace(ctx core.Context, deleteNamespace NamespaceDeleterFunc, result *DeleteEnvironmentResult) error {
	result.Namespace = config.KubernetesNamespaceName(result.Tenant, result.Environment)
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

func removeTenantWhenLastEnvironmentDeleted(ctx core.Context, store DeleteStore, tenant, deletedEnvironment string) error {
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

func clearDeletedDefaultTenant(ctx core.Context, store DeleteStore, deletedTenant string) error {
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

func clearDeletedDefaultEnvironment(ctx core.Context, store DeleteStore, tenant, deletedEnvironment string) error {
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
