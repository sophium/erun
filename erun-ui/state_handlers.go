package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

func (a *App) LoadState() (uiState, error) {
	result, err := eruncommon.ResolveListResult(a.deps.store, a.deps.findProjectRoot, eruncommon.OpenParams{
		UseDefaultTenant:      true,
		UseDefaultEnvironment: true,
	})
	if err != nil {
		if errors.Is(err, eruncommon.ErrNotInitialized) {
			info := a.deps.resolveBuildInfo()
			return uiState{
				Message:            "ERun is not initialized yet. Run `erun init` first.",
				Build:              buildDetailsFrom(info),
				VersionSuggestions: a.runtimeVersionSuggestions(info, "", ""),
			}, nil
		}
		return uiState{}, err
	}
	info := a.deps.resolveBuildInfo()
	state := stateFromListResult(result, info)
	suggestionTenant := ""
	suggestionEnv := ""
	if state.Selected != nil {
		suggestionTenant = state.Selected.Tenant
		suggestionEnv = state.Selected.Environment
	} else if len(state.Tenants) > 0 {
		suggestionTenant = state.Tenants[0].Name
	}
	state.VersionSuggestions = a.runtimeVersionSuggestions(info, suggestionTenant, suggestionEnv)
	return state, nil
}

func (a *App) resolveRuntimeRegistryVersionsForTenant(namespace, tenant string) eruncommon.RuntimeRegistryVersions {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if namespace = strings.TrimSpace(namespace); namespace == "" {
		namespace = eruncommon.DefaultContainerRegistry
	}
	repository := eruncommon.DefaultRuntimeImageName
	if tenant = strings.TrimSpace(tenant); tenant != "" {
		repository = eruncommon.RuntimeReleaseName(tenant)
	}
	versions, err := a.deps.resolveImageRegistry(ctx, namespace, repository)
	if err != nil {
		return eruncommon.RuntimeRegistryVersions{}
	}
	return versions
}

// runtimeRegistryNamespace returns the container-registry namespace where the
// given environment's runtime image was last published, so the "Version to
// deploy" picker queries the same place `erun deploy` pushed to instead of the
// hardcoded default. It mirrors the deploy provenance recorded by
// PersistRuntimeVersionFromDeploySpecs: the env's persisted
// RuntimeRegistry. When no specific environment is given (the tenant-wide
// initial state and the Upgrade-all plan) the first env of the tenant that
// recorded a registry stands in for the tenant. Returns "" when nothing is
// recorded — a never-deployed env, or one predating the provenance — so the
// caller falls back to DefaultContainerRegistry.
func (a *App) runtimeRegistryNamespace(tenant, environment string) string {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return ""
	}
	envs, err := a.deps.store.ListEnvConfigs(tenant)
	if err != nil {
		return ""
	}
	if environment = strings.TrimSpace(environment); environment != "" {
		for _, env := range envs {
			if strings.EqualFold(strings.TrimSpace(env.Name), environment) {
				return strings.TrimSpace(env.RuntimeRegistry)
			}
		}
		return ""
	}
	for _, env := range envs {
		if registry := strings.TrimSpace(env.RuntimeRegistry); registry != "" {
			return registry
		}
	}
	return ""
}

func (a *App) runtimeVersionSuggestions(info eruncommon.BuildInfo, tenant, environment string) []uiVersion {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return labelRuntimeVersionSuggestions("ERun", eruncommon.DefaultContainerRegistry+"/"+eruncommon.DefaultRuntimeImageName, eruncommon.RuntimeDeployVersionSuggestions(info, a.resolveRuntimeRegistryVersionsForTenant("", "")))
	}

	tenantImage := eruncommon.RuntimeReleaseName(tenant)
	suggestions := make([]uiVersion, 0, 8)
	for _, registry := range a.environmentDiscoveryRegistries(tenant, environment) {
		versions := a.resolveRuntimeRegistryVersionsForTenant(registry, tenant)
		image := strings.TrimRight(strings.TrimSpace(registry), "/") + "/" + tenantImage
		suggestions = append(suggestions, labelRuntimeVersionSuggestions(tenant, image, eruncommon.RuntimeDeployVersionSuggestions(info, versions))...)
	}
	// tenant images are thin wrappers rebuilt from the canonical ERun
	// image, so the canonical channel-latest is part of the env's real target
	// universe. Skipped when the tenant image is the canonical image itself.
	if tenantImage != eruncommon.DefaultRuntimeImageName {
		suggestions = append(suggestions, labelRuntimeVersionSuggestions("ERun", eruncommon.DefaultContainerRegistry+"/"+eruncommon.DefaultRuntimeImageName, eruncommon.RuntimeDeployVersionSuggestions(info, a.resolveRuntimeRegistryVersionsForTenant("", "")))...)
	}
	return suggestions
}

