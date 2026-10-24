package main

import (
	"errors"
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

// applyContainerRegistries stores local-agent and host lists in project config
// and remote/runtime lists on the env config, whose project config is not local.
func (a *App) applyContainerRegistries(selection uiSelection, updated *configpkg.EnvConfig, registries configpkg.ContainerRegistries) error {
	switch updated.ResolvedType() {
	case configpkg.EnvironmentTypeLocalAgent, configpkg.EnvironmentTypeHost:
		updated.ContainerRegistries = nil
		return a.saveEnvironmentProjectRegistries(selection.Tenant, *updated, registries)
	case configpkg.EnvironmentTypeRemoteAgent, configpkg.EnvironmentTypeRuntime:
		fallthrough
	default:
		updated.ContainerRegistries = registries.Clone()
		return nil
	}
}

func (a *App) saveEnvironmentProjectRegistries(tenant string, config configpkg.EnvConfig, registries configpkg.ContainerRegistries) error {
	projectRoot, store, ok := a.environmentProjectConfigStore(config)
	if !ok {
		return fmt.Errorf("cannot resolve the project root for %q; a local-agent environment's container registries are stored in its repo's .erun/config.yaml", strings.TrimSpace(config.Name))
	}
	projectConfig, _, err := store.LoadProjectConfig(projectRoot)
	if err != nil && !errors.Is(err, configpkg.ErrNotInitialized) {
		return err
	}
	projectConfig.SetContainerRegistriesForEnvironment(environmentName(config.Name, config), registries)
	return store.SaveProjectConfig(projectRoot, projectConfig)
}

func containerRegistriesToUI(list configpkg.ContainerRegistries) []uiContainerRegistryEntry {
	entries := make([]uiContainerRegistryEntry, 0, len(list))
	for _, entry := range list {
		roles := make([]string, 0, len(entry.Roles))
		for _, role := range entry.Roles {
			roles = append(roles, string(role))
		}
		uiEntry := uiContainerRegistryEntry{Registry: strings.TrimSpace(entry.Registry), Roles: roles}
		// A cluster entry has no static host; show its resolved address instead.
		if entry.Cluster != nil {
			cluster := entry.Cluster.WithDefaults()
			uiEntry.Cluster = &uiContainerRegistryCluster{
				Service:   cluster.Service,
				Namespace: cluster.Namespace,
				Port:      cluster.Port,
				Insecure:  cluster.Insecure,
				Label:     fmt.Sprintf("%s.%s:%d", cluster.Service, cluster.Namespace, cluster.Port),
			}
		}
		entries = append(entries, uiEntry)
	}
	return entries
}

// An empty result clears the override so the env inherits the project default.
func uiToContainerRegistries(entries []uiContainerRegistryEntry) (configpkg.ContainerRegistries, error) {
	list := make(configpkg.ContainerRegistries, 0, len(entries))
	for _, entry := range entries {
		roles := make([]configpkg.RegistryRole, 0, len(entry.Roles))
		for _, role := range entry.Roles {
			if trimmed := strings.TrimSpace(role); trimmed != "" {
				roles = append(roles, configpkg.RegistryRole(trimmed))
			}
		}
		// Preserve a cluster entry so a context-resolved registry is not dropped.
		if entry.Cluster != nil {
			list = append(list, configpkg.ContainerRegistryEntry{
				Cluster: &configpkg.ClusterRegistry{
					Service:   strings.TrimSpace(entry.Cluster.Service),
					Namespace: strings.TrimSpace(entry.Cluster.Namespace),
					Port:      entry.Cluster.Port,
					Insecure:  entry.Cluster.Insecure,
				},
				Roles: roles,
			})
			continue
		}
		registry := strings.TrimSpace(entry.Registry)
		if registry == "" {
			continue
		}
		list = append(list, configpkg.ContainerRegistryEntry{Registry: registry, Roles: roles})
	}
	if list.IsZero() {
		return nil, nil
	}
	if err := list.Validate(); err != nil {
		return nil, err
	}
	return list, nil
}

// effectiveEnvironmentContainerRegistries mirrors the build/deploy resolution
// order; the bool reports inheritance from the project config.
func (a *App) effectiveEnvironmentContainerRegistries(tenant, environment string, config configpkg.EnvConfig) (configpkg.ContainerRegistries, bool) {
	if !config.ContainerRegistries.IsZero() {
		return config.ContainerRegistries, false
	}
	projectConfig, ok := a.loadEnvironmentProjectConfig(tenant, config)
	if !ok {
		return nil, false
	}
	list := projectConfig.ContainerRegistriesForEnvironment(environmentName(environment, config))
	return list, !list.IsZero()
}

func (a *App) loadEnvironmentProjectConfig(tenant string, config configpkg.EnvConfig) (configpkg.ProjectConfig, bool) {
	projectRoot, store, ok := a.environmentProjectConfigStore(config)
	if !ok {
		return configpkg.ProjectConfig{}, false
	}
	projectConfig, _, err := store.LoadProjectConfig(projectRoot)
	if err != nil {
		return configpkg.ProjectConfig{}, false
	}
	return projectConfig, true
}

func (a *App) environmentProjectConfigStore(config configpkg.EnvConfig) (string, projectConfigStore, bool) {
	if a.deps.store == nil {
		return "", nil, false
	}
	projectRoot := strings.TrimSpace(config.EffectiveLocalRepoPath())
	if projectRoot == "" {
		return "", nil, false
	}
	store, ok := a.deps.store.(projectConfigStore)
	if !ok {
		return "", nil, false
	}
	return projectRoot, store, true
}
