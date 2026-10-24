package lifecycle

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	modelpkg "github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// An unobserved power state must read as Unknown, never Stopped or Idle.
func TestResolveEnvironmentLifecycleStateUnknownIsNotAFalseNegative(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   EnvironmentLifecycleInputs
	}{
		{
			name: "managed cloud with no cloud-context observation at all",
			in:   EnvironmentLifecycleInputs{ManagedCloud: true},
		},
		{
			name: "managed cloud with a pending (mid-transition) power state",
			in:   EnvironmentLifecycleInputs{ManagedCloud: true, CloudContextObserved: true, CloudContextStatus: cloud.CloudContextStatusPending},
		},
		{
			name: "managed cloud with an explicitly unknown power state",
			in:   EnvironmentLifecycleInputs{ManagedCloud: true, CloudContextObserved: true, CloudContextStatus: cloud.CloudContextStatusUnknown},
		},
		{
			name: "non-managed with nothing observed at all",
			in:   EnvironmentLifecycleInputs{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveEnvironmentLifecycleState(tc.in)
			if got != modelpkg.EnvironmentLifecycleUnknown {
				t.Fatalf("want unknown, got %s", got)
			}
			if got == modelpkg.EnvironmentLifecycleStopped || got == modelpkg.EnvironmentLifecycleIdle {
				t.Fatalf("an unobserved signal must never resolve to a real state; got %s", got)
			}
		})
	}
}

func TestResolveEnvironmentLifecycleStateManagedCloudPowerState(t *testing.T) {
	stopped := ResolveEnvironmentLifecycleState(EnvironmentLifecycleInputs{
		ManagedCloud: true, CloudContextObserved: true, CloudContextStatus: cloud.CloudContextStatusStopped,
	})
	if stopped != modelpkg.EnvironmentLifecycleStopped {
		t.Fatalf("want stopped, got %s", stopped)
	}

	running := ResolveEnvironmentLifecycleState(EnvironmentLifecycleInputs{
		ManagedCloud: true, CloudContextObserved: true, CloudContextStatus: cloud.CloudContextStatusRunning,
	})
	if running != modelpkg.EnvironmentLifecycleRunning {
		t.Fatalf("want running, got %s", running)
	}
}

func TestResolveEnvironmentLifecycleStateDeployFailedTakesPriorityOverIdle(t *testing.T) {
	// Unhealthy deploy wins over idle eligibility.
	got := ResolveEnvironmentLifecycleState(EnvironmentLifecycleInputs{
		DeployHealthObserved: true, DeployHealthy: false,
		IdleStatusObserved: true, StopEligible: true,
	})
	if got != modelpkg.EnvironmentLifecycleDeployFailed {
		t.Fatalf("want deploy-failed, got %s", got)
	}
}

func TestResolveEnvironmentLifecycleStateIdleAndRunning(t *testing.T) {
	idle := ResolveEnvironmentLifecycleState(EnvironmentLifecycleInputs{
		IdleStatusObserved: true, StopEligible: true,
	})
	if idle != modelpkg.EnvironmentLifecycleIdle {
		t.Fatalf("want idle, got %s", idle)
	}

	running := ResolveEnvironmentLifecycleState(EnvironmentLifecycleInputs{
		IdleStatusObserved: true, StopEligible: false,
	})
	if running != modelpkg.EnvironmentLifecycleRunning {
		t.Fatalf("want running, got %s", running)
	}
}

func TestAssembleEnvironmentReadModelNilSignalsStayUnknown(t *testing.T) {
	entry := ListEnvironmentResult{Name: "dev", Type: config.EnvironmentTypeRemoteAgent}
	model := AssembleEnvironmentReadModel(" acme ", entry, nil, nil, nil)

	if model.Tenant != "acme" {
		t.Fatalf("tenant not trimmed: got %q", model.Tenant)
	}
	if model.State != modelpkg.EnvironmentLifecycleUnknown {
		t.Fatalf("with no cloud context, idle, or health observed, want unknown, got %s", model.State)
	}
	if model.CloudContext != nil || model.Idle != nil || model.Health != nil {
		t.Fatalf("nil inputs must stay nil on the assembled model, got %+v", model)
	}
}

func TestAssembleEnvironmentReadModelUnrefreshedCloudContextIsUnknownNotStopped(t *testing.T) {
	// Status is blank when loaded from config only, as the no-refresh read model produces.
	entry := ListEnvironmentResult{Name: "dev", Type: config.EnvironmentTypeRuntime}
	cloudContext := &modelpkg.CloudContextStatus{CloudContextConfig: modelpkg.CloudContextConfig{Name: "acme-dev"}}
	idle := &modelpkg.EnvironmentIdleStatus{ManagedCloud: true, StopEligible: false}

	model := AssembleEnvironmentReadModel("acme", entry, cloudContext, idle, nil)

	if model.State != modelpkg.EnvironmentLifecycleUnknown {
		t.Fatalf("a blank (unrefreshed) cloud-context status must read as unknown, got %s", model.State)
	}
	if model.CloudContext == nil {
		t.Fatalf("the config-level cloud context should still be attached even though its power state is unknown")
	}
}

func TestAssembleEnvironmentReadModelDeployFailedFromHealth(t *testing.T) {
	entry := ListEnvironmentResult{Name: "dev", Type: config.EnvironmentTypeRemoteAgent}
	idle := &modelpkg.EnvironmentIdleStatus{StopEligible: false}
	resolvedHealth := ResolveEnvironmentHealth(modelpkg.RootConfigInspection{}, modelpkg.DeployDiagnosisResult{HelmStatus: "STATUS: pending-upgrade"})
	health := &resolvedHealth

	model := AssembleEnvironmentReadModel("acme", entry, nil, idle, health)

	if model.State != modelpkg.EnvironmentLifecycleDeployFailed {
		t.Fatalf("want deploy-failed, got %s", model.State)
	}
	if model.Health.RecommendedRecovery != modelpkg.DeployRecoveryClearPendingHelm {
		t.Fatalf("want clear-pending-helm recommendation, got %s", model.Health.RecommendedRecovery)
	}
}
