package main

import (
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// The node an environment's cluster runs on is already measured and cached by
// the cloud-context poller (cloud_context_cache.go), and reading it costs one
// in-memory map lookup. This file is the fan-out from that per-context cache to
// the per-environment read model the sidebar renders, so a row whose own state
// cannot be determined can still say the one thing that is known about it.
//
// Two states this must never collapse into each other: a node that is stopped
// (start it) and a node whose reading is missing or stale (nothing has been
// established). The cache reports the first as "stopped" and the second as ""
// or "unknown"; both survive to the frontend verbatim.

// environmentNodeSnapshot resolves the node backing one environment. A nil
// result is the definite "no node erun manages", not a failed read: an env with
// no kubernetes context, or one whose context is not a managed cloud context,
// has nothing to say about a node and must render nothing rather than "stopped".
func (a *App) environmentNodeSnapshot(cloudProviderAlias, kubernetesContext string) *uiEnvironmentNodeSnapshot {
	context, ok, err := a.linkedCloudContextFor(cloudProviderAlias, kubernetesContext)
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

// seedEnvironmentNodeSnapshots attaches each environment's node reading to the
// initial-state read model, for the same reason seedEnvironmentActivitySnapshots
// exists: the env-node event only fires on a transition, so a boot from a clean
// store has nothing to replay and would render every row as if no node had ever
// been observed.
func (a *App) seedEnvironmentNodeSnapshots(state *uiState, result eruncommon.ListResult) {
	links := cloudContextLinksByEnvironment(result)
	for ti := range state.Tenants {
		tenant := &state.Tenants[ti]
		for ei := range tenant.Environments {
			env := &tenant.Environments[ei]
			link, ok := links[selectionKey(uiSelection{Tenant: tenant.Name, Environment: env.Name})]
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

func cloudContextLinksByEnvironment(result eruncommon.ListResult) map[string]cloudContextLink {
	links := make(map[string]cloudContextLink)
	for _, tenant := range result.Tenants {
		for _, environment := range tenant.Environments {
			key := selectionKey(uiSelection{
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

// refreshEnvironmentNodeStatuses republishes every environment whose node
// reading changed since the last pass. Called after the cloud-context cache is
// written — by the poller's own tick and by the handlers that start or stop a
// node, so an operator's action is reflected without waiting out a poll
// interval. Every read here is a store or in-memory read; nothing reaches the
// cloud.
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
			selection := uiSelection{
				Tenant:      strings.TrimSpace(tenant.Name),
				Environment: strings.TrimSpace(env.Name),
			}
			a.emitEnvNodeIfChanged(selection, a.environmentNodeSnapshot(env.CloudProviderAlias, env.KubernetesContext))
		}
	}
}

// emitEnvNodeIfChanged keeps an unchanged reading off the event bridge, the
// same discipline emitEnvActivityIfChanged applies: this sweep runs on every
// poll tick and most ticks observe nothing new.
func (a *App) emitEnvNodeIfChanged(selection uiSelection, node *uiEnvironmentNodeSnapshot) {
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
	a.emitEvent(envNodeEvent, envNodePayload{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
		Node:        node,
	})
}
