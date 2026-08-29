package eruncommon

import (
	"strings"
	"time"
)

// EnvironmentLifecycleState is the environment read model's single resolved
// answer to "what is this environment doing right now?" -- composed from
// cloud-context power state, deploy health, and idle-policy eligibility,
// each of which alone only answers a narrower question.
type EnvironmentLifecycleState string

const (
	EnvironmentLifecycleRunning      EnvironmentLifecycleState = "running"
	EnvironmentLifecycleIdle         EnvironmentLifecycleState = "idle"
	EnvironmentLifecycleDeployFailed EnvironmentLifecycleState = "deploy-failed"
	EnvironmentLifecycleStopped      EnvironmentLifecycleState = "stopped"
	// EnvironmentLifecycleUnknown is reported whenever a signal the state
	// depends on was never observed -- e.g. a managed-cloud environment's
	// power state is a live AWS reading this package never persists, so a
	// caller that has not refreshed it sees a blank value here rather than a
	// guess at "stopped" or "running".
	EnvironmentLifecycleUnknown EnvironmentLifecycleState = "unknown"
)

// EnvironmentLifecycleInputs is the already-resolved signal set
// ResolveEnvironmentLifecycleState composes into one state. Every signal
// that can fail to be observed carries its own explicit "observed" bit, so a
// caller that could not read a signal says so instead of leaving a zero
// value indistinguishable from a real negative reading.
type EnvironmentLifecycleInputs struct {
	ManagedCloud bool

	// CloudContextObserved is true only when a live cloud-context power
	// state was actually read. A config-only match (the environment names a
	// cloud context) is not an observation: CloudContextStatus is never
	// persisted (see CloudContextStatus's own doc comment), so a caller that
	// never refreshed it has nothing real to report.
	CloudContextObserved bool
	CloudContextStatus   string

	// DeployHealthObserved is true only when a real (non-dry-run) deploy
	// diagnosis ran for this environment.
	DeployHealthObserved bool
	DeployHealthy        bool

	// IdleStatusObserved is true only when the environment's own idle policy
	// resolved successfully.
	IdleStatusObserved bool
	StopEligible       bool
}

// ResolveEnvironmentLifecycleState is pure: it never reaches for a signal it
// wasn't given, so it never has to guess at one. See the "observed" contract
// on EnvironmentLifecycleInputs -- an input whose observed bit is false is
// never used to distinguish Stopped or Idle from Unknown.
func ResolveEnvironmentLifecycleState(in EnvironmentLifecycleInputs) EnvironmentLifecycleState {
	if in.ManagedCloud {
		if state, resolved := resolveManagedCloudLifecycleState(in); resolved {
			return state
		}
		// Confirmed running: fall through to the shared workload checks
		// below, since a running instance can still be deploy-failed or
		// idle-eligible.
	}
	return resolveWorkloadLifecycleState(in)
}

// resolveManagedCloudLifecycleState resolves a managed-cloud environment's
// power state. resolved is false only when the instance is confirmed
// running, the one case the caller must still apply the shared workload
// checks for.
func resolveManagedCloudLifecycleState(in EnvironmentLifecycleInputs) (state EnvironmentLifecycleState, resolved bool) {
	if !in.CloudContextObserved {
		return EnvironmentLifecycleUnknown, true
	}
	switch in.CloudContextStatus {
	case CloudContextStatusStopped:
		return EnvironmentLifecycleStopped, true
	case CloudContextStatusRunning:
		return "", false
	default:
		// Pending, unknown, or any unrecognized value: the power state is
		// mid-transition or was never resolved past a guess.
		return EnvironmentLifecycleUnknown, true
	}
}

// resolveWorkloadLifecycleState resolves everything a cloud-managed
// environment's power state does not answer on its own: deploy health and
// idle-policy eligibility. It is also the whole answer for a non-managed
// environment, which has no separate power state to check first.
func resolveWorkloadLifecycleState(in EnvironmentLifecycleInputs) EnvironmentLifecycleState {
	switch {
	case in.DeployHealthObserved && !in.DeployHealthy:
		return EnvironmentLifecycleDeployFailed
	case in.IdleStatusObserved && in.StopEligible:
		return EnvironmentLifecycleIdle
	case in.IdleStatusObserved || in.DeployHealthObserved || in.ManagedCloud:
		return EnvironmentLifecycleRunning
	default:
		// Nothing at all was observed -- neither the cloud context, the
		// deploy health, nor the idle policy. Reporting Running here would
		// assert the environment is alive purely because nobody said
		// otherwise.
		return EnvironmentLifecycleUnknown
	}
}

// EnvironmentHealth is the read-only doctor-derived health view: the root
// config gate plus the current deploy diagnosis, with the single recovery
// action RecommendedDeployRecovery would suggest layered on top so a caller
// does not need to re-run that classification itself.
type EnvironmentHealth struct {
	RootConfig          RootConfigInspection  `json:"rootConfig"`
	Deploy              DeployDiagnosisResult `json:"deploy"`
	RecommendedRecovery DeployRecoveryAction  `json:"recommendedRecovery,omitempty"`
}

// ResolveEnvironmentHealth composes the doctor pieces callers already have
// (RootConfigInspection and DeployDiagnosisResult) into one health view. It
// runs no commands of its own; the caller is responsible for having run a
// real diagnosis -- see EnvironmentHealth's doc comment.
func ResolveEnvironmentHealth(rootConfig RootConfigInspection, deploy DeployDiagnosisResult) EnvironmentHealth {
	recovery, _ := RecommendedDeployRecovery(deploy)
	return EnvironmentHealth{RootConfig: rootConfig, Deploy: deploy, RecommendedRecovery: recovery}
}

