package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	registrypkg "github.com/sophium/erun/erun-common/infra/registry"
)

func (a *App) LoadState() (uiState, error) {
	result, err := lifecycle.ResolveListResult(a.deps.store, a.deps.findProjectRoot, model.OpenParams{
		UseDefaultTenant:      true,
		UseDefaultEnvironment: true,
	})
	if err != nil {
		if errors.Is(err, config.ErrNotInitialized) {
			// No config is the same state as zero tenants. Tenants must be non-nil: the
			// frontend iterates it on boot and a nil slice marshals to null.
			info := a.deps.resolveBuildInfo()
			suggestions, notices := a.runtimeVersionSuggestions(info, "", "")
			return uiState{
				Tenants:                  []uiTenant{},
				Build:                    buildDetailsFrom(info),
				VersionSuggestions:       suggestions,
				VersionSuggestionNotices: notices,
			}, nil
		}
		if errors.Is(err, config.ErrConfigCorrupted) {
			// An unreadable config returns a degraded state, not err: it must not read as
			// "no tenants", and the yaml error must not reach the browser.
			info := a.deps.resolveBuildInfo()
			return uiState{
				Tenants:          []uiTenant{},
				Build:            buildDetailsFrom(info),
				ConfigUnreadable: true,
				Message:          "Some configuration could not be read. Run `erun doctor` to inspect and repair it, then reload.",
			}, nil
		}
		return uiState{}, err
	}
	info := a.deps.resolveBuildInfo()
	state := stateFromListResult(result, info)
	a.seedEnvironmentActivitySnapshots(&state)
	a.seedEnvironmentUsageSnapshots(&state)
	a.seedEnvironmentNodeSnapshots(&state, result)
	suggestionTenant := ""
	suggestionEnv := ""
	if state.Selected != nil {
		suggestionTenant = state.Selected.Tenant
		suggestionEnv = state.Selected.Environment
	} else if len(state.Tenants) > 0 {
		suggestionTenant = state.Tenants[0].Name
	}
	state.VersionSuggestions, state.VersionSuggestionNotices = a.runtimeVersionSuggestions(info, suggestionTenant, suggestionEnv)
	return state, nil
}

func (a *App) resolveRuntimeRegistryVersionsForTenant(namespace, tenant string) (model.RuntimeRegistryVersions, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if namespace = strings.TrimSpace(namespace); namespace == "" {
		namespace = config.DefaultContainerRegistry
	}
	repository := config.DefaultRuntimeImageName
	if tenant = strings.TrimSpace(tenant); tenant != "" {
		repository = config.RuntimeReleaseName(tenant)
	}
	return a.deps.resolveImageRegistry(ctx, namespace, repository)
}

// runtimeRegistryNamespace returns where `erun deploy` last pushed the runtime image.
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

func (a *App) runtimeVersionSuggestions(info model.BuildInfo, tenant, environment string) ([]uiVersion, []uiVersionNotice) {
	tenant = strings.TrimSpace(tenant)
	defaultImage := config.DefaultContainerRegistry + "/" + config.DefaultRuntimeImageName
	if tenant == "" {
		suggestions, notice := a.suggestionsForImage(info, "ERun", defaultImage, "", "")
		return suggestions, appendVersionNotice(nil, notice)
	}

	tenantImage := config.RuntimeReleaseName(tenant)
	suggestions := make([]uiVersion, 0, 8)
	notices := make([]uiVersionNotice, 0, 4)
	for _, registry := range a.environmentDiscoveryRegistries(tenant, environment) {
		image := strings.TrimRight(strings.TrimSpace(registry), "/") + "/" + tenantImage
		got, notice := a.suggestionsForImage(info, tenant, image, registry, tenant)
		suggestions = append(suggestions, got...)
		notices = appendVersionNotice(notices, notice)
	}
	// Tenant images are rebuilt from the canonical image, so its channel-latest is deployable too.
	if tenantImage != config.DefaultRuntimeImageName {
		got, notice := a.suggestionsForImage(info, "ERun", defaultImage, "", "")
		suggestions = append(suggestions, got...)
		notices = appendVersionNotice(notices, notice)
	}
	return suggestions, notices
}

// suggestionsForImage returns a notice rather than dropping the source when listing fails.
func (a *App) suggestionsForImage(info model.BuildInfo, source, image, namespace, tenant string) ([]uiVersion, *uiVersionNotice) {
	versions, err := a.resolveRuntimeRegistryVersionsForTenant(namespace, tenant)
	if err != nil {
		return nil, &uiVersionNotice{Image: image, Kind: versionNoticeKind(err)}
	}
	return labelRuntimeVersionSuggestions(source, image, registrypkg.RuntimeDeployVersionSuggestions(info, versions)), nil
}

