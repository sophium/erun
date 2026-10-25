package activity

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestPinRepoCheckoutStatusResolvesForALocalWorktree(t *testing.T) {
	app := newTestService(Deps{
		Store: fakeStore{
			envs: map[string]config.EnvConfig{
				"team/dev": {Name: "dev", Type: config.EnvironmentTypeLocalAgent, LocalRepoPath: t.TempDir()},
			},
		},
	})
	status, err := app.PinRepoCheckoutStatus(uimodel.Selection{Tenant: "team", Environment: "dev"})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Resolvable {
		t.Fatalf("expected resolvable, got %+v", status)
	}
}

// A sourceless runtime env resolves through a sibling's checkout of the shared repo.
func TestPinRepoCheckoutStatusResolvesFromASiblingsCheckout(t *testing.T) {
	checkout := t.TempDir()
	app := newTestService(Deps{
		Store: fakeStore{
			envs: map[string]config.EnvConfig{
				"frs/dev":  {Name: "dev", Type: config.EnvironmentTypeLocalAgent, LocalRepoPath: checkout},
				"frs/prod": {Name: "prod", Type: config.EnvironmentTypeRuntime},
			},
		},
	})
	status, err := app.PinRepoCheckoutStatus(uimodel.Selection{Tenant: "frs", Environment: "prod"})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Resolvable {
		t.Fatalf("expected resolvable via the sibling checkout, got %+v", status)
	}
}

func TestPinRepoCheckoutStatusRefusesASourcelessEnvWithNoTenantCheckout(t *testing.T) {
	app := newTestService(Deps{
		Store: fakeStore{
			envs: map[string]config.EnvConfig{
				"frs/prod": {Name: "prod", Type: config.EnvironmentTypeRuntime},
			},
		},
	})
	status, err := app.PinRepoCheckoutStatus(uimodel.Selection{Tenant: "frs", Environment: "prod"})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Resolvable {
		t.Fatal("expected not resolvable")
	}
	if status.Reason == "" {
		t.Fatal("expected a reason naming the problem")
	}
}