// environmentDiscoveryRegistries returns the registries the version picker
// queries for an environment: the registry the env was last deployed from
// (provenance) plus every registry in the env's marked list, so an
// offered version can come from any listed registry and carry its source.
// Falls back to the canonical registry when nothing is configured.
func (a *App) environmentDiscoveryRegistries(tenant, environment string) []string {
	registries := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	add := func(registry string) {
		registry = strings.TrimSpace(registry)
		if registry == "" {
			return
		}
		if _, ok := seen[registry]; ok {
			return
		}
		seen[registry] = struct{}{}
		registries = append(registries, registry)
	}
	add(a.runtimeRegistryNamespace(tenant, environment))
	if env, ok := a.lookupEnvConfig(tenant, environment); ok {
		for _, registry := range eruncommon.ResolveEnvironmentContainerRegistries(env).DistinctRegistries() {
			add(registry)
		}
	}
	if len(registries) == 0 {
		add(eruncommon.DefaultContainerRegistry)
	}
	return registries
}

func (a *App) lookupEnvConfig(tenant, environment string) (eruncommon.EnvConfig, bool) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" || a.deps.store == nil {
		return eruncommon.EnvConfig{}, false
	}
	envs, err := a.deps.store.ListEnvConfigs(tenant)
	if err != nil {
		return eruncommon.EnvConfig{}, false
	}
	for _, env := range envs {
		if strings.EqualFold(strings.TrimSpace(env.Name), environment) {
			return env, true
		}
	}
	return eruncommon.EnvConfig{}, false
}

func (a *App) LoadVersionSuggestions(selection uiSelection) ([]uiVersion, error) {
	selection = normalizeSelection(selection)
	return a.runtimeVersionSuggestions(a.deps.resolveBuildInfo(), selection.Tenant, selection.Environment), nil
}

func (a *App) LoadKubernetesContexts() ([]string, error) {
	contexts, err := a.deps.listKubeContexts()
	if err != nil {
		return nil, err
	}
	return normalizeKubernetesContexts(contexts), nil
}

func labelRuntimeVersionSuggestions(source, image string, suggestions []uiVersion) []uiVersion {
	source = strings.TrimSpace(source)
	image = strings.TrimSpace(image)
	labeled := make([]uiVersion, 0, len(suggestions))
	for _, suggestion := range suggestions {
		label := strings.TrimSpace(suggestion.Label)
		if source != "" && label != "" {
			label = source + " " + strings.ToLower(label[:1]) + label[1:]
		}
		suggestion.Label = label
		suggestion.Source = source
		suggestion.Image = image
		labeled = append(labeled, suggestion)
	}
	return labeled
}

func stateFromListResult(result eruncommon.ListResult, info eruncommon.BuildInfo) uiState {
	state := uiState{
		Tenants:        make([]uiTenant, 0, len(result.Tenants)),
		Build:          buildDetailsFrom(info),
		CloudProviders: cloudProviderStatusesToUI(result.CloudProviders),
	}
	for _, tenant := range result.Tenants {
		if len(tenant.Environments) == 0 {
			continue
		}
		item := uiTenant{
			Name:                      strings.TrimSpace(tenant.Name),
			DefaultEnvironment:        strings.TrimSpace(tenant.DefaultEnvironment),
			CloudProviderAliases:      append([]string(nil), tenant.CloudProviderAliases...),
			PrimaryCloudProviderAlias: strings.TrimSpace(tenant.PrimaryCloudProviderAlias),
			Environments:              make([]uiEnvironment, 0, len(tenant.Environments)),
		}
		for _, environment := range tenant.Environments {
			item.Environments = append(item.Environments, uiEnvironment{
				Name:              strings.TrimSpace(environment.Name),
				Type:              strings.TrimSpace(string(environment.Type)),
				MCPURL:            mcpEndpointForListEnvironment(environment),
				APIURL:            strings.TrimSpace(environment.APIURL),
				RuntimeVersion:    strings.TrimSpace(environment.RuntimeVersion),
				KubernetesContext: strings.TrimSpace(environment.KubernetesContext),
				IsActive:          environment.IsActive,
				SSHDEnabled:       environment.SSH.Enabled,
				AutoStart:         copyBoolPtr(environment.AutoStart),
			})
		}
		state.Tenants = append(state.Tenants, item)
	}
	if result.CurrentDirectory.Effective != nil {
		state.Selected = &uiSelection{
			Tenant:      strings.TrimSpace(result.CurrentDirectory.Effective.Tenant),
			Environment: strings.TrimSpace(result.CurrentDirectory.Effective.Environment),
		}
	}
	return state
}