func versionNoticeKind(err error) string {
	if errors.Is(err, registrypkg.ErrRegistryAuthRequired) {
		return "auth"
	}
	return "unreachable"
}

func appendVersionNotice(notices []uiVersionNotice, notice *uiVersionNotice) []uiVersionNotice {
	if notice == nil {
		return notices
	}
	for _, existing := range notices {
		if existing.Image == notice.Image && existing.Kind == notice.Kind {
			return notices
		}
	}
	return append(notices, *notice)
}

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
		for _, registry := range config.ResolveEnvironmentContainerRegistries(env).DistinctRegistries() {
			add(registry)
		}
	}
	if len(registries) == 0 {
		add(config.DefaultContainerRegistry)
	}
	return registries
}

func (a *App) lookupEnvConfig(tenant, environment string) (config.EnvConfig, bool) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" || a.deps.store == nil {
		return config.EnvConfig{}, false
	}
	envs, err := a.deps.store.ListEnvConfigs(tenant)
	if err != nil {
		return config.EnvConfig{}, false
	}
	for _, env := range envs {
		if strings.EqualFold(strings.TrimSpace(env.Name), environment) {
			return env, true
		}
	}
	return config.EnvConfig{}, false
}

func (a *App) LoadVersionSuggestions(selection uiSelection) (uiVersionSuggestions, error) {
	selection = normalizeSelection(selection)
	suggestions, notices := a.runtimeVersionSuggestions(a.deps.resolveBuildInfo(), selection.Tenant, selection.Environment)
	return uiVersionSuggestions{Suggestions: suggestions, Notices: notices}, nil
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

func stateFromListResult(result lifecycle.ListResult, info model.BuildInfo) uiState {
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
				Name:                     strings.TrimSpace(environment.Name),
				Type:                     strings.TrimSpace(string(environment.Type)),
				MCPURL:                   mcpEndpointForListEnvironment(environment),
				APIURL:                   strings.TrimSpace(environment.APIURL),
				RuntimeVersion:           strings.TrimSpace(environment.RuntimeVersion),
				RuntimeVersionLine:       environment.RuntimeVersionLine,
				ErunVersion:              environment.ErunVersion,
				RuntimeImageLineMismatch: environment.RuntimeImageLineMismatch,
				KubernetesContext:        strings.TrimSpace(environment.KubernetesContext),
				IsActive:                 environment.IsActive,
				SSHDEnabled:              environment.SSH.Enabled,
				AutoStart:                copyBoolPtr(environment.AutoStart),
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

func mcpEndpointForOpenResult(result config.OpenResult) string {
	return fmt.Sprintf("http://127.0.0.1:%d/mcp", config.MCPPortForResult(result))
}

func mcpEndpointForListEnvironment(environment lifecycle.ListEnvironmentResult) string {
	port := environment.LocalPorts.MCP
	if port <= 0 {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
}

func buildDetailsFrom(info model.BuildInfo) uiBuildDetails {
	return uiBuildDetails{
		Version: info.Version,
		Commit:  info.Commit,
		Date:    info.Date,
	}
}

func listKubernetesContexts() ([]string, error) {
	contextsCmd := exec.Command("kubectl", "config", "get-contexts", "-o=name")
	core.HideConsoleWindow(contextsCmd)
	output, err := contextsCmd.Output()
	if err != nil {
		return nil, wrapKubectlError(err)
	}
	contexts := strings.Split(string(output), "\n")

	currentCmd := exec.Command("kubectl", "config", "current-context")
	core.HideConsoleWindow(currentCmd)
	currentOutput, err := currentCmd.Output()
	if err == nil {
		contexts = preferCurrentKubernetesContext(contexts, string(currentOutput))
	}

	return contexts, nil
}

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

// errMissingTenantOrEnvironment returns nil when both are set, so it can be a caller's whole guard.
func errMissingTenantOrEnvironment(operation, tenant, environment string) error {
	var missing []string
	if strings.TrimSpace(tenant) == "" {
		missing = append(missing, "tenant")
	}
	if strings.TrimSpace(environment) == "" {
		missing = append(missing, "environment")
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(
		"%s: %s not set -- select an environment in the sidebar before retrying this action",
		operation, strings.Join(missing, " and "),
	)
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
