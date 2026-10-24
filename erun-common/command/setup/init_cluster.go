package setup

import (
	"context"
	"errors"
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/open"
)

func (s BootstrapRunner) resolveKubernetesContext(params open.BootstrapInitParams, tenant, envName, current string) (string, error) {
	if params.KubernetesContext != "" {
		return params.KubernetesContext, nil
	}

	current = strings.TrimSpace(current)
	if current != "" || params.AutoApprove {
		return current, nil
	}

	if s.PromptKubernetesContext == nil {
		return "", BootstrapInitInteractionError{Interaction: BootstrapInitInteraction{
			Type:  BootstrapInitInteractionKubernetesContext,
			Label: kubernetesContextLabel(tenant, envName),
		}}
	}

	context, err := s.PromptKubernetesContext(kubernetesContextLabel(tenant, envName))
	if err != nil {
		return "", err
	}
	context = strings.TrimSpace(context)
	if context == "" {
		return "", ErrKubernetesContextCancelled
	}
	return context, nil
}

func (s BootstrapRunner) ResolveContainerRegistry(params open.BootstrapInitParams, tenant, envName, projectRoot, current string, creating bool) (string, error) {
	// A cluster registry is written as a resolvable cluster: entry, not a static
	// string, so short-circuit the string resolution/prompt entirely.
	if params.ClusterRegistry != nil {
		return "", nil
	}
	if params.ErunRegistry {
		return s.resolveHostedRegistry(tenant)
	}
	if params.ContainerRegistry != "" {
		return params.ContainerRegistry, nil
	}

	projectRegistry, err := s.projectContainerRegistry(projectRoot, envName)
	if err != nil || projectRegistry != "" {
		return projectRegistry, err
	}

	current = strings.TrimSpace(current)
	if current != "" {
		return current, nil
	}
	if !creating {
		return "", nil
	}
	if params.AutoApprove {
		return configpkg.DefaultContainerRegistry, nil
	}
	return s.promptContainerRegistry(tenant, envName)
}

// resolveHostedRegistry resolves erun's hosted container registry reference,
// probing its reachability so a choice nothing can push to is refused at
// selection time rather than surfacing at the first build far from the flag
// that chose it.
func (s BootstrapRunner) resolveHostedRegistry(tenant string) (string, error) {
	reference := configpkg.HostedRegistryReference(tenant)
	// A dry-run resolves and traces without touching the world, so it does
	// not probe. Saying so is the point: a dry-run that printed the resolved
	// reference alone would read as "this choice works", which is the very
	// impression the probe exists to stop the real run from giving.
	if s.Context.DryRun {
		s.Context.Trace("init: --erun-registry resolves to " + reference + "; the real run probes " + configpkg.HostedRegistryHost + " and refuses the choice if it is not reachable")
		return reference, nil
	}
	if status := s.probeHostedRegistry(); !status.Available {
		return "", status.Err()
	}
	return reference, nil
}

func (s BootstrapRunner) projectContainerRegistry(projectRoot, envName string) (string, error) {
	if projectRoot == "" || s.LoadProjectConfig == nil {
		return "", nil
	}
	projectConfig, _, err := s.LoadProjectConfig(projectRoot)
	if err != nil {
		if errors.Is(err, configpkg.ErrNotInitialized) {
			return "", nil
		}
		return "", err
	}
	registry, _ := projectConfig.ContainerRegistriesForEnvironment(envName).BuildRegistry()
	return registry, nil
}

func (s BootstrapRunner) promptContainerRegistry(tenant, envName string) (string, error) {
	if s.PromptContainerRegistry == nil {
		return "", BootstrapInitInteractionError{Interaction: BootstrapInitInteraction{
			Type:         BootstrapInitInteractionContainerRegistry,
			Label:        containerRegistryLabel(tenant, envName),
			DefaultValue: configpkg.DefaultContainerRegistry,
		}}
	}

	registry, err := s.PromptContainerRegistry(containerRegistryLabel(tenant, envName))
	if err != nil {
		return "", err
	}
	registry = strings.TrimSpace(registry)
	if registry == "" {
		return configpkg.DefaultContainerRegistry, nil
	}
	return registry, nil
}