func mcpEndpointForOpenResult(result eruncommon.OpenResult) string {
	return fmt.Sprintf("http://127.0.0.1:%d/mcp", eruncommon.MCPPortForResult(result))
}

func mcpEndpointForListEnvironment(environment eruncommon.ListEnvironmentResult) string {
	port := environment.LocalPorts.MCP
	if port <= 0 {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
}

func buildDetailsFrom(info eruncommon.BuildInfo) uiBuildDetails {
	return uiBuildDetails{
		Version: info.Version,
		Commit:  info.Commit,
		Date:    info.Date,
	}
}

func listKubernetesContexts() ([]string, error) {
	output, err := exec.Command("kubectl", "config", "get-contexts", "-o=name").Output()
	if err != nil {
		return nil, wrapKubectlError(err)
	}
	contexts := strings.Split(string(output), "\n")

	currentOutput, err := exec.Command("kubectl", "config", "current-context").Output()
	if err == nil {
		contexts = preferCurrentKubernetesContext(contexts, string(currentOutput))
	}

	return contexts, nil
}

// wrapKubectlError returns an error whose message includes kubectl's stderr
// (when available) so the dialog can show the actual reason for the
// failure — e.g. "stat /Users/x/.kube/config: no such file or directory"
// or "executable file not found in $PATH" — instead of the bare exit-code
// message that exec.Cmd.Output() yields by default.
func wrapKubectlError(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if stderr := strings.TrimSpace(string(exitErr.Stderr)); stderr != "" {
			return fmt.Errorf("%w: %s", err, stderr)
		}
	}
	return err
}

func normalizeKubernetesContexts(contexts []string) []string {
	seen := make(map[string]struct{}, len(contexts))
	result := make([]string, 0, len(contexts))
	for _, context := range contexts {
		context = strings.TrimSpace(context)
		if context == "" {
			continue
		}
		if _, ok := seen[context]; ok {
			continue
		}
		seen[context] = struct{}{}
		result = append(result, context)
	}
	return result
}

func preferCurrentKubernetesContext(contexts []string, current string) []string {
	current = strings.TrimSpace(current)
	if current == "" {
		return contexts
	}

	result := make([]string, 0, len(contexts)+1)
	result = append(result, current)
	for _, context := range contexts {
		if strings.TrimSpace(context) == current {
			continue
		}
		result = append(result, context)
	}
	return result
}

func normalizeSelection(selection uiSelection) uiSelection {
	return uiSelection{
		Tenant:            strings.TrimSpace(selection.Tenant),
		Environment:       strings.TrimSpace(selection.Environment),
		Version:           strings.TrimSpace(selection.Version),
		RuntimeImage:      strings.TrimSpace(selection.RuntimeImage),
		RuntimeCPU:        strings.TrimSpace(selection.RuntimeCPU),
		RuntimeMemory:     strings.TrimSpace(selection.RuntimeMemory),
		KubernetesContext: strings.TrimSpace(selection.KubernetesContext),
		ContainerRegistry: strings.TrimSpace(selection.ContainerRegistry),
		Type:              strings.TrimSpace(selection.Type),
		LocalRepoPath:     strings.TrimSpace(selection.LocalRepoPath),
		NoGit:             selection.NoGit,
		SetDefaultTenant:  selection.SetDefaultTenant,
		Components:        trimmedComponentNames(selection.Components),
	}
}
