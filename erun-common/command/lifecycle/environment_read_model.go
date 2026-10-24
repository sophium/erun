package lifecycle

import (
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/infra/cloud"
	diagnosispkg "github.com/sophium/erun/erun-common/orchestration/diagnosis"
	deploypkg "github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/open"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// EnvironmentLifecycleState combines power state, deploy health, and idle eligibility.
type EnvironmentLifecycleState string

const (
	EnvironmentLifecycleRunning      EnvironmentLifecycleState = "running"
	EnvironmentLifecycleIdle         EnvironmentLifecycleState = "idle"
	EnvironmentLifecycleDeployFailed EnvironmentLifecycleState = "deploy-failed"
	EnvironmentLifecycleStopped      EnvironmentLifecycleState = "stopped"
	// EnvironmentLifecycleUnknown is reported when a signal the state depends on was not observed.
	EnvironmentLifecycleUnknown EnvironmentLifecycleState = "unknown"
)

// EnvironmentLifecycleInputs carries an explicit observed bit per signal so a zero value is
// never mistaken for a real negative reading.
type EnvironmentLifecycleInputs struct {
	ManagedCloud bool

	// CloudContextStatus is never persisted, so only a live read counts as observed.
	CloudContextObserved bool
	CloudContextStatus   string

	DeployHealthObserved bool
	DeployHealthy        bool

	IdleStatusObserved bool
	StopEligible       bool
}

// ResolveEnvironmentLifecycleState never uses an unobserved input to tell Stopped or Idle from Unknown.
func ResolveEnvironmentLifecycleState(in EnvironmentLifecycleInputs) EnvironmentLifecycleState {
	if in.ManagedCloud {
		if state, resolved := resolveManagedCloudLifecycleState(in); resolved {
			return state
		}
		// Running can still be deploy-failed or idle-eligible.
	}
	return resolveWorkloadLifecycleState(in)
}

// resolved is false only when the instance is confirmed running.
func resolveManagedCloudLifecycleState(in EnvironmentLifecycleInputs) (state EnvironmentLifecycleState, resolved bool) {
	if !in.CloudContextObserved {
		return EnvironmentLifecycleUnknown, true
	}
	switch in.CloudContextStatus {
	case cloud.CloudContextStatusStopped:
		return EnvironmentLifecycleStopped, true
	case cloud.CloudContextStatusRunning:
		return "", false
	default:
		return EnvironmentLifecycleUnknown, true
	}
}

func resolveWorkloadLifecycleState(in EnvironmentLifecycleInputs) EnvironmentLifecycleState {
	switch {
	case in.DeployHealthObserved && !in.DeployHealthy:
		return EnvironmentLifecycleDeployFailed
	case in.IdleStatusObserved && in.StopEligible:
		return EnvironmentLifecycleIdle
	case in.IdleStatusObserved || in.DeployHealthObserved || in.ManagedCloud:
		return EnvironmentLifecycleRunning
	default:
		// Nothing observed: Running would be a guess.
		return EnvironmentLifecycleUnknown
	}
}

// EnvironmentHealth is the root config gate and deploy diagnosis with its recommended recovery.
type EnvironmentHealth struct {
	RootConfig          config.RootConfigInspection     `json:"rootConfig"`
	Deploy              deploypkg.DeployDiagnosisResult `json:"deploy"`
	RecommendedRecovery deploypkg.DeployRecoveryAction  `json:"recommendedRecovery,omitempty"`
}

// ResolveEnvironmentHealth runs no commands; the caller supplies a real diagnosis.
func ResolveEnvironmentHealth(rootConfig config.RootConfigInspection, deploy deploypkg.DeployDiagnosisResult) EnvironmentHealth {
	recovery, _ := deploypkg.RecommendedDeployRecovery(deploy)
	return EnvironmentHealth{RootConfig: rootConfig, Deploy: deploy, RecommendedRecovery: recovery}
}

// EnvironmentReadModel is an environment's summary and detail with one resolved lifecycle state.
type EnvironmentReadModel struct {
	Tenant       string                          `json:"tenant"`
	Environment  ListEnvironmentResult           `json:"environment"`
	State        EnvironmentLifecycleState       `json:"state"`
	CloudContext *config.CloudContextStatus      `json:"cloudContext,omitempty"`
	Idle         *activity.EnvironmentIdleStatus `json:"idle,omitempty"`
	Health       *EnvironmentHealth              `json:"health,omitempty"`
}

// AssembleEnvironmentReadModel treats a nil cloudContext, idle, or health as unobserved.
func AssembleEnvironmentReadModel(tenant string, environment ListEnvironmentResult, cloudContext *config.CloudContextStatus, idle *activity.EnvironmentIdleStatus, health *EnvironmentHealth) EnvironmentReadModel {
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
		_, needsRecovery := deploypkg.RecommendedDeployRecovery(health.Deploy)
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

// ResolveEnvironmentDetail returns ResolveListResult's entry for one tenant/environment.
func ResolveEnvironmentDetail(store ListStore, tenant, environment string) (ListEnvironmentResult, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := model.ErrMissingTenantOrEnvironment("resolve environment detail", tenant, environment); err != nil {
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
	effective, effectiveErr := open.ResolveOpen(store, shared.OpenParams{Tenant: tenant, Environment: environment})
	portAllocations, err := config.ResolveAllEnvironmentLocalPorts(store)
	if err != nil {
		return ListEnvironmentResult{}, err
	}
	return NewListEnvironmentResult(store, tenantConfig, envConfig, effective, effectiveErr, portAllocations), nil
}

// ResolveEnvironmentReadModel never refreshes live power state, and diagnoses only outside dry-run.
func ResolveEnvironmentReadModel(ctx core.Context, store ListStore, tenant, environment string, now time.Time) (EnvironmentReadModel, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := model.ErrMissingTenantOrEnvironment("resolve environment read model", tenant, environment); err != nil {
		return EnvironmentReadModel{}, err
	}

	entry, err := ResolveEnvironmentDetail(store, tenant, environment)
	if err != nil {
		return EnvironmentReadModel{}, err
	}

	idleStatus, err := activity.ResolveStoredEnvironmentIdleStatus(store, tenant, environment, now)
	if err != nil {
		return EnvironmentReadModel{}, err
	}

	var cloudContext *config.CloudContextStatus
	if status, ok, err := config.FindCloudContextForKubernetesContext(store, entry.KubernetesContext); err != nil {
		return EnvironmentReadModel{}, err
	} else if ok {
		cloudContext = &status
	}

	var health *EnvironmentHealth
	if !ctx.DryRun {
		rootConfig, err := config.InspectRootConfig(store)
		if err != nil {
			return EnvironmentReadModel{}, err
		}
		openResult, err := diagnosispkg.ResolveDoctorTarget(store, shared.OpenParams{Tenant: tenant, Environment: environment})
		if err != nil {
			return EnvironmentReadModel{}, err
		}
		request := cloud.ShellLaunchParamsFromResult(openResult)
		diagnosis := deploypkg.RunDeployDiagnosis(ctx, request)
		resolved := ResolveEnvironmentHealth(rootConfig, diagnosis)
		health = &resolved
	}

	return AssembleEnvironmentReadModel(tenant, entry, cloudContext, &idleStatus, health), nil
}
