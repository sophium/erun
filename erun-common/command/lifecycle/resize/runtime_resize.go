package resize

import (
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/command/lifecycle/list"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/deploy/run"
	"github.com/sophium/erun/erun-common/primitive/deploy/runtimeusage"
	"github.com/sophium/erun/erun-common/primitive/deploy/runtimeversion"
	"github.com/sophium/erun/erun-common/primitive/deploy/spec"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
)

// apply-recommendation sizes only the runtime pod, so it combines freely with dind values.
func validateRuntimeResizeInput(input model.RuntimeResizeInput, explicitCPU, explicitMemory, explicitDindCPU, explicitDindMemory string) error {
	if input.ApplyRecommendation && (explicitCPU != "" || explicitMemory != "") {
		return fmt.Errorf("resize: pass either apply-recommendation or explicit cpu/memory values, not both")
	}
	if !input.ApplyRecommendation && explicitCPU == "" && explicitMemory == "" && explicitDindCPU == "" && explicitDindMemory == "" {
		return fmt.Errorf("resize: pass cpu/memory and/or dind-cpu/dind-memory, or apply-recommendation to size the runtime pod from the environment's standing recommendation")
	}
	return nil
}

// The in-pod config carries no runtimepod when silent, so prefer live cgroup limits; otherwise a
// held resource would be reset to package defaults.
func liveRuntimePodResources(configured model.RuntimePodResources, recommendation *model.RuntimeSizingRecommendation) model.RuntimePodResources {
	if recommendation == nil {
		return configured
	}
	live := configured
	for _, verdict := range recommendation.Verdicts {
		if verdict.Current == "" {
			continue
		}
		switch verdict.Resource {
		case "cpu":
			live.CPU = verdict.Current
		case "memory":
			live.Memory = verdict.Current
		}
	}
	return live
}

func resolveRuntimeResizeTarget(tenant, environment string, current model.RuntimePodResources, input model.RuntimeResizeInput, recommendation *model.RuntimeSizingRecommendation, explicitCPU, explicitMemory string) (model.RuntimePodResources, error) {
	target := current
	if !input.ApplyRecommendation {
		if explicitCPU != "" {
			target.CPU = explicitCPU
		}
		if explicitMemory != "" {
			target.Memory = explicitMemory
		}
		return target, nil
	}
	if recommendation == nil {
		return model.RuntimePodResources{}, fmt.Errorf("resize: no standing recommendation is available for %s/%s yet — it needs retained usage history, which is only readable from inside the environment's own runtime pod (its resize tool over MCP), not from the host; run `erun usage` a few times first, or resize with explicit --cpu/--memory instead", tenant, environment)
	}
	for _, verdict := range recommendation.Verdicts {
		if verdict.Action != model.RuntimeSizingRaise && verdict.Action != model.RuntimeSizingLower {
			continue
		}
		switch verdict.Resource {
		case "cpu":
			target.CPU = verdict.Suggested
		case "memory":
			target.Memory = verdict.Suggested
		}
	}
	return target, nil
}

func runtimeResizeActions(current, target model.RuntimePodResources) []model.RuntimeResizeAction {
	var actions []model.RuntimeResizeAction
	if target.CPU != current.CPU {
		actions = append(actions, model.RuntimeResizeAction{Resource: "cpu", From: current.CPU, To: target.CPU})
	}
	if target.Memory != current.Memory {
		actions = append(actions, model.RuntimeResizeAction{Resource: "memory", From: current.Memory, To: target.Memory})
	}
	return actions
}

// No recommendation path for the sidecar: usage history comes from the erun-devops container's
// own cgroups, and the sidecar's are not reachable from there.
func resolveRuntimeDindResizeTarget(current model.RuntimePodResources, explicitDindCPU, explicitDindMemory string) model.RuntimePodResources {
	target := current
	if explicitDindCPU != "" {
		target.CPU = explicitDindCPU
	}
	if explicitDindMemory != "" {
		target.Memory = explicitDindMemory
	}
	return target
}

// Distinct resource names keep sidecar changes apart from runtime-container ones.
func runtimeDindResizeActions(current, target model.RuntimePodResources) []model.RuntimeResizeAction {
	var actions []model.RuntimeResizeAction
	if target.CPU != current.CPU {
		actions = append(actions, model.RuntimeResizeAction{Resource: "dind-cpu", From: current.CPU, To: target.CPU})
	}
	if target.Memory != current.Memory {
		actions = append(actions, model.RuntimeResizeAction{Resource: "dind-memory", From: current.Memory, To: target.Memory})
	}
	return actions
}

