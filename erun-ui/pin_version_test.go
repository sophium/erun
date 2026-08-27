package main

import (
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// TestPinRepoCheckoutStatusResolvesForALocalWorktree covers the common case: a
// local-agent (or host) environment's worktree already lives on this machine,
// so there is nothing to check against sibling environments.
func TestPinRepoCheckoutStatusResolvesForALocalWorktree(t *testing.T) {
	app := &App{
		deps: erunUIDeps{
			store: stubUIStore{
				envs: map[string]eruncommon.EnvConfig{
					"team/dev": {Name: "dev", Type: eruncommon.EnvironmentTypeLocalAgent, LocalRepoPath: t.TempDir()},
				},
			},
		},
	}
	status, err := app.PinRepoCheckoutStatus(uiSelection{Tenant: "team", Environment: "dev"})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Resolvable {
		t.Fatalf("expected resolvable, got %+v", status)
	}
}

// TestPinRepoCheckoutStatusResolvesFromASiblingsCheckout covers the reported
// case (#1439): a sourceless runtime environment has no worktree of its own,
// but a sibling environment of the same tenant does, and every environment of
// a tenant shares one repo.
func TestPinRepoCheckoutStatusResolvesFromASiblingsCheckout(t *testing.T) {
	checkout := t.TempDir()
	app := &App{
		deps: erunUIDeps{
			store: stubUIStore{
				envs: map[string]eruncommon.EnvConfig{
					"frs/dev":  {Name: "dev", Type: eruncommon.EnvironmentTypeLocalAgent, LocalRepoPath: checkout},
					"frs/prod": {Name: "prod", Type: eruncommon.EnvironmentTypeRuntime},
				},
			},
		},
	}
	status, err := app.PinRepoCheckoutStatus(uiSelection{Tenant: "frs", Environment: "prod"})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Resolvable {
		t.Fatalf("expected resolvable via the sibling checkout, got %+v", status)
	}
}

// TestPinRepoCheckoutStatusRefusesASourcelessEnvWithNoTenantCheckout covers the
// dead end this exists to prevent: a runtime environment with no MountSource
// and no sibling environment checked out on this machine has nothing pin
// could ever rewrite, and the dialog needs to know before offering
// Preview/Apply.
func TestPinRepoCheckoutStatusRefusesASourcelessEnvWithNoTenantCheckout(t *testing.T) {
	app := &App{
		deps: erunUIDeps{
			store: stubUIStore{
				envs: map[string]eruncommon.EnvConfig{
					"frs/prod": {Name: "prod", Type: eruncommon.EnvironmentTypeRuntime},
				},
			},
		},
	}
	status, err := app.PinRepoCheckoutStatus(uiSelection{Tenant: "frs", Environment: "prod"})
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