// EnvironmentReadModel is the transport-neutral "what is this environment
// doing" answer: an environment's list-style summary plus the deeper detail
// -- cloud-context state, idle policy + activity snapshot, doctor health --
// with a single resolved lifecycle state layered on top. Every piece here
// reuses an existing resolver (ResolveListResult's per-environment entry,
// EnvironmentIdleStatus, CloudContextStatus, doctor's diagnosis); this type
// only composes them.
type EnvironmentReadModel struct {
	Tenant       string                    `json:"tenant"`
	Environment  ListEnvironmentResult     `json:"environment"`
	State        EnvironmentLifecycleState `json:"state"`
	CloudContext *CloudContextStatus       `json:"cloudContext,omitempty"`
	Idle         *EnvironmentIdleStatus    `json:"idle,omitempty"`
	Health       *EnvironmentHealth        `json:"health,omitempty"`
}

// AssembleEnvironmentReadModel is pure: it composes already-resolved pieces
// into one read model and derives the lifecycle state from exactly what was
// passed in. A nil cloudContext/idle/health is not an error -- it means that
// signal was not observed, and ResolveEnvironmentLifecycleState treats it
// that way rather than defaulting it to a real reading.
func AssembleEnvironmentReadModel(tenant string, environment ListEnvironmentResult, cloudContext *CloudContextStatus, idle *EnvironmentIdleStatus, health *EnvironmentHealth) EnvironmentReadModel {
	inputs := EnvironmentLifecycleInputs{}
	if idle != nil {
		inputs.ManagedCloud = idle.ManagedCloud
		inputs.IdleStatusObserved = true
		inputs.StopEligible = idle.StopEligible
	}
	if cloudContext != nil {
		if status := strings.TrimSpace(cloudContext.Status); status != "" {
			inputs.CloudContextObserved = true
			inputs.CloudContextStatus = status
		}
	}
	if health != nil {
		inputs.DeployHealthObserved = true
		_, needsRecovery := RecommendedDeployRecovery(health.Deploy)
		inputs.DeployHealthy = !needsRecovery
	}
	return EnvironmentReadModel{
		Tenant:       strings.TrimSpace(tenant),
		Environment:  environment,
		State:        ResolveEnvironmentLifecycleState(inputs),
		CloudContext: cloudContext,
		Idle:         idle,
		Health:       health,
	}
}

// ResolveEnvironmentDetail resolves the same per-environment entry
// ResolveListResult computes for every environment in a tenant, for exactly
// one named tenant/environment -- the "environment detail" half of the read
// model, as opposed to "tenant environment list" which ResolveListResult
// already covers unmodified.
func ResolveEnvironmentDetail(store ListStore, tenant, environment string) (ListEnvironmentResult, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := errMissingTenantOrEnvironment("resolve environment detail", tenant, environment); err != nil {
		return ListEnvironmentResult{}, err
	}
	tenantConfig, _, err := store.LoadTenantConfig(tenant)
	if err != nil {
		return ListEnvironmentResult{}, err
	}
	envConfig, _, err := store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return ListEnvironmentResult{}, err
	}
	effective, effectiveErr := ResolveOpen(store, OpenParams{Tenant: tenant, Environment: environment})
	portAllocations, err := ResolveAllEnvironmentLocalPorts(store)
	if err != nil {
		return ListEnvironmentResult{}, err
	}
	return listEnvironmentResult(store, tenantConfig, envConfig, effective, effectiveErr, portAllocations), nil
}

// ResolveEnvironmentReadModel is the read model's one impure entrypoint: it
// resolves the environment detail, its idle status, its cloud-context
// config (never a live power-state refresh -- see CloudContextObserved's
// doc comment), and, on a real (non-dry-run) call, a doctor deploy
// diagnosis, then assembles them. Every piece is an existing resolver; this
// function only calls them and hands the results to AssembleEnvironmentReadModel.
func ResolveEnvironmentReadModel(ctx Context, store ListStore, tenant, environment string, now time.Time) (EnvironmentReadModel, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := errMissingTenantOrEnvironment("resolve environment read model", tenant, environment); err != nil {
		return EnvironmentReadModel{}, err
	}

	entry, err := ResolveEnvironmentDetail(store, tenant, environment)
	if err != nil {
		return EnvironmentReadModel{}, err
	}

	idleStatus, err := ResolveStoredEnvironmentIdleStatus(store, tenant, environment, now)
	if err != nil {
		return EnvironmentReadModel{}, err
	}

	var cloudContext *CloudContextStatus
	if status, ok, err := findCloudContextForKubernetesContext(store, entry.KubernetesContext); err != nil {
		return EnvironmentReadModel{}, err
	} else if ok {
		cloudContext = &status
	}

	var health *EnvironmentHealth
	if !ctx.DryRun {
		rootConfig, err := InspectRootConfig(store)
		if err != nil {
			return EnvironmentReadModel{}, err
		}
		openResult, err := ResolveDoctorTarget(store, OpenParams{Tenant: tenant, Environment: environment})
		if err != nil {
			return EnvironmentReadModel{}, err
		}
		diagnosis := RunDeployDiagnosis(ctx, ShellLaunchParamsFromResult(openResult))
		resolved := ResolveEnvironmentHealth(rootConfig, diagnosis)
		health = &resolved
	}

	return AssembleEnvironmentReadModel(tenant, entry, cloudContext, &idleStatus, health), nil
}
