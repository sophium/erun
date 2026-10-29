package main

import (
	"context"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle/list"
	activitysvc "github.com/sophium/erun/erun-ui/internal/activity"
	"github.com/sophium/erun/erun-ui/internal/runtimeview"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// The runtime surface; the behavior lives in internal/runtimeview. Seeding the
// read model stays here because uiState is the shell's.

func (a *App) CheckEnvironmentHealth(selection uimodel.Selection) (runtimeview.Health, error) {
	return a.runtime.CheckEnvironmentHealth(selection)
}

func (a *App) EnvironmentWorkingIssue(selection uimodel.Selection) (runtimeview.WorkingIssue, error) {
	return a.runtime.EnvironmentWorkingIssue(selection)
}

func (a *App) LoadClusterRegistry(input runtimeview.ResourceInput) (runtimeview.ClusterRegistryStatus, error) {
	return a.runtime.LoadClusterRegistry(input)
}

func (a *App) LoadHostedRegistry() (runtimeview.HostedRegistryStatus, error) {
	return a.runtime.LoadHostedRegistry()
}

func (a *App) LoadRuntimeActivity(selection uimodel.Selection) (runtimeview.Activity, error) {
	return a.runtime.LoadRuntimeActivity(selection)
}

func (a *App) LoadRuntimeResourceStatus(input runtimeview.ResourceInput) (runtimeview.ResourceStatus, error) {
	return a.runtime.LoadRuntimeResourceStatus(input)
}

func (a *App) LoadRuntimeSizing(selection uimodel.Selection) (runtimeview.SizingRecommendation, error) {
	return a.runtime.LoadRuntimeSizing(selection)
}

func (a *App) LoadRuntimeUsage(selection uimodel.Selection) (runtimeview.Usage, error) {
	return a.runtime.LoadRuntimeUsage(selection)
}

func (a *App) ReclaimRuntimeResources(input runtimeview.ReclaimInput) (runtimeview.ReclaimResult, error) {
	return a.runtime.ReclaimRuntimeResources(input)
}

func (a *App) ResetEnvironmentActivityObservations() {
	a.runtime.ResetEnvironmentActivityObservations()
}

func (a *App) ResetEnvironmentUsageObservations() {
	a.runtime.ResetEnvironmentUsageObservations()
}

func (a *App) ResizeRuntimeToRecommendation(selection uimodel.Selection, overrideLease bool) (runtimeview.SizingRecommendation, error) {
	return a.runtime.ResizeRuntimeToRecommendation(selection, overrideLease)
}

func (a *App) TriggerEnvironmentActivitySweep() {
	a.runtime.TriggerEnvironmentActivitySweep()
}

// newRuntimeService reads the deps funcs and sibling services per call, so a
// test that builds App by hand or swaps a dep after NewApp still reaches its fake.
func (a *App) newRuntimeService() *runtimeview.Service {
	return runtimeview.New(runtimeview.Deps{
		Store:   a.deps.store,
		Context: a.context,
		Emit:    a.emit,
		ExecRuntimePod: func(ctx context.Context, selection uimodel.Selection, script string) (string, error) {
			return a.execInRuntimePod(ctx, selection, script)
		},
		LoadUsage: func(ctx context.Context, selection uimodel.Selection) (runtimeview.Usage, error) {
			return a.deps.loadRuntimeUsage(ctx, selection)
		},
		LoadResourceStatus: func(ctx context.Context, input runtimeview.ResourceInput) (runtimeview.ResourceStatus, error) {
			return a.deps.loadResourceStatus(ctx, input)
		},
		LoadClusterRegistry: func(ctx context.Context, input runtimeview.ResourceInput) (runtimeview.ClusterRegistryStatus, error) {
			return a.deps.loadClusterRegistry(ctx, input)
		},
		LoadHostedRegistry: func(ctx context.Context) runtimeview.HostedRegistryStatus {
			return a.deps.loadHostedRegistry(ctx)
		},
		CheckRuntimeDeployed: func(ctx context.Context, kubeContext, tenant, environment string) (bool, error) {
			return a.deps.checkRuntimeDeployed(ctx, kubeContext, tenant, environment)
		},
		ResolveImageRegistry: func(ctx context.Context, namespace, repository string) (model.RuntimeRegistryVersions, error) {
			return a.deps.resolveImageRegistry(ctx, namespace, repository)
		},
		ChartAvailabilityOverride: activitysvc.ChartAvailabilityOverride,
		CanConnectLocalPort:       func(port int) bool { return a.deps.canConnectLocalPort(port) },
		CanReachMCPEndpoint:       func(port int) bool { return a.deps.canReachMCPEndpoint(port) },
		MCPEndpoint:               mcpEndpointForOpenResult,
		MCPBearer:                 a.mcpBearer,
		LoadIdleStatus: func(ctx context.Context, endpoint, bearer string) (model.EnvironmentIdleStatus, error) {
			return a.deps.loadIdleStatus(ctx, endpoint, bearer)
		},
		LoadPodBranch: func(ctx context.Context, endpoint, bearer string) (string, error) {
			return a.deps.loadPodBranch(ctx, endpoint, bearer)
		},
		RunCommand: func(ctx context.Context, dir, name string, args ...string) (string, error) {
			return a.deps.runWorkingIssueCommand(ctx, dir, name, args...)
		},
		ForgetForwardRepair: func(selection uimodel.Selection) {
			a.envops.ForgetForwardRepair(selection)
		},
		ReconcileForwardHealth: func(selection uimodel.Selection, port int, portIsBound bool) bool {
			return a.envops.ReconcileForwardHealth(selection, port, portIsBound)
		},
		NoteEdgeAnswering: func(selection uimodel.Selection) { a.orchestrator.NoteEdgeAnswering(selection) },
		EffectiveContainerRegistries: func(tenant, environment string, env config.EnvConfig) (config.ContainerRegistries, bool) {
			return a.settings.EffectiveContainerRegistries(tenant, environment, env)
		},
		LinkedCloudContextFor: func(cloudProviderAlias, kubernetesContext string) (model.CloudContextStatus, bool, error) {
			return a.settings.LinkedCloudContextFor(cloudProviderAlias, kubernetesContext)
		},
		RuntimeRegistryNamespace: a.runtimeRegistryNamespace,
		UsageHistoryPath:         a.deps.environmentUsageHistoryPath,
	})
}

// seedEnvironmentActivitySnapshots lets a reload learn activity that will not
// re-emit until the next transition.
func (a *App) seedEnvironmentActivitySnapshots(state *uiState) {
	observed := a.runtime.ActivitySnapshot()
	for ti := range state.Tenants {
		tenant := &state.Tenants[ti]
		for ei := range tenant.Environments {
			env := &tenant.Environments[ei]
			if activity, ok := observed[uimodel.SelectionKey(uimodel.Selection{Tenant: tenant.Name, Environment: env.Name})]; ok {
				env.Activity = runtimeview.ActivitySnapshotFrom(activity)
			}
		}
	}
}

// seedEnvironmentUsageSnapshots attaches the last cached reading, persisted
// across restarts, so a hover card shows it with its true age.
func (a *App) seedEnvironmentUsageSnapshots(state *uiState) {
	readings := a.runtime.UsageSnapshot()
	for ti := range state.Tenants {
		tenant := &state.Tenants[ti]
		for ei := range tenant.Environments {
			env := &tenant.Environments[ei]
			if reading, ok := readings[uimodel.SelectionKey(uimodel.Selection{Tenant: tenant.Name, Environment: env.Name})]; ok {
				env.Usage = runtimeview.UsageSnapshotFrom(reading)
			}
		}
	}
}

// seedEnvironmentNodeSnapshots lets a reload see readings that only emit on transition.
func (a *App) seedEnvironmentNodeSnapshots(state *uiState, result list.ListResult) {
	links := cloudContextLinksByEnvironment(result)
	for ti := range state.Tenants {
		tenant := &state.Tenants[ti]
		for ei := range tenant.Environments {
			env := &tenant.Environments[ei]
			link, ok := links[uimodel.SelectionKey(uimodel.Selection{Tenant: tenant.Name, Environment: env.Name})]
			if !ok {
				continue
			}
			env.Node = a.runtime.NodeSnapshot(link.cloudProviderAlias, link.kubernetesContext)
		}
	}
}

type cloudContextLink struct {
	cloudProviderAlias string
	kubernetesContext  string
}

func cloudContextLinksByEnvironment(result list.ListResult) map[string]cloudContextLink {
	links := make(map[string]cloudContextLink)
	for _, tenant := range result.Tenants {
		for _, environment := range tenant.Environments {
			key := uimodel.SelectionKey(uimodel.Selection{
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
