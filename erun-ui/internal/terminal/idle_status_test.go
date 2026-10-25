package terminal

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

func TestIdleStatusToUICarriesFromPodProvenance(t *testing.T) {
	app := newTestService(Deps{})
	status := model.EnvironmentIdleStatus{StopEligible: true}
	result := config.OpenResult{}

	if ui := app.idleStatusToUI(result, status, true); !ui.FromPod {
		t.Fatalf("a pod-backed reading must report FromPod=true, got %+v", ui)
	}
	if ui := app.idleStatusToUI(result, status, false); ui.FromPod {
		t.Fatalf("a host-only reading must report FromPod=false, got %+v", ui)
	}
}

func TestLoadLocalIdleStatusReportsHostOnlyProvenance(t *testing.T) {
	store := stubUIStore{envs: map[string]config.EnvConfig{
		"acme/dev": {Name: "dev"},
	}}
	app := newTestService(Deps{Store: store})
	result := config.OpenResult{Tenant: "acme", Environment: "dev"}

	resolved, err := app.loadLocalIdleStatus(result)
	if err != nil {
		t.Fatalf("loadLocalIdleStatus: %v", err)
	}
	if resolved.ui.FromPod {
		t.Fatalf("the host-only fallback must never report FromPod=true, got %+v", resolved.ui)
	}
}
