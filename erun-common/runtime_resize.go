package eruncommon

import (
	"fmt"
	"strings"
	"time"
)

// A resize turns the standing sizing recommendation (runtime_sizing.go) into
// an action: it moves the runtime container's own resource *limits* — the
// throttle/OOM ceiling and the namespace ResourceQuota draw those limits count
// against — never the scheduler *request*, which erun pins to a small fixed
// default (runtime_resources.go) independent of RuntimePod. It reuses the
// existing deploy path (ResolveCurrentDeploySpecs/RunDeploySpecs) to roll the
// pod exactly once, the same way `erun open`'s drift redeploy and `erun
// upgrade` already do — a resize is not a new rollout mechanism, only a new
// way to decide the value that one already threads through.
//
// Because a resize restarts the runtime pod, it is the first mutating
// operation to consult the environment's activity leases before running: any
// held lease (build, deploy, or an agent session) is evidence of a worker this
// resize would interrupt, so it refuses unless explicitly overridden — the
// same predicate the desktop's own AI-session spawn already uses
// (erun-ui/terminal_sessions.go's aiSessionOccupants).

// RuntimeResizeInput is what a resize request resolves from: either an
// explicit CPU/memory pair (merged onto the current value, so naming only one
// leaves the other where it was) or a request to size from the environment's
// own standing recommendation.
type RuntimeResizeInput struct {
	CPU                 string
	Memory              string
	ApplyRecommendation bool
}

// RuntimeResizeAction is one resource's resolved change.
type RuntimeResizeAction struct {
	Resource string `json:"resource"`
	From     string `json:"from"`
	To       string `json:"to"`
}

// RuntimeResizePlan is the resolved, side-effect-free outcome of a resize
// request. NoOp is true when the resolved target equals the current recorded
// size, so a caller reports "already sized" rather than rolling a pod for
// nothing.
type RuntimeResizePlan struct {
	Tenant      string
	Environment string
	Current     RuntimePodResources
	Target      RuntimePodResources
	Actions     []RuntimeResizeAction
	NoOp        bool
}

// ResolveRuntimeResizePlan is pure: it never reads a lease, writes config, or
// deploys. Given the environment's current recorded size, its namespace
// ceiling, and the caller's request, it resolves the target size and
// validates it against the namespace quota (a ResourceQuota counts every
// container in the pod, so the erun-dind sidecar's own limit is spent before
// the runtime container gets anything — the same accounting
// boundRuntimeMemorySuggestion already applies to a recommendation's own
// suggested value).
func ResolveRuntimeResizePlan(tenant, environment string, current RuntimePodResources, ceiling NamespaceResourceQuota, input RuntimeResizeInput, recommendation *RuntimeSizingRecommendation) (RuntimeResizePlan, error) {
	current = NormalizeRuntimePodResources(current)
	explicitCPU := strings.TrimSpace(input.CPU)
	explicitMemory := strings.TrimSpace(input.Memory)
	if err := validateRuntimeResizeInputSelection(input.ApplyRecommendation, explicitCPU, explicitMemory); err != nil {
		return RuntimeResizePlan{}, err
	}

	var target RuntimePodResources
	var err error
	if input.ApplyRecommendation {
		target, err = applyRuntimeSizingRecommendation(tenant, environment, current, recommendation)
	} else {
		target = applyExplicitRuntimeResize(current, explicitCPU, explicitMemory)
	}
	if err != nil {
		return RuntimeResizePlan{}, err
	}

	target = NormalizeRuntimePodResources(target)
	if err := ValidateRuntimePodResources(target); err != nil {
		return RuntimeResizePlan{}, fmt.Errorf("resize: %w", err)
	}
	if err := validateRuntimeResizeAgainstQuota(target, ceiling); err != nil {
		return RuntimeResizePlan{}, err
	}

	actions := diffRuntimeResizeActions(current, target)
	return RuntimeResizePlan{
		Tenant:      tenant,
		Environment: environment,
		Current:     current,
		Target:      target,
		Actions:     actions,
		NoOp:        len(actions) == 0,
	}, nil
}

