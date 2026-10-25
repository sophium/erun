package containerregistry

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

// DefaultContainerRegistries is the seed for a fresh project.
func DefaultContainerRegistries() config.ContainerRegistries {
	return config.ContainerRegistries{{
		Registry: config.DefaultContainerRegistry,
		Roles:    []model.RegistryRole{model.RegistryRoleBuild, model.RegistryRoleDeploy},
	}}
}

// ClusterContainerRegistries seeds a build+deploy in-cluster registry entry whose
// addresses resolve per caller (ClusterIP in pod, port-forward on host).
func ClusterContainerRegistries(cluster config.ClusterRegistry) config.ContainerRegistries {
	c := cluster.WithDefaults()
	return config.ContainerRegistries{{
		Cluster: &c,
		Roles:   []model.RegistryRole{model.RegistryRoleBuild, model.RegistryRoleDeploy},
	}}
}

// HostedRegistryHost is erun's hosted registry, authenticated by the tenant's API token.
const HostedRegistryHost = "registry.erunpaas.com"

// HostedRegistryLoginUsername is ignored by the token endpoint; the tenant comes
// from the password's issuer.
const HostedRegistryLoginUsername = "erun"

// HostedRegistryReference returns the tenant's namespace under the hosted registry;
// the token service clamps scope to it.
func HostedRegistryReference(tenant string) string {
	return HostedRegistryHost + "/" + strings.TrimSpace(tenant)
}

// ResolveEnvironmentContainerRegistries returns the configured list, or nil.
// Reporting must use EffectiveEnvironmentContainerRegistries instead.
func ResolveEnvironmentContainerRegistries(env config.EnvConfig) config.ContainerRegistries {
	if !env.ContainerRegistries.IsZero() {
		return env.ContainerRegistries
	}
	configured, err := configuredContainerRegistries(env.EffectiveLocalRepoPath(), env.Name)
	if err != nil {
		return nil
	}
	return configured
}

// EffectiveEnvironmentContainerRegistries is ResolveEnvironmentContainerRegistries
// with the default seed, so reporting matches what build and deploy use.
func EffectiveEnvironmentContainerRegistries(env config.EnvConfig) config.ContainerRegistries {
	if configured := ResolveEnvironmentContainerRegistries(env); !configured.IsZero() {
		return configured
	}
	return DefaultContainerRegistries()
}

// DeployTargetContainerRegistries resolves and validates the list for a deploy target.
func DeployTargetContainerRegistries(target config.OpenResult) (config.ContainerRegistries, error) {
	if !target.EnvConfig.ContainerRegistries.IsZero() {
		list := target.EnvConfig.ContainerRegistries
		if err := list.Validate(); err != nil {
			return nil, fmt.Errorf("container registries for environment %q: %w", strings.TrimSpace(target.Environment), err)
		}
		return list, nil
	}
	return EffectiveContainerRegistries(target.RepoPath, target.Environment)
}

// configuredContainerRegistries returns the project's explicit list for an
// environment, or nil; display and execution share it.
func configuredContainerRegistries(projectRoot, environment string) (config.ContainerRegistries, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return nil, nil
	}
	projectConfig, _, err := config.LoadProjectConfig(projectRoot)
	if err != nil {
		if errors.Is(err, config.ErrNotInitialized) {
			return nil, nil
		}
		return nil, err
	}
	return projectConfig.ContainerRegistriesForEnvironment(environment), nil
}

// EffectiveContainerRegistries applies the default seed when nothing is configured.
func EffectiveContainerRegistries(projectRoot, environment string) (config.ContainerRegistries, error) {
	list := DefaultContainerRegistries()
	configured, err := configuredContainerRegistries(projectRoot, environment)
	if err != nil {
		return nil, err
	}
	if !configured.IsZero() {
		list = configured
	}
	if err := list.Validate(); err != nil {
		return nil, fmt.Errorf("container registries for environment %q: %w", strings.TrimSpace(environment), err)
	}
	return list, nil
}