// ResolveRuntimeResizePlan resolves and quota-checks a resize target without side effects.
func ResolveRuntimeResizePlan(tenant, environment string, current, currentDind model.RuntimePodResources, ceiling model.NamespaceResourceQuota, input model.RuntimeResizeInput, recommendation *model.RuntimeSizingRecommendation) (model.RuntimeResizePlan, error) {
	current = config.NormalizeRuntimePodResources(liveRuntimePodResources(current, recommendation))
	currentDind = config.NormalizeRuntimeDindPodResources(currentDind)
	explicitCPU := strings.TrimSpace(input.CPU)
	explicitMemory := strings.TrimSpace(input.Memory)
	explicitDindCPU := strings.TrimSpace(input.DindCPU)
	explicitDindMemory := strings.TrimSpace(input.DindMemory)

	if err := validateRuntimeResizeInput(input, explicitCPU, explicitMemory, explicitDindCPU, explicitDindMemory); err != nil {
		return model.RuntimeResizePlan{}, err
	}

	target, err := resolveRuntimeResizeTarget(tenant, environment, current, input, recommendation, explicitCPU, explicitMemory)
	if err != nil {
		return model.RuntimeResizePlan{}, err
	}
	target = config.NormalizeRuntimePodResources(target)
	if err := config.ValidateRuntimePodResources(target); err != nil {
		return model.RuntimeResizePlan{}, fmt.Errorf("resize: %w", err)
	}

	dindTarget := config.NormalizeRuntimeDindPodResources(resolveRuntimeDindResizeTarget(currentDind, explicitDindCPU, explicitDindMemory))
	if err := config.ValidateRuntimeDindPodResources(dindTarget); err != nil {
		return model.RuntimeResizePlan{}, fmt.Errorf("resize: %w", err)
	}

	if err := validateRuntimeResizeAgainstQuota(target, dindTarget, ceiling); err != nil {
		return model.RuntimeResizePlan{}, err
	}

	actions := append(runtimeResizeActions(current, target), runtimeDindResizeActions(currentDind, dindTarget)...)

	return model.RuntimeResizePlan{
		Tenant:      tenant,
		Environment: environment,
		Current:     current,
		Target:      target,
		DindCurrent: currentDind,
		DindTarget:  dindTarget,
		Actions:     actions,
		NoOp:        len(actions) == 0,
	}, nil
}

// A ResourceQuota counts every container, so the sidecar's target is spent first.
func validateRuntimeResizeAgainstQuota(target, dindTarget model.RuntimePodResources, ceiling model.NamespaceResourceQuota) error {
	if ceiling.IsZero() {
		return nil
	}
	if quotaCPUMilli, err := config.ParseKubernetesCPUToMilli(ceiling.CPU); err == nil {
		dindCPUMilli, _ := config.ParseKubernetesCPUToMilli(dindTarget.CPU)
		if targetCPUMilli, tErr := config.ParseKubernetesCPUToMilli(target.CPU); tErr == nil && targetCPUMilli+dindCPUMilli > quotaCPUMilli {
			available := quotaCPUMilli - dindCPUMilli
			if available < 0 {
				available = 0
			}
			return fmt.Errorf("resize: %s CPU plus the erun-dind sidecar's %s would exceed the namespace quota of %s CPU (%s available for the runtime container) — lower --cpu/--dind-cpu or raise the namespace quota with `erun deploy --max-cpu`", target.CPU, dindTarget.CPU, ceiling.CPU, formatAvailableCPUMilli(available))
		}
	}
	if quotaMemMi, err := config.ParseKubernetesMemoryToMi(ceiling.Memory); err == nil {
		dindMemMi, _ := config.ParseKubernetesMemoryToMi(dindTarget.Memory)
		if targetMemMi, tErr := config.ParseKubernetesMemoryToMi(target.Memory); tErr == nil && targetMemMi+dindMemMi > quotaMemMi {
			available := quotaMemMi - dindMemMi
			if available < 0 {
				available = 0
			}
			return fmt.Errorf("resize: %s memory plus the erun-dind sidecar's %s would exceed the namespace quota of %s memory (%dMi available for the runtime container) — lower --memory/--dind-memory or raise the namespace quota with `erun deploy --max-memory`", target.Memory, dindTarget.Memory, ceiling.Memory, available)
		}
	}
	return nil
}

