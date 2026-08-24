package main

import (
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// TestIdleStatusToUICarriesFromPodProvenance is the regression for
// erun#1216 bug 3: the idle widget rendered a confident countdown with no
// way to tell whether the reading came from the pod's own idle monitor or
// was assembled on the host because the pod could not be reached — the same
// moment the sidebar might be showing the environment as unreachable.
func TestIdleStatusToUICarriesFromPodProvenance(t *testing.T) {
	app := &App{}
	status := eruncommon.EnvironmentIdleStatus{StopEligible: true}
	result := eruncommon.OpenResult{}

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
	store := stubUIStore{envs: map[string]eruncommon.EnvConfig{
		"acme/dev": {Name: "dev"},
	}}
	app := &App{deps: erunUIDeps{store: store}}
	result := eruncommon.OpenResult{Tenant: "acme", Environment: "dev"}

	resolved, err := app.loadLocalIdleStatus(result)
	if err != nil {
		t.Fatalf("loadLocalIdleStatus: %v", err)
	}
	if resolved.ui.FromPod {
		t.Fatalf("the host-only fallback must never report FromPod=true, got %+v", resolved.ui)
	}
}