func (s BootstrapRunner) resolveCloudProviderAlias(kubernetesContext, current string) (string, error) {
	current = strings.TrimSpace(current)
	if current != "" {
		return current, nil
	}

	status, ok, err := configpkg.FindCloudContextForKubernetesContext(s.Store, kubernetesContext)
	if err != nil || !ok {
		return current, err
	}
	return strings.TrimSpace(status.CloudProviderAlias), nil
}

func (s BootstrapRunner) saveProjectContainerRegistry(projectRoot, envName, registry string, params open.BootstrapInitParams) error {
	// Remote envs record the registry on the env config only; the shared project
	// config is for local-agent envs whose worktree lives in this repo.
	if params.Remote {
		return nil
	}
	if projectRoot == "" || envName == "" || s.SaveProjectConfig == nil {
		return nil
	}

	if params.ClusterRegistry != nil {
		return s.saveClusterContainerRegistry(projectRoot, envName, *params.ClusterRegistry)
	}
	return s.savePlainContainerRegistry(projectRoot, envName, registry)
}

func (s BootstrapRunner) saveClusterContainerRegistry(projectRoot, envName string, cluster configpkg.ClusterRegistry) error {
	projectConfig, err := s.loadProjectConfigForContainerRegistry(projectRoot)
	if err != nil {
		return err
	}
	desired := configpkg.ClusterContainerRegistries(cluster)
	if projectConfig.ContainerRegistriesForEnvironment(envName).Equal(desired) {
		return nil
	}
	projectConfig.SetContainerRegistriesForEnvironment(envName, desired)
	return s.SaveProjectConfig(projectRoot, projectConfig)
}

func (s BootstrapRunner) savePlainContainerRegistry(projectRoot, envName, registry string) error {
	if registry == "" {
		return nil
	}
	projectConfig, err := s.loadProjectConfigForContainerRegistry(projectRoot)
	if err != nil {
		return err
	}
	if existing, _ := projectConfig.ContainerRegistriesForEnvironment(envName).BuildRegistry(); existing == registry {
		return nil
	}
	projectConfig.SetContainerRegistriesForEnvironment(envName, configpkg.SingleContainerRegistries(registry))
	return s.SaveProjectConfig(projectRoot, projectConfig)
}

func (s BootstrapRunner) loadProjectConfigForContainerRegistry(projectRoot string) (configpkg.ProjectConfig, error) {
	if s.LoadProjectConfig == nil {
		return configpkg.ProjectConfig{}, nil
	}
	projectConfig, _, err := s.LoadProjectConfig(projectRoot)
	if err != nil {
		if errors.Is(err, configpkg.ErrNotInitialized) {
			return configpkg.ProjectConfig{}, nil
		}
		return configpkg.ProjectConfig{}, err
	}
	return projectConfig, nil
}

func (s BootstrapRunner) ensureKubernetesNamespace(tenant, envName, currentContext, nextContext string) error {
	if s.EnsureKubernetesNamespace == nil {
		return nil
	}

	nextContext = strings.TrimSpace(nextContext)
	if nextContext == "" || nextContext == strings.TrimSpace(currentContext) {
		return nil
	}
	if err := s.Context.EnsureKubernetesContext(nextContext); err != nil {
		return err
	}

	namespace := configpkg.KubernetesNamespaceName(tenant, envName)
	if namespace == "" {
		return fmt.Errorf("kubernetes namespace name is empty for tenant %q and environment %q", tenant, envName)
	}

	return s.EnsureKubernetesNamespace(nextContext, namespace)
}

// probeHostedRegistry runs the availability probe the hosted-registry choice is
// gated on, defaulting to the real one when no dependency was injected.
func (s BootstrapRunner) probeHostedRegistry() HostedRegistryStatus {
	probe := s.ProbeHostedRegistry
	if probe == nil {
		probe = func(ctx context.Context) HostedRegistryStatus {
			return ProbeHostedRegistry(ctx, nil)
		}
	}
	return probe(context.Background())
}