// FormatKubernetesCPUFromMilli renders zero as "", which would drop the figure here.
func formatAvailableCPUMilli(milli int64) string {
	if milli <= 0 {
		return "0"
	}
	return config.FormatKubernetesCPUFromMilli(milli)
}

// RuntimeResizeOccupancyError refuses a resize while leases are held, since the pod roll kills sessions.
type RuntimeResizeOccupancyError struct {
	Holders []model.EnvironmentActivityLease
}

func (e *RuntimeResizeOccupancyError) Error() string {
	return fmt.Sprintf("resize refused: this environment is held by %s — a resize restarts the runtime pod and would interrupt that work; pass the override to resize anyway, or wait until it finishes", activity.FormatLeaseHolders(e.Holders))
}

// load must read the environment's own lease store: a host-side CLI reading its local
// filesystem always sees zero leases. The caller's own exclusive lease guards concurrent resizes.
func checkRuntimeResizeOccupancy(load func(tenant, environment string, now time.Time) ([]model.EnvironmentActivityLease, error), tenant, environment string, now time.Time, override bool) ([]model.EnvironmentActivityLease, error) {
	if load == nil {
		load = activity.LoadEnvironmentActivityLeases
	}
	leases, err := load(tenant, environment, now)
	if err != nil {
		return nil, fmt.Errorf("resize: reading activity leases: %w", err)
	}
	if len(leases) == 0 || override {
		return leases, nil
	}
	return nil, &RuntimeResizeOccupancyError{Holders: leases}
}

// RuntimeResizeDependencies reuses the composition `erun deploy` injects.
type RuntimeResizeDependencies struct {
	Store                          seam.DeployStore
	SaveEnvConfig                  seam.EnvConfigSaver
	FindProjectRoot                seam.ProjectFinderFunc
	ResolveDockerBuildContext      seam.BuildContextResolverFunc
	ResolveKubernetesDeployContext seam.DeployContextResolverFunc
	Now                            seam.NowFunc
	DeployHelmChart                seam.HelmChartDeployerFunc
	// LoadActivityLeases nil reads the local store, correct only inside the environment's pod.
	LoadActivityLeases func(tenant, environment string, now time.Time) ([]model.EnvironmentActivityLease, error)
}

func traceRuntimeResizePlanActions(ctx core.Context, tenant, environment string, plan model.RuntimeResizePlan) {
	for _, action := range plan.Actions {
		ctx.Trace(fmt.Sprintf("resize: %s/%s %s %s -> %s", tenant, environment, action.Resource, action.From, action.To))
	}
	ctx.Trace(fmt.Sprintf("resize: %s/%s this moves the runtime container's throttle/OOM ceiling (and the erun-dind sidecar's, when --dind-cpu/--dind-memory are set) and their namespace quota draw; it does not change what the scheduler reserves (a fixed request independent of runtimepod/runtimedindpod) and it does not resize any PVC", tenant, environment))
}

// Shows the recommendation resize acts on, so a no-op or held resource is explainable.
func traceRuntimeSizingRecommendation(ctx core.Context, tenant, environment string, recommendation *model.RuntimeSizingRecommendation) {
	if recommendation == nil {
		return
	}
	for _, verdict := range recommendation.Verdicts {
		ctx.Trace(fmt.Sprintf("resize: %s/%s sizing: %s", tenant, environment, runtimeusage.FormatRuntimeSizingVerdict(verdict)))
	}
	ctx.Trace(fmt.Sprintf("resize: %s/%s sizing-evidence: %s", tenant, environment, runtimeusage.FormatRuntimeSizingEvidence(*recommendation)))
}

func traceRuntimeResizeOverriddenLeases(ctx core.Context, tenant, environment string, leases []model.EnvironmentActivityLease) {
	if len(leases) == 0 {
		return
	}
	ctx.Trace(fmt.Sprintf("resize: %s/%s overriding %d held lease(s): %s", tenant, environment, len(leases), activity.FormatLeaseHolders(leases)))
}

