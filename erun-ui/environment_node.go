package main

import (
	"strings"

	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// A stopped node and a missing or stale reading ("" or "unknown") must never
// collapse into each other; both reach the frontend verbatim.

// environmentNodeSnapshot returns nil when no erun-managed node backs the env,
// which must render nothing rather than "stopped".
func (a *App) environmentNodeSnapshot(cloudProviderAlias, kubernetesContext string) *uiEnvironmentNodeSnapshot {
	context, ok, err := a.settings.LinkedCloudContextFor(cloudProviderAlias, kubernetesContext)
	if err != nil || !ok {
		return nil
	}
	name := strings.TrimSpace(context.Name)
	if name == "" {
		return nil
	}
	return &uiEnvironmentNodeSnapshot{
		Name:   name,
		Label:  cloudContextDisplayName(context),
		Status: strings.TrimSpace(context.Status),
	}
}

// seedEnvironmentNodeSnapshots lets a reload see readings that only emit on transition.
func (a *App) seedEnvironmentNodeSnapshots(state *uiState, result lifecycle.ListResult) {
	links := cloudContextLinksByEnvironment(result)
	for ti := range state.Tenants {
		tenant := &state.Tenants[ti]
		for ei := range tenant.Environments {
			env := &tenant.Environments[ei]
			link, ok := links[selectionKey(uimodel.Selection{Tenant: tenant.Name, Environment: env.Name})]
			if !ok {
				continue
			}
			env.Node = a.environmentNodeSnapshot(link.cloudProviderAlias, link.kubernetesContext)
		}
	}
}

type cloudContextLink struct {
	cloudProviderAlias string
	kubernetesContext  string
}

func cloudContextLinksByEnvironment(result lifecycle.ListResult) map[string]cloudContextLink {
	links := make(map[string]cloudContextLink)
	for _, tenant := range result.Tenants {
		for _, environment := range tenant.Environments {
			key := selectionKey(uimodel.Selection{
				Tenant:      strings.TrimSpace(tenant.Name),
				Environment: strings.TrimSpace(environment.Name),
			})
			links[key] = cloudContextLink{
				cloudProviderAlias: environment.CloudProviderAlias,
				kubernetesContext:  environment.KubernetesContext,
			}
		}
	}
	return links
}

// refreshEnvironmentNodeStatuses republishes changed node readings; it reads only
// local state, never the cloud.
func (a *App) refreshEnvironmentNodeStatuses() {
	if a.deps.store == nil {
		return
	}
	tenants, err := a.deps.store.ListTenantConfigs()
	if err != nil {
		return
	}
	for _, tenant := range tenants {
		envs, err := a.deps.store.ListEnvConfigs(tenant.Name)
		if err != nil {
			continue
		}
		for _, env := range envs {
			selection := uimodel.Selection{
				Tenant:      strings.TrimSpace(tenant.Name),
				Environment: strings.TrimSpace(env.Name),
			}
			a.emitEnvNodeIfChanged(selection, a.environmentNodeSnapshot(env.CloudProviderAlias, env.KubernetesContext))
		}
	}
}

func (a *App) emitEnvNodeIfChanged(selection uimodel.Selection, node *uiEnvironmentNodeSnapshot) {
	key := selectionKey(selection)
	var current uiEnvironmentNodeSnapshot
	if node != nil {
		current = *node
	}
	a.envNodesMu.Lock()
	previous, seen := a.envNodes[key]
	unchanged := seen && previous == current
	if !unchanged {
		if a.envNodes == nil {
			a.envNodes = make(map[string]uiEnvironmentNodeSnapshot)
		}
		a.envNodes[key] = current
	}
	a.envNodesMu.Unlock()
	if unchanged {
		return
	}
	a.emitEvent(uimodel.EnvNodeEvent, envNodePayload{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
		Node:        node,
	})
}
