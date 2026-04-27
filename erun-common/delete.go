package eruncommon

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

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
	if store == nil {
		store = ConfigStore{}
	}
	if deleteNamespace == nil {
		deleteNamespace = DeleteKubernetesNamespace
	}

	tenant := strings.TrimSpace(params.Tenant)
	environment := strings.TrimSpace(params.Environment)
	if tenant == "" || environment == "" {
		return DeleteEnvironmentResult{}, fmt.Errorf("tenant and environment are required")
	}

	envConfig, configPath, err := store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return DeleteEnvironmentResult{}, err
	}
	if envConfig.Name == "" {
		envConfig.Name = environment
	}

	result := DeleteEnvironmentResult{
		Tenant:            tenant,
		Environment:       environment,
		Remote:            envConfig.Remote,
		KubernetesContext: strings.TrimSpace(envConfig.KubernetesContext),
		ConfigDir:         filepath.Dir(configPath),
	}

	if envConfig.Remote {
		result.Namespace = KubernetesNamespaceName(tenant, environment)
		if err := ctx.EnsureKubernetesContext(result.KubernetesContext); err != nil {
			return result, err
		}
		TraceDeleteKubernetesNamespace(ctx, result.KubernetesContext, result.Namespace)
		if !ctx.DryRun {
			if err := deleteNamespace(result.KubernetesContext, result.Namespace); err != nil {
				result.NamespaceDeleteError = err.Error()
			}
		}
	}

	ctx.TraceCommand("", "rm", "-rf", result.ConfigDir)
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