// validateRuntimeResizeInputSelection enforces that a resize request names
// exactly one of "size from the standing recommendation" or "size to these
// explicit values" — never both, never neither.
func validateRuntimeResizeInputSelection(applyRecommendation bool, explicitCPU, explicitMemory string) error {
	if applyRecommendation && (explicitCPU != "" || explicitMemory != "") {
		return fmt.Errorf("resize: pass either apply-recommendation or explicit cpu/memory values, not both")
	}
	if !applyRecommendation && explicitCPU == "" && explicitMemory == "" {
		return fmt.Errorf("resize: pass cpu and/or memory, or apply-recommendation to size from the environment's standing recommendation")
	}
	return nil
}

// applyRuntimeSizingRecommendation resolves the target size from the
// environment's standing recommendation: every resource with a raise/lower
// verdict moves to its suggested value, everything else stays at its current
// size.
func applyRuntimeSizingRecommendation(tenant, environment string, current RuntimePodResources, recommendation *RuntimeSizingRecommendation) (RuntimePodResources, error) {
	if recommendation == nil {
		return RuntimePodResources{}, fmt.Errorf("resize: no standing recommendation is available for %s/%s yet — it needs retained usage history, which is only readable from inside the environment's own runtime pod (its resize tool over MCP), not from the host; run `erun usage` a few times first, or resize with explicit --cpu/--memory instead", tenant, environment)
	}
	target := current
	for _, verdict := range recommendation.Verdicts {
		if verdict.Action != RuntimeSizingRaise && verdict.Action != RuntimeSizingLower {
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

// applyExplicitRuntimeResize resolves the target size from the caller's
// explicit CPU/memory: naming only one leaves the other at its current value.
func applyExplicitRuntimeResize(current RuntimePodResources, explicitCPU, explicitMemory string) RuntimePodResources {
	target := current
	if explicitCPU != "" {
		target.CPU = explicitCPU
	}
	if explicitMemory != "" {
		target.Memory = explicitMemory
	}
	return target
}

// diffRuntimeResizeActions reports which resources actually change between
// the current and resolved target size.
func diffRuntimeResizeActions(current, target RuntimePodResources) []RuntimeResizeAction {
	var actions []RuntimeResizeAction
	if target.CPU != current.CPU {
		actions = append(actions, RuntimeResizeAction{Resource: "cpu", From: current.CPU, To: target.CPU})
	}
	if target.Memory != current.Memory {
		actions = append(actions, RuntimeResizeAction{Resource: "memory", From: current.Memory, To: target.Memory})
	}
	return actions
}

// validateRuntimeResizeAgainstQuota refuses a target the namespace
// ResourceQuota could never admit, naming the resource and the overage —
// mirroring the shape of the hosted platform's own quota 409s (root
// AGENTS.md's "same question, same shape" precedent) rather than letting the
// operator discover it as a stuck helm rollout.
func validateRuntimeResizeAgainstQuota(target RuntimePodResources, ceiling NamespaceResourceQuota) error {
	if ceiling.IsZero() {
		return nil
	}
	if quotaCPUMilli, err := ParseKubernetesCPUToMilli(ceiling.CPU); err == nil {
		dindCPUMilli, _ := ParseKubernetesCPUToMilli(DefaultRuntimeDindCPU)
		if targetCPUMilli, tErr := ParseKubernetesCPUToMilli(target.CPU); tErr == nil && targetCPUMilli+dindCPUMilli > quotaCPUMilli {
			available := quotaCPUMilli - dindCPUMilli
			if available < 0 {
				available = 0
			}
			return fmt.Errorf("resize: %s CPU plus the erun-dind sidecar's %s would exceed the namespace quota of %s CPU (%s available for the runtime container) — lower --cpu or raise the namespace quota with `erun deploy --max-cpu`", target.CPU, DefaultRuntimeDindCPU, ceiling.CPU, FormatKubernetesCPUFromMilli(available))
		}
	}
	if quotaMemMi, err := ParseKubernetesMemoryToMi(ceiling.Memory); err == nil {
		dindMemMi, _ := ParseKubernetesMemoryToMi(DefaultRuntimeDindMemory)
		if targetMemMi, tErr := ParseKubernetesMemoryToMi(target.Memory); tErr == nil && targetMemMi+dindMemMi > quotaMemMi {
			available := quotaMemMi - dindMemMi
			if available < 0 {
				available = 0
			}
			return fmt.Errorf("resize: %s memory plus the erun-dind sidecar's %s would exceed the namespace quota of %s memory (%dMi available for the runtime container) — lower --memory or raise the namespace quota with `erun deploy --max-memory`", target.Memory, DefaultRuntimeDindMemory, ceiling.Memory, available)
		}
	}
	return nil
}

// RuntimeResizeOccupancyError is returned when a resize is refused because the
// environment is not idle: a resize rolls the runtime pod (Recreate strategy),
// which would kill any live session inside it, so it is refused, naming every
// holder, unless the caller explicitly overrides it.
type RuntimeResizeOccupancyError struct {
	Holders []EnvironmentActivityLease
}

func (e *RuntimeResizeOccupancyError) Error() string {
	names := make([]string, 0, len(e.Holders))
	for _, lease := range e.Holders {
		names = append(names, fmt.Sprintf("%s (lease %q)", lease.Holder.String(), lease.Name))
	}
	return fmt.Sprintf("resize refused: this environment is held by %s — a resize restarts the runtime pod and would interrupt that work; pass the override to resize anyway, or wait until it finishes", strings.Join(names, "; "))
}

// checkRuntimeResizeOccupancy loads every currently held lease (plain and
// exclusive alike — the same predicate erun-ui's aiSessionOccupants already
// uses to decide whether starting a second agent is a foreign occupancy) and
// refuses unless the caller overrides. It does not itself claim anything: the
// caller takes its own exclusive lease afterward, which is what actually
// guards against a second, concurrent resize racing this one.
func checkRuntimeResizeOccupancy(tenant, environment string, now time.Time, override bool) ([]EnvironmentActivityLease, error) {
	leases, err := LoadEnvironmentActivityLeases(tenant, environment, now)
	if err != nil {
		return nil, fmt.Errorf("resize: reading activity leases: %w", err)
	}
	if len(leases) == 0 || override {
		return leases, nil
	}
	return nil, &RuntimeResizeOccupancyError{Holders: leases}
}

// RuntimeResizeParams is the resize operation's whole input, shared by both
// transports.
type RuntimeResizeParams struct {
	Tenant        string
	Environment   string
	Input         RuntimeResizeInput
	OverrideLease bool
	Holder        EnvironmentActivityLeaseHolder
}

// RuntimeResizeDependencies mirrors the shape `erun deploy` already injects
// (ResolveCurrentDeploySpecs/RunDeploySpecs's own parameters), so resize
// reuses that exact composition instead of a second one.
type RuntimeResizeDependencies struct {
	Store                          DeployStore
	SaveEnvConfig                  EnvConfigSaver
	FindProjectRoot                ProjectFinderFunc
	ResolveDockerBuildContext      BuildContextResolverFunc
	ResolveKubernetesDeployContext DeployContextResolverFunc
	Now                            NowFunc
	DeployHelmChart                HelmChartDeployerFunc
}

// RuntimeResizeResult reports what a resize did (or would do), for both
// transports to render.
type RuntimeResizeResult struct {
	Plan             RuntimeResizePlan
	OverriddenLeases []EnvironmentActivityLease
}

// RunRuntimeResize resolves the target size, checks occupancy, and — outside
// dry-run — persists EnvConfig.RuntimePod and rolls the runtime pod through
// the same deploy composition `erun deploy` uses. Dry-run traces every
// decision (current/target per resource, held leases, whether an override was
// needed) and performs no write, per the repository's dry-run contract.
func RunRuntimeResize(ctx Context, deps RuntimeResizeDependencies, params RuntimeResizeParams) (RuntimeResizeResult, error) {
	target, err := ResolveOpen(deps.Store, OpenParams{
		Tenant:                strings.TrimSpace(params.Tenant),
		Environment:           strings.TrimSpace(params.Environment),
		UseDefaultTenant:      strings.TrimSpace(params.Tenant) == "",
		UseDefaultEnvironment: strings.TrimSpace(params.Environment) == "",
	})
	if err != nil {
		return RuntimeResizeResult{}, err
	}
	tenant, environment := target.Tenant, target.Environment

	recommendation := listEnvironmentSizing(tenant, target.EnvConfig)
	plan, err := ResolveRuntimeResizePlan(tenant, environment, target.EnvConfig.RuntimePod, target.EnvConfig.NamespaceQuota, params.Input, recommendation)
	if err != nil {
		return RuntimeResizeResult{}, err
	}
	if plan.NoOp {
		ctx.Trace(fmt.Sprintf("resize: %s/%s is already sized at cpu=%s memory=%s; no change", tenant, environment, plan.Current.CPU, plan.Current.Memory))
		return RuntimeResizeResult{Plan: plan}, nil
	}
	traceRuntimeResizePlan(ctx, tenant, environment, plan)

	now := time.Now()
	if deps.Now != nil {
		now = deps.Now()
	}
	leases, err := checkRuntimeResizeOccupancy(tenant, environment, now, params.OverrideLease)
	if err != nil {
		return RuntimeResizeResult{}, err
	}
	traceRuntimeResizeOccupancyOverride(ctx, tenant, environment, leases)

	ctx.TraceCommand("", "resize", tenant, environment, "cpu="+plan.Target.CPU, "memory="+plan.Target.Memory)
	ctx.Trace(fmt.Sprintf("resize: %s/%s will roll the runtime pod once to apply the new limits", tenant, environment))
	if ctx.DryRun {
		return RuntimeResizeResult{Plan: plan, OverriddenLeases: leases}, nil
	}

	if err := rollRuntimeResize(ctx, deps, target, tenant, environment, plan, params.Holder, now); err != nil {
		return RuntimeResizeResult{}, err
	}
	return RuntimeResizeResult{Plan: plan, OverriddenLeases: leases}, nil
}

// traceRuntimeResizePlan emits the per-resource change lines and the standing
// "what this does and does not touch" explanation, for a plan already known
// not to be a no-op.
func traceRuntimeResizePlan(ctx Context, tenant, environment string, plan RuntimeResizePlan) {
	for _, action := range plan.Actions {
		ctx.Trace(fmt.Sprintf("resize: %s/%s %s %s -> %s", tenant, environment, action.Resource, action.From, action.To))
	}
	ctx.Trace(fmt.Sprintf("resize: %s/%s this moves the runtime container's throttle/OOM ceiling and its namespace quota draw; it does not change what the scheduler reserves (a fixed request independent of runtimepod) and it does not resize the erun-dind sidecar or any PVC", tenant, environment))
}

// traceRuntimeResizeOccupancyOverride traces which held leases a resize is
// about to interrupt; a no-op when nothing is held.
func traceRuntimeResizeOccupancyOverride(ctx Context, tenant, environment string, leases []EnvironmentActivityLease) {
	if len(leases) == 0 {
		return
	}
	holders := make([]string, 0, len(leases))
	for _, lease := range leases {
		holders = append(holders, fmt.Sprintf("%s (lease %q)", lease.Holder.String(), lease.Name))
	}
	ctx.Trace(fmt.Sprintf("resize: %s/%s overriding %d held lease(s): %s", tenant, environment, len(leases), strings.Join(holders, "; ")))
}

// rollRuntimeResize is RunRuntimeResize's real-run half: take the exclusive
// lease, persist the new size, and roll the runtime pod through the same
// deploy composition `erun deploy` uses.
func rollRuntimeResize(ctx Context, deps RuntimeResizeDependencies, target OpenResult, tenant, environment string, plan RuntimeResizePlan, holder EnvironmentActivityLeaseHolder, now time.Time) error {
	lease, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant: tenant, Environment: environment, Name: "resize", Exclusive: true, Holder: holder, Now: now,
	})
	if err != nil {
		return fmt.Errorf("resize: %w", err)
	}
	defer func() {
		_ = ReleaseExclusiveEnvironmentActivityLease(tenant, environment, lease.Scope, lease.ID)
	}()

	updatedConfig := target.EnvConfig
	updatedConfig.RuntimePod = plan.Target
	if err := deps.SaveEnvConfig(tenant, updatedConfig); err != nil {
		return fmt.Errorf("resize: saving the new runtime pod size: %w", err)
	}

	specs, err := ResolveCurrentDeploySpecs(ctx, deps.Store, deps.FindProjectRoot, deps.ResolveDockerBuildContext, deps.ResolveKubernetesDeployContext, deps.Now, DeployTarget{
		Tenant:      tenant,
		Environment: environment,
		RepoPath:    target.RepoPath,
	})
	if err != nil {
		return err
	}
	if err := RunDeploySpecs(ctx, specs, deps.DeployHelmChart); err != nil {
		return err
	}
	return PersistRuntimeVersionFromDeploySpecs(ctx, specs, deps.SaveEnvConfig, ResolveDeployedHelmReleaseVersion)
}
