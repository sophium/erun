package main

import (
	"context"
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/open"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func (a *App) saveRemoteCloudAlias(selection uiSelection, existing, updated configpkg.EnvConfig) error {
	if !existing.RemoteWorktree() || strings.TrimSpace(updated.CloudProviderAlias) == strings.TrimSpace(existing.CloudProviderAlias) {
		return nil
	}
	result, err := open.ResolveOpen(a.deps.store, shared.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := a.ensureMCPAvailable(ctx, result); err != nil {
		return err
	}
	bearer := a.mcpBearer(selection.Tenant, selection.Environment)
	_, err = a.deps.setRemoteCloudAlias(ctx, mcpEndpointForOpenResult(result), bearer, selection.Tenant, selection.Environment, updated.CloudProviderAlias)
	return err
}

// environmentCloudProviderTypes is the render order for cloud-alias slots, AWS
// first as the legacy primary. Adding a type here gives it its own env selector
// and sidebar login row.
var environmentCloudProviderTypes = []string{
	configpkg.CloudProviderAWS,
	configpkg.CloudProviderCloudflare,
}

// environmentCloudAliasSlots builds the per-provider-type cloud-alias slots for
// one env. Two rules shape it: omit a type that has no configured aliases and
// nothing attached, so a new env shows no empty selector before any token of
// that type exists; and keep a currently-attached alias whose provider config
// was deleted as an option, so the operator can still see and clear it
// (recognition over recall).
func environmentCloudAliasSlots(store configpkg.CloudReadStore, config configpkg.EnvConfig) []uiEnvironmentCloudAlias {
	optionsByType := configuredCloudAliasesByType(store)
	attached := config.ResolvedCloudAliases()
	slots := make([]uiEnvironmentCloudAlias, 0, len(environmentCloudProviderTypes))
	for _, providerType := range environmentCloudProviderTypes {
		options := append([]string(nil), optionsByType[providerType]...)
		current := strings.TrimSpace(attached[providerType])
		if current != "" && !cloudAliasListContains(options, current) {
			options = append([]string{current}, options...)
		}
		if current == "" && len(options) == 0 {
			continue
		}
		slots = append(slots, uiEnvironmentCloudAlias{
			Provider: providerType,
			Alias:    current,
			Options:  options,
		})
	}
	return slots
}

func configuredCloudAliasesByType(store configpkg.CloudReadStore) map[string][]string {
	grouped := make(map[string][]string)
	providers, err := cloud.ListCloudProviders(store)
	if err != nil {
		return grouped
	}
	for _, provider := range providers {
		alias := strings.TrimSpace(provider.Alias)
		providerType := strings.ToLower(strings.TrimSpace(provider.Provider))
		if alias == "" || providerType == "" {
			continue
		}
		if cloudAliasListContains(grouped[providerType], alias) {
			continue
		}
		grouped[providerType] = append(grouped[providerType], alias)
	}
	return grouped
}

func cloudAliasListContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func environmentCloudProviderAliases(store configpkg.CloudReadStore, current string) []string {
	providers, err := cloud.ListCloudProviders(store)
	if err != nil {
		return nil
	}
	current = strings.TrimSpace(current)
	aliases := make([]string, 0, len(providers)+1)
	seen := make(map[string]struct{}, len(providers)+1)
	for _, provider := range providers {
		alias := strings.TrimSpace(provider.Alias)
		if alias == "" {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		aliases = append(aliases, alias)
		seen[alias] = struct{}{}
	}
	if current != "" {
		if _, ok := seen[current]; !ok {
			aliases = append([]string{current}, aliases...)
		}
	}
	return aliases
}

// applyEnvironmentCloudAliasSlots writes the edited cloud aliases back onto the
// env config. The AWS alias stays in the legacy CloudProviderAlias scalar so
// every existing AWS reader is unchanged; other types live in the per-type
// CloudProviderAliases map. An empty slot value clears that type's attachment.
// When the UI sends no slots (the AWS-only single-selector path), the legacy
// scalar is applied directly to preserve older-caller behavior.
func applyEnvironmentCloudAliasSlots(existing *configpkg.EnvConfig, config uiEnvironmentConfig) {
	if len(config.CloudAliasSlots) == 0 {
		existing.CloudProviderAlias = strings.TrimSpace(config.CloudProviderAlias)
		return
	}
	for _, slot := range config.CloudAliasSlots {
		providerType := strings.ToLower(strings.TrimSpace(slot.Provider))
		alias := strings.TrimSpace(slot.Alias)
		if providerType == "" || providerType == configpkg.CloudProviderAWS {
			existing.CloudProviderAlias = alias
			continue
		}
		if alias == "" {
			delete(existing.CloudProviderAliases, providerType)
			continue
		}
		if existing.CloudProviderAliases == nil {
			existing.CloudProviderAliases = make(map[string]string)
		}
		existing.CloudProviderAliases[providerType] = alias
	}
}

// linkedCloudContext returns the cloud context backing the env, with Status
// filled from the background poller's in-memory cache. The persisted config no
// longer carries Status, so a missing cache entry surfaces as Status="" that
// callers must treat as "not yet observed," not "stopped."
func (a *App) linkedCloudContext(config configpkg.EnvConfig) (configpkg.CloudContextStatus, bool, error) {
	return a.linkedCloudContextFor(config.CloudProviderAlias, config.KubernetesContext)
}

// linkedCloudContextFor is the same resolution keyed on the two fields it
// actually reads, for read-model callers that hold a list result rather than a
// full EnvConfig.
func (a *App) linkedCloudContextFor(cloudProviderAlias, kubernetesContext string) (configpkg.CloudContextStatus, bool, error) {
	cloudProviderAlias = strings.TrimSpace(cloudProviderAlias)
	kubernetesContext = strings.TrimSpace(kubernetesContext)
	if kubernetesContext == "" {
		return configpkg.CloudContextStatus{}, false, nil
	}
	statuses, err := cloud.ListCloudContextStatuses(a.deps.store)
	if err != nil {
		return configpkg.CloudContextStatus{}, false, err
	}
	for _, status := range statuses {
		context := configpkg.NormalizeCloudContextConfig(status.CloudContextConfig)
		if cloudProviderAlias != "" && strings.TrimSpace(context.CloudProviderAlias) != cloudProviderAlias {
			continue
		}
		if strings.TrimSpace(context.KubernetesContext) == kubernetesContext || strings.TrimSpace(context.Name) == kubernetesContext {
			status.CloudContextConfig = context
			status.Status = a.cloudContextStatus(context.Name)
			return status, true, nil
		}
	}
	return configpkg.CloudContextStatus{}, false, nil
}

func (a *App) ensureLinkedCloudContextRunning(config configpkg.EnvConfig) (configpkg.CloudContextStatus, bool, error) {
	status, ok, err := a.linkedCloudContext(config)
	if err != nil || !ok {
		return status, ok, err
	}
	if strings.TrimSpace(status.Status) == cloud.CloudContextStatusRunning {
		a.emitAppStatus(fmt.Sprintf("Cloud context %s is running. Opening environment...", cloudContextDisplayName(status)))
		return status, true, nil
	}
	a.emitAppStatus(fmt.Sprintf("Starting cloud context %s and waiting for Kubernetes access...", cloudContextDisplayName(status)))
	status, err = cloud.StartCloudContext(core.Context{}, a.deps.store, cloud.CloudContextParams{Name: status.Name}, a.deps.cloudContextDeps)
	if err != nil {
		return configpkg.CloudContextStatus{}, true, err
	}
	a.clearIdleStopsForCloudContext(status.Name)
	a.setCloudContextStatusInCache(status.Name, status.Status)
	a.emitAppStatus(fmt.Sprintf("Cloud context %s is running. Opening environment...", cloudContextDisplayName(status)))
	return status, true, nil
}

func (a *App) stopCloudContext(name string) (configpkg.CloudContextStatus, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	status, err := a.deps.stopCloudContext(ctx, name)
	if err != nil {
		return status, err
	}
	a.setCloudContextStatusInCache(status.Name, status.Status)
	return status, nil
}
