package main

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/activity"
)

// TestIdleStatusToUICarriesFromPodProvenance: the idle widget must be able to
// tell whether a reading came from the pod's own idle monitor or was
// assembled on the host because the pod could not be reached.
func TestIdleStatusToUICarriesFromPodProvenance(t *testing.T) {
	app := &App{}
	status := activity.EnvironmentIdleStatus{StopEligible: true}
	result := config.OpenResult{}

	if ui := app.idleStatusToUI(result, status, true); !ui.FromPod {
		t.Fatalf("a pod-backed reading must report FromPod=true, got %+v", ui)
	}
	if ui := app.idleStatusToUI(result, status, false); ui.FromPod {
		t.Fatalf("a host-only reading must report FromPod=false, got %+v", ui)
	}
}

// TestLoadLocalIdleStatusReportsHostOnlyProvenance locks in the one call site
// that is always host-only: the local fallback path never claims a live pod
// observation.
func TestLoadLocalIdleStatusReportsHostOnlyProvenance(t *testing.T) {
	store := stubUIStore{envs: map[string]config.EnvConfig{
		"acme/dev": {Name: "dev"},
	}}
	app := &App{deps: erunUIDeps{store: store}}
	result := config.OpenResult{Tenant: "acme", Environment: "dev"}

	resolved, err := app.loadLocalIdleStatus(result)
	if err != nil {
		t.Fatalf("loadLocalIdleStatus: %v", err)
	}
	if resolved.ui.FromPod {
		t.Fatalf("the host-only fallback must never report FromPod=true, got %+v", resolved.ui)
	}
}
