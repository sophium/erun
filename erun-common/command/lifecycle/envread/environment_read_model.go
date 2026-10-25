package envread

import (
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/envstore"
	"github.com/sophium/erun/erun-common/command/lifecycle/list"
	"github.com/sophium/erun/erun-common/infra/activity/idlestatus"
	"github.com/sophium/erun/erun-common/infra/cloud/cloudcontext"
	"github.com/sophium/erun/erun-common/infra/cloud/shelllaunch"
	diagnosispkg "github.com/sophium/erun/erun-common/orchestration/diagnosis"
	deploydiagnosis "github.com/sophium/erun/erun-common/primitive/deploy/diagnosis"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
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
func ResolveEnvironmentLifecycleState(in EnvironmentLifecycleInputs) model.EnvironmentLifecycleState {
	if in.ManagedCloud {
		if state, resolved := resolveManagedCloudLifecycleState(in); resolved {
			return state
		}
		// Running can still be deploy-failed or idle-eligible.
	}
	return resolveWorkloadLifecycleState(in)
}

// resolved is false only when the instance is confirmed running.
func resolveManagedCloudLifecycleState(in EnvironmentLifecycleInputs) (state model.EnvironmentLifecycleState, resolved bool) {
	if !in.CloudContextObserved {
		return model.EnvironmentLifecycleUnknown, true
	}
	switch in.CloudContextStatus {
	case cloudcontext.CloudContextStatusStopped:
		return model.EnvironmentLifecycleStopped, true
	case cloudcontext.CloudContextStatusRunning:
		return "", false
	default:
		return model.EnvironmentLifecycleUnknown, true
	}
}

func resolveWorkloadLifecycleState(in EnvironmentLifecycleInputs) model.EnvironmentLifecycleState {
	switch {
	case in.DeployHealthObserved && !in.DeployHealthy:
		return model.EnvironmentLifecycleDeployFailed
	case in.IdleStatusObserved && in.StopEligible:
		return model.EnvironmentLifecycleIdle
	case in.IdleStatusObserved || in.DeployHealthObserved || in.ManagedCloud:
		return model.EnvironmentLifecycleRunning
	default:
		// Nothing observed: Running would be a guess.
		return model.EnvironmentLifecycleUnknown
	}
}

// ResolveEnvironmentHealth runs no commands; the caller supplies a real diagnosis.
func ResolveEnvironmentHealth(rootConfig model.RootConfigInspection, deploy model.DeployDiagnosisResult) model.EnvironmentHealth {
	recovery, _ := deploydiagnosis.RecommendedDeployRecovery(deploy)
	return model.EnvironmentHealth{RootConfig: rootConfig, Deploy: deploy, RecommendedRecovery: recovery}
}

// EnvironmentReadModel is an environment's summary and detail with one resolved lifecycle state.
type EnvironmentReadModel struct {
	Tenant       string                          `json:"tenant"`
	Environment  list.ListEnvironmentResult      `json:"environment"`
	State        model.EnvironmentLifecycleState `json:"state"`
	CloudContext *model.CloudContextStatus       `json:"cloudContext,omitempty"`
	Idle         *model.EnvironmentIdleStatus    `json:"idle,omitempty"`
	Health       *model.EnvironmentHealth        `json:"health,omitempty"`
}

// AssembleEnvironmentReadModel treats a nil cloudContext, idle, or health as unobserved.
func AssembleEnvironmentReadModel(tenant string, environment list.ListEnvironmentResult, cloudContext *model.CloudContextStatus, idle *model.EnvironmentIdleStatus, health *model.EnvironmentHealth) EnvironmentReadModel {
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
		_, needsRecovery := deploydiagnosis.RecommendedDeployRecovery(health.Deploy)
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
func ResolveEnvironmentDetail(store envstore.ListStore, tenant, environment string) (list.ListEnvironmentResult, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := core.ErrMissingTenantOrEnvironment("resolve environment detail", tenant, environment); err != nil {
		return list.ListEnvironmentResult{}, err
	}
	tenantConfig, _, err := store.LoadTenantConfig(tenant)
	if err != nil {
		return list.ListEnvironmentResult{}, err
	}
	envConfig, _, err := store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return list.ListEnvironmentResult{}, err
	}
	effective, effectiveErr := resolve.ResolveOpen(store, model.OpenParams{Tenant: tenant, Environment: environment})
	portAllocations, err := config.ResolveAllEnvironmentLocalPorts(store)
	if err != nil {
		return list.ListEnvironmentResult{}, err
	}
	return list.NewListEnvironmentResult(store, tenantConfig, envConfig, effective, effectiveErr, portAllocations), nil
}

// ResolveEnvironmentReadModel never refreshes live power state, and diagnoses only outside dry-run.
func ResolveEnvironmentReadModel(ctx core.Context, store envstore.ListStore, tenant, environment string, now time.Time) (EnvironmentReadModel, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := core.ErrMissingTenantOrEnvironment("resolve environment read model", tenant, environment); err != nil {
		return EnvironmentReadModel{}, err
	}

	entry, err := ResolveEnvironmentDetail(store, tenant, environment)
	if err != nil {
		return EnvironmentReadModel{}, err
	}

	idleStatus, err := idlestatus.ResolveStoredEnvironmentIdleStatus(store, tenant, environment, now)
	if err != nil {
		return EnvironmentReadModel{}, err
	}

	var cloudContext *model.CloudContextStatus
	if status, ok, err := config.FindCloudContextForKubernetesContext(store, entry.KubernetesContext); err != nil {
		return EnvironmentReadModel{}, err
	} else if ok {
		cloudContext = &status
	}

	var health *model.EnvironmentHealth
	if !ctx.DryRun {
		rootConfig, err := config.InspectRootConfig(store)
		if err != nil {
			return EnvironmentReadModel{}, err
		}
		openResult, err := diagnosispkg.ResolveDoctorTarget(store, model.OpenParams{Tenant: tenant, Environment: environment})
		if err != nil {
			return EnvironmentReadModel{}, err
		}
		request := shelllaunch.ShellLaunchParamsFromResult(openResult)
		diagnosis := deploydiagnosis.RunDeployDiagnosis(ctx, request)
		resolved := ResolveEnvironmentHealth(rootConfig, diagnosis)
		health = &resolved
	}

	return AssembleEnvironmentReadModel(tenant, entry, cloudContext, &idleStatus, health), nil
}
