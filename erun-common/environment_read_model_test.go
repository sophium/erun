package eruncommon

import "testing"

// TestResolveEnvironmentLifecycleStateUnknownIsNotAFalseNegative is the
// mandatory negative case: an environment whose power state was never
// observed must read as Unknown, never as Stopped or Idle just because a
// bool defaulted to false. CloudContextStatus is a live value this package
// never persists (see CloudContextStatus's own doc comment), so a
// managed-cloud environment queried without a fresh refresh is exactly the
// realistic case this guards.
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
			in:   EnvironmentLifecycleInputs{ManagedCloud: true, CloudContextObserved: true, CloudContextStatus: CloudContextStatusPending},
		},
		{
			name: "managed cloud with an explicitly unknown power state",
			in:   EnvironmentLifecycleInputs{ManagedCloud: true, CloudContextObserved: true, CloudContextStatus: CloudContextStatusUnknown},
		},
		{
			name: "non-managed with nothing observed at all",
			in:   EnvironmentLifecycleInputs{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveEnvironmentLifecycleState(tc.in)
			if got != EnvironmentLifecycleUnknown {
				t.Fatalf("want unknown, got %s", got)
			}
			if got == EnvironmentLifecycleStopped || got == EnvironmentLifecycleIdle {
				t.Fatalf("an unobserved signal must never resolve to a real state; got %s", got)
			}
		})
	}
}

func TestResolveEnvironmentLifecycleStateManagedCloudPowerState(t *testing.T) {
	stopped := ResolveEnvironmentLifecycleState(EnvironmentLifecycleInputs{
		ManagedCloud: true, CloudContextObserved: true, CloudContextStatus: CloudContextStatusStopped,
	})
	if stopped != EnvironmentLifecycleStopped {
		t.Fatalf("want stopped, got %s", stopped)
	}

	running := ResolveEnvironmentLifecycleState(EnvironmentLifecycleInputs{
		ManagedCloud: true, CloudContextObserved: true, CloudContextStatus: CloudContextStatusRunning,
	})
	if running != EnvironmentLifecycleRunning {
		t.Fatalf("want running, got %s", running)
	}
}

func TestResolveEnvironmentLifecycleStateDeployFailedTakesPriorityOverIdle(t *testing.T) {
	// A deploy diagnosis reporting unhealthy must win even when the idle
	// policy would otherwise call the environment stop-eligible: an
	// environment stuck on a bad release is not "idle", it is broken.
	got := ResolveEnvironmentLifecycleState(EnvironmentLifecycleInputs{
		DeployHealthObserved: true, DeployHealthy: false,
		IdleStatusObserved: true, StopEligible: true,
	})
	if got != EnvironmentLifecycleDeployFailed {
		t.Fatalf("want deploy-failed, got %s", got)
	}
}

func TestResolveEnvironmentLifecycleStateIdleAndRunning(t *testing.T) {
	idle := ResolveEnvironmentLifecycleState(EnvironmentLifecycleInputs{
		IdleStatusObserved: true, StopEligible: true,
	})
	if idle != EnvironmentLifecycleIdle {
		t.Fatalf("want idle, got %s", idle)
	}

	running := ResolveEnvironmentLifecycleState(EnvironmentLifecycleInputs{
		IdleStatusObserved: true, StopEligible: false,
	})
	if running != EnvironmentLifecycleRunning {
		t.Fatalf("want running, got %s", running)
	}
}

func TestAssembleEnvironmentReadModelNilSignalsStayUnknown(t *testing.T) {
	entry := ListEnvironmentResult{Name: "dev", Type: EnvironmentTypeRemoteAgent}
	model := AssembleEnvironmentReadModel(" acme ", entry, nil, nil, nil)

	if model.Tenant != "acme" {
		t.Fatalf("tenant not trimmed: got %q", model.Tenant)
	}
	if model.State != EnvironmentLifecycleUnknown {
		t.Fatalf("with no cloud context, idle, or health observed, want unknown, got %s", model.State)
	}
	if model.CloudContext != nil || model.Idle != nil || model.Health != nil {
		t.Fatalf("nil inputs must stay nil on the assembled model, got %+v", model)
	}
}

func TestAssembleEnvironmentReadModelUnrefreshedCloudContextIsUnknownNotStopped(t *testing.T) {
	// CloudContextStatus.Status is blank whenever it was loaded from config
	// only (findCloudContextForKubernetesContext never refreshes it) -- this
	// is the realistic shape ResolveEnvironmentReadModel's default,
	// no-live-refresh call produces for a managed-cloud environment.
	entry := ListEnvironmentResult{Name: "dev", Type: EnvironmentTypeRuntime}
	cloudContext := &CloudContextStatus{CloudContextConfig: CloudContextConfig{Name: "acme-dev"}}
	idle := &EnvironmentIdleStatus{ManagedCloud: true, StopEligible: false}

	model := AssembleEnvironmentReadModel("acme", entry, cloudContext, idle, nil)

	if model.State != EnvironmentLifecycleUnknown {
		t.Fatalf("a blank (unrefreshed) cloud-context status must read as unknown, got %s", model.State)
	}
	if model.CloudContext == nil {
		t.Fatalf("the config-level cloud context should still be attached even though its power state is unknown")
	}
}

func TestAssembleEnvironmentReadModelDeployFailedFromHealth(t *testing.T) {
	entry := ListEnvironmentResult{Name: "dev", Type: EnvironmentTypeRemoteAgent}
	idle := &EnvironmentIdleStatus{StopEligible: false}
	resolvedHealth := ResolveEnvironmentHealth(RootConfigInspection{}, DeployDiagnosisResult{HelmStatus: "STATUS: pending-upgrade"})
	health := &resolvedHealth

	model := AssembleEnvironmentReadModel("acme", entry, nil, idle, health)

	if model.State != EnvironmentLifecycleDeployFailed {
		t.Fatalf("want deploy-failed, got %s", model.State)
	}
	if model.Health.RecommendedRecovery != DeployRecoveryClearPendingHelm {
		t.Fatalf("want clear-pending-helm recommendation, got %s", model.Health.RecommendedRecovery)
	}
}
