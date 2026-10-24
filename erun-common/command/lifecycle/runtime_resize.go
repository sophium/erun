package lifecycle

import (
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/open"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// A resize moves the runtime and dind containers' limits, never the scheduler request, and rolls
// the pod once through the normal deploy path. Since it restarts the pod, any held activity
// lease refuses it unless overridden.

// RuntimeResizeInput is explicit values merged onto the current size, or the standing
// recommendation; the dind sidecar has no recommendation path.
type RuntimeResizeInput struct {
	CPU                 string
	Memory              string
	DindCPU             string
	DindMemory          string
	ApplyRecommendation bool
}

// RuntimeResizeAction is one resource's resolved change.
type RuntimeResizeAction struct {
	Resource string `json:"resource"`
	From     string `json:"from"`
	To       string `json:"to"`
}

// RuntimeResizePlan is the resolved outcome of a resize; NoOp means already sized.
type RuntimeResizePlan struct {
	Tenant      string
	Environment string
	Current     config.RuntimePodResources
	Target      config.RuntimePodResources
	DindCurrent config.RuntimePodResources
	DindTarget  config.RuntimePodResources
	Actions     []RuntimeResizeAction
	NoOp        bool
}

// apply-recommendation sizes only the runtime pod, so it combines freely with dind values.
func validateRuntimeResizeInput(input RuntimeResizeInput, explicitCPU, explicitMemory, explicitDindCPU, explicitDindMemory string) error {
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
func liveRuntimePodResources(configured config.RuntimePodResources, recommendation *deploy.RuntimeSizingRecommendation) config.RuntimePodResources {
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

func resolveRuntimeResizeTarget(tenant, environment string, current config.RuntimePodResources, input RuntimeResizeInput, recommendation *deploy.RuntimeSizingRecommendation, explicitCPU, explicitMemory string) (config.RuntimePodResources, error) {
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
		return config.RuntimePodResources{}, fmt.Errorf("resize: no standing recommendation is available for %s/%s yet — it needs retained usage history, which is only readable from inside the environment's own runtime pod (its resize tool over MCP), not from the host; run `erun usage` a few times first, or resize with explicit --cpu/--memory instead", tenant, environment)
	}
	for _, verdict := range recommendation.Verdicts {
		if verdict.Action != deploy.RuntimeSizingRaise && verdict.Action != deploy.RuntimeSizingLower {
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

func runtimeResizeActions(current, target config.RuntimePodResources) []RuntimeResizeAction {
	var actions []RuntimeResizeAction
	if target.CPU != current.CPU {
		actions = append(actions, RuntimeResizeAction{Resource: "cpu", From: current.CPU, To: target.CPU})
	}
	if target.Memory != current.Memory {
		actions = append(actions, RuntimeResizeAction{Resource: "memory", From: current.Memory, To: target.Memory})
	}
	return actions
}

// No recommendation path for the sidecar: usage history comes from the erun-devops container's
// own cgroups, and the sidecar's are not reachable from there.
func resolveRuntimeDindResizeTarget(current config.RuntimePodResources, explicitDindCPU, explicitDindMemory string) config.RuntimePodResources {
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
func runtimeDindResizeActions(current, target config.RuntimePodResources) []RuntimeResizeAction {
	var actions []RuntimeResizeAction
	if target.CPU != current.CPU {
		actions = append(actions, RuntimeResizeAction{Resource: "dind-cpu", From: current.CPU, To: target.CPU})
	}
	if target.Memory != current.Memory {
		actions = append(actions, RuntimeResizeAction{Resource: "dind-memory", From: current.Memory, To: target.Memory})
	}
	return actions
}

// ResolveRuntimeResizePlan resolves and quota-checks a resize target without side effects.
func ResolveRuntimeResizePlan(tenant, environment string, current, currentDind config.RuntimePodResources, ceiling config.NamespaceResourceQuota, input RuntimeResizeInput, recommendation *deploy.RuntimeSizingRecommendation) (RuntimeResizePlan, error) {
	current = config.NormalizeRuntimePodResources(liveRuntimePodResources(current, recommendation))
	currentDind = config.NormalizeRuntimeDindPodResources(currentDind)
	explicitCPU := strings.TrimSpace(input.CPU)
	explicitMemory := strings.TrimSpace(input.Memory)
	explicitDindCPU := strings.TrimSpace(input.DindCPU)
	explicitDindMemory := strings.TrimSpace(input.DindMemory)

	if err := validateRuntimeResizeInput(input, explicitCPU, explicitMemory, explicitDindCPU, explicitDindMemory); err != nil {
		return RuntimeResizePlan{}, err
	}

	target, err := resolveRuntimeResizeTarget(tenant, environment, current, input, recommendation, explicitCPU, explicitMemory)
	if err != nil {
		return RuntimeResizePlan{}, err
	}
	target = config.NormalizeRuntimePodResources(target)
	if err := config.ValidateRuntimePodResources(target); err != nil {
		return RuntimeResizePlan{}, fmt.Errorf("resize: %w", err)
	}

	dindTarget := config.NormalizeRuntimeDindPodResources(resolveRuntimeDindResizeTarget(currentDind, explicitDindCPU, explicitDindMemory))
	if err := config.ValidateRuntimeDindPodResources(dindTarget); err != nil {
		return RuntimeResizePlan{}, fmt.Errorf("resize: %w", err)
	}

	if err := validateRuntimeResizeAgainstQuota(target, dindTarget, ceiling); err != nil {
		return RuntimeResizePlan{}, err
	}

	actions := append(runtimeResizeActions(current, target), runtimeDindResizeActions(currentDind, dindTarget)...)

	return RuntimeResizePlan{
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
func validateRuntimeResizeAgainstQuota(target, dindTarget config.RuntimePodResources, ceiling config.NamespaceResourceQuota) error {
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
	Holders []activity.EnvironmentActivityLease
}

func (e *RuntimeResizeOccupancyError) Error() string {
	return fmt.Sprintf("resize refused: this environment is held by %s — a resize restarts the runtime pod and would interrupt that work; pass the override to resize anyway, or wait until it finishes", activity.FormatLeaseHolders(e.Holders))
}

// load must read the environment's own lease store: a host-side CLI reading its local
// filesystem always sees zero leases. The caller's own exclusive lease guards concurrent resizes.
func checkRuntimeResizeOccupancy(load func(tenant, environment string, now time.Time) ([]activity.EnvironmentActivityLease, error), tenant, environment string, now time.Time, override bool) ([]activity.EnvironmentActivityLease, error) {
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

// RuntimeResizeParams is the resize input shared by both transports.
type RuntimeResizeParams struct {
	Tenant        string
	Environment   string
	Input         RuntimeResizeInput
	OverrideLease bool
	Holder        activity.EnvironmentActivityLeaseHolder
}

// RuntimeResizeDependencies reuses the composition `erun deploy` injects.
type RuntimeResizeDependencies struct {
	Store                          deploy.DeployStore
	SaveEnvConfig                  deploy.EnvConfigSaver
	FindProjectRoot                shared.ProjectFinderFunc
	ResolveDockerBuildContext      shared.BuildContextResolverFunc
	ResolveKubernetesDeployContext deploy.DeployContextResolverFunc
	Now                            shared.NowFunc
	DeployHelmChart                deploy.HelmChartDeployerFunc
	// LoadActivityLeases nil reads the local store, correct only inside the environment's pod.
	LoadActivityLeases func(tenant, environment string, now time.Time) ([]activity.EnvironmentActivityLease, error)
}

// RuntimeResizeResult reports what a resize did or would do.
type RuntimeResizeResult struct {
	Plan             RuntimeResizePlan
	OverriddenLeases []activity.EnvironmentActivityLease
}

func traceRuntimeResizePlanActions(ctx core.Context, tenant, environment string, plan RuntimeResizePlan) {
	for _, action := range plan.Actions {
		ctx.Trace(fmt.Sprintf("resize: %s/%s %s %s -> %s", tenant, environment, action.Resource, action.From, action.To))
	}
	ctx.Trace(fmt.Sprintf("resize: %s/%s this moves the runtime container's throttle/OOM ceiling (and the erun-dind sidecar's, when --dind-cpu/--dind-memory are set) and their namespace quota draw; it does not change what the scheduler reserves (a fixed request independent of runtimepod/runtimedindpod) and it does not resize any PVC", tenant, environment))
}

// Shows the recommendation resize acts on, so a no-op or held resource is explainable.
func traceRuntimeSizingRecommendation(ctx core.Context, tenant, environment string, recommendation *deploy.RuntimeSizingRecommendation) {
	if recommendation == nil {
		return
	}
	for _, verdict := range recommendation.Verdicts {
		ctx.Trace(fmt.Sprintf("resize: %s/%s sizing: %s", tenant, environment, deploy.FormatRuntimeSizingVerdict(verdict)))
	}
	ctx.Trace(fmt.Sprintf("resize: %s/%s sizing-evidence: %s", tenant, environment, deploy.FormatRuntimeSizingEvidence(*recommendation)))
}

func traceRuntimeResizeOverriddenLeases(ctx core.Context, tenant, environment string, leases []activity.EnvironmentActivityLease) {
	if len(leases) == 0 {
		return
	}
	ctx.Trace(fmt.Sprintf("resize: %s/%s overriding %d held lease(s): %s", tenant, environment, len(leases), activity.FormatLeaseHolders(leases)))
}

func applyRuntimeResize(ctx core.Context, deps RuntimeResizeDependencies, tenant, environment string, target config.OpenResult, plan RuntimeResizePlan) error {
	updatedConfig := target.EnvConfig
	updatedConfig.RuntimePod = plan.Target
	updatedConfig.RuntimeDindPod = plan.DindTarget
	if err := deps.SaveEnvConfig(tenant, updatedConfig); err != nil {
		return fmt.Errorf("resize: saving the new runtime pod size: %w", err)
	}

	specs, err := deploy.ResolveCurrentDeploySpecs(ctx, deps.Store, deps.FindProjectRoot, deps.ResolveDockerBuildContext, deps.ResolveKubernetesDeployContext, deps.Now, deploy.DeployTarget{
		Tenant:      tenant,
		Environment: environment,
		RepoPath:    target.RepoPath,
	})
	if err != nil {
		return err
	}
	if err := deploy.RunDeploySpecs(ctx, specs, deps.DeployHelmChart); err != nil {
		return err
	}
	return deploy.PersistRuntimeVersionFromDeploySpecs(ctx, specs, deps.SaveEnvConfig, deploy.ResolveDeployedHelmReleaseVersion)
}

// RunRuntimeResize checks occupancy and, outside dry-run, persists the sizing and rolls the pod once.
func RunRuntimeResize(ctx core.Context, deps RuntimeResizeDependencies, params RuntimeResizeParams) (RuntimeResizeResult, error) {
	target, err := open.ResolveOpen(deps.Store, shared.OpenParams{
		Tenant:                    strings.TrimSpace(params.Tenant),
		Environment:               strings.TrimSpace(params.Environment),
		UseDefaultTenant:          strings.TrimSpace(params.Tenant) == "",
		UseDefaultEnvironment:     strings.TrimSpace(params.Environment) == "",
		Command:                   ctx.Command,
		CommandScopesTenantByFlag: ctx.CommandScopesTenantByFlag,
	})
	if err != nil {
		return RuntimeResizeResult{}, err
	}
	tenant, environment := target.Tenant, target.Environment

	recommendation := EnvironmentRuntimeSizing(tenant, target.EnvConfig)
	traceRuntimeSizingRecommendation(ctx, tenant, environment, recommendation)
	plan, err := ResolveRuntimeResizePlan(tenant, environment, target.EnvConfig.RuntimePod, target.EnvConfig.RuntimeDindPod, target.EnvConfig.NamespaceQuota, params.Input, recommendation)
	if err != nil {
		return RuntimeResizeResult{}, err
	}
	if plan.NoOp {
		ctx.Trace(fmt.Sprintf("resize: %s/%s is already sized at cpu=%s memory=%s dind-cpu=%s dind-memory=%s; no change", tenant, environment, plan.Current.CPU, plan.Current.Memory, plan.DindCurrent.CPU, plan.DindCurrent.Memory))
		return RuntimeResizeResult{Plan: plan}, nil
	}
	traceRuntimeResizePlanActions(ctx, tenant, environment, plan)

	now := time.Now()
	if deps.Now != nil {
		now = deps.Now()
	}
	leases, err := checkRuntimeResizeOccupancy(deps.LoadActivityLeases, tenant, environment, now, params.OverrideLease)
	if err != nil {
		return RuntimeResizeResult{}, err
	}
	traceRuntimeResizeOverriddenLeases(ctx, tenant, environment, leases)

	ctx.TraceCommand("", "resize", tenant, environment, "cpu="+plan.Target.CPU, "memory="+plan.Target.Memory, "dind-cpu="+plan.DindTarget.CPU, "dind-memory="+plan.DindTarget.Memory)
	ctx.Trace(fmt.Sprintf("resize: %s/%s will roll the runtime pod once to apply the new limits", tenant, environment))
	if ctx.DryRun {
		return RuntimeResizeResult{Plan: plan, OverriddenLeases: leases}, nil
	}

	lease, err := activity.TakeEnvironmentActivityLease(activity.TakeEnvironmentActivityLeaseParams{
		Tenant: tenant, Environment: environment, Name: "resize", Exclusive: true, Holder: params.Holder, Now: now,
	})
	if err != nil {
		return RuntimeResizeResult{}, fmt.Errorf("resize: %w", err)
	}
	defer func() {
		_, _ = activity.ReleaseExclusiveEnvironmentActivityLease(tenant, environment, lease.Scope, lease.ID)
	}()

	if err := applyRuntimeResize(ctx, deps, tenant, environment, target, plan); err != nil {
		return RuntimeResizeResult{}, err
	}

	return RuntimeResizeResult{Plan: plan, OverriddenLeases: leases}, nil
}