func applyRuntimeResize(ctx core.Context, deps RuntimeResizeDependencies, tenant, environment string, target config.OpenResult, plan model.RuntimeResizePlan) error {
	updatedConfig := target.EnvConfig
	updatedConfig.RuntimePod = plan.Target
	updatedConfig.RuntimeDindPod = plan.DindTarget
	if err := deps.SaveEnvConfig(tenant, updatedConfig); err != nil {
		return fmt.Errorf("resize: saving the new runtime pod size: %w", err)
	}

	specs, err := spec.ResolveCurrentDeploySpecs(ctx, deps.Store, deps.FindProjectRoot, deps.ResolveDockerBuildContext, deps.ResolveKubernetesDeployContext, deps.Now, model.DeployTarget{
		Tenant:      tenant,
		Environment: environment,
		RepoPath:    target.RepoPath,
	})
	if err != nil {
		return err
	}
	if err := run.RunDeploySpecs(ctx, specs, deps.DeployHelmChart); err != nil {
		return err
	}
	return runtimeversion.PersistRuntimeVersionFromDeploySpecs(ctx, specs, deps.SaveEnvConfig, runtimeversion.ResolveDeployedHelmReleaseVersion)
}

// RunRuntimeResize checks occupancy and, outside dry-run, persists the sizing and rolls the pod once.
func RunRuntimeResize(ctx core.Context, deps RuntimeResizeDependencies, params model.RuntimeResizeParams) (model.RuntimeResizeResult, error) {
	target, err := resolve.ResolveOpen(deps.Store, model.OpenParams{
		Tenant:                    strings.TrimSpace(params.Tenant),
		Environment:               strings.TrimSpace(params.Environment),
		UseDefaultTenant:          strings.TrimSpace(params.Tenant) == "",
		UseDefaultEnvironment:     strings.TrimSpace(params.Environment) == "",
		Command:                   ctx.Command,
		CommandScopesTenantByFlag: ctx.CommandScopesTenantByFlag,
	})
	if err != nil {
		return model.RuntimeResizeResult{}, err
	}
	tenant, environment := target.Tenant, target.Environment

	recommendation := list.EnvironmentRuntimeSizing(tenant, target.EnvConfig)
	traceRuntimeSizingRecommendation(ctx, tenant, environment, recommendation)
	plan, err := ResolveRuntimeResizePlan(tenant, environment, target.EnvConfig.RuntimePod, target.EnvConfig.RuntimeDindPod, target.EnvConfig.NamespaceQuota, params.Input, recommendation)
	if err != nil {
		return model.RuntimeResizeResult{}, err
	}
	if plan.NoOp {
		ctx.Trace(fmt.Sprintf("resize: %s/%s is already sized at cpu=%s memory=%s dind-cpu=%s dind-memory=%s; no change", tenant, environment, plan.Current.CPU, plan.Current.Memory, plan.DindCurrent.CPU, plan.DindCurrent.Memory))
		return model.RuntimeResizeResult{Plan: plan}, nil
	}
	traceRuntimeResizePlanActions(ctx, tenant, environment, plan)

	now := time.Now()
	if deps.Now != nil {
		now = deps.Now()
	}
	leases, err := checkRuntimeResizeOccupancy(deps.LoadActivityLeases, tenant, environment, now, params.OverrideLease)
	if err != nil {
		return model.RuntimeResizeResult{}, err
	}
	traceRuntimeResizeOverriddenLeases(ctx, tenant, environment, leases)

	ctx.TraceCommand("", "resize", tenant, environment, "cpu="+plan.Target.CPU, "memory="+plan.Target.Memory, "dind-cpu="+plan.DindTarget.CPU, "dind-memory="+plan.DindTarget.Memory)
	ctx.Trace(fmt.Sprintf("resize: %s/%s will roll the runtime pod once to apply the new limits", tenant, environment))
	if ctx.DryRun {
		return model.RuntimeResizeResult{Plan: plan, OverriddenLeases: leases}, nil
	}

	lease, err := activity.TakeEnvironmentActivityLease(activity.TakeEnvironmentActivityLeaseParams{
		Tenant: tenant, Environment: environment, Name: "resize", Exclusive: true, Holder: params.Holder, Now: now,
	})
	if err != nil {
		return model.RuntimeResizeResult{}, fmt.Errorf("resize: %w", err)
	}
	defer func() {
		_, _ = activity.ReleaseExclusiveEnvironmentActivityLease(tenant, environment, lease.Scope, lease.ID)
	}()

	if err := applyRuntimeResize(ctx, deps, tenant, environment, target, plan); err != nil {
		return model.RuntimeResizeResult{}, err
	}

	return model.RuntimeResizeResult{Plan: plan, OverriddenLeases: leases}, nil
}
