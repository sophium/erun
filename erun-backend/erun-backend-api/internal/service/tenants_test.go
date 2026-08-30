package service

import (
	"context"
	"errors"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

type fakeTenantNameUpdater struct {
	calls       int
	gotTenantID string
	gotName     string
	result      model.Tenant
	err         error
}

func (f *fakeTenantNameUpdater) UpdateName(_ context.Context, tenantID, name string) (model.Tenant, error) {
	f.calls++
	f.gotTenantID, f.gotName = tenantID, name
	if f.err != nil {
		return model.Tenant{}, f.err
	}
	return f.result, nil
}

type fakeTenantEnvironmentLister struct {
	environments []model.Environment
	err          error
}

func (f *fakeTenantEnvironmentLister) List(context.Context) ([]model.Environment, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.environments, nil
}

// TestReconcileBootstrapNameRefusesWithNoDeclaredTenant locks the "empty
// ERUN_TENANT disables the action" behavior: with nothing declared to
// reconcile against, the workflow must not guess a target name.
func TestReconcileBootstrapNameRefusesWithNoDeclaredTenant(t *testing.T) {
	updater := &fakeTenantNameUpdater{}
	svc := NewTenantService(updater, &fakeTenantEnvironmentLister{}, "")
	_, err := svc.ReconcileBootstrapName(context.Background(), model.Tenant{TenantID: "t1", Name: "operations"})
	if !errors.Is(err, ErrBootstrapNameNotConfigured) {
		t.Fatalf("err = %v, want ErrBootstrapNameNotConfigured", err)
	}
	if updater.calls != 0 {
		t.Fatalf("expected no UpdateName call, got %d", updater.calls)
	}
}

// TestReconcileBootstrapNameNoopWhenAlreadyMatches proves an already-correct
// tenant name is a no-op success, not a refusal.
func TestReconcileBootstrapNameNoopWhenAlreadyMatches(t *testing.T) {
	updater := &fakeTenantNameUpdater{}
	svc := NewTenantService(updater, &fakeTenantEnvironmentLister{}, "frs")
	tenant := model.Tenant{TenantID: "t1", Name: "frs"}
	got, err := svc.ReconcileBootstrapName(context.Background(), tenant)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "frs" {
		t.Fatalf("got.Name = %q, want %q", got.Name, "frs")
	}
	if updater.calls != 0 {
		t.Fatalf("expected no UpdateName call when already matching, got %d", updater.calls)
	}
}

// TestReconcileBootstrapNameAppliesWhenNoEnvironments locks the apply path:
// zero environments is exactly the precondition that authorises the rename.
func TestReconcileBootstrapNameAppliesWhenNoEnvironments(t *testing.T) {
	updater := &fakeTenantNameUpdater{result: model.Tenant{TenantID: "t1", Name: "frs"}}
	svc := NewTenantService(updater, &fakeTenantEnvironmentLister{}, "frs")
	tenant := model.Tenant{TenantID: "t1", Name: "operations"}
	got, err := svc.ReconcileBootstrapName(context.Background(), tenant)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "frs" {
		t.Fatalf("got.Name = %q, want %q", got.Name, "frs")
	}
	if updater.gotTenantID != "t1" || updater.gotName != "frs" {
		t.Fatalf("UpdateName called with (%q, %q), want (\"t1\", \"frs\")", updater.gotTenantID, updater.gotName)
	}
}

// TestReconcileBootstrapNameRefusesWhenTenantHasEnvironments locks the
// refusal path (erun#1480's central safety property): any existing environment
// refuses the rename outright, and no UpdateName call is even attempted.
func TestReconcileBootstrapNameRefusesWhenTenantHasEnvironments(t *testing.T) {
	updater := &fakeTenantNameUpdater{}
	environments := &fakeTenantEnvironmentLister{environments: []model.Environment{{EnvironmentID: "e1"}}}
	svc := NewTenantService(updater, environments, "frs")
	tenant := model.Tenant{TenantID: "t1", Name: "operations"}
	_, err := svc.ReconcileBootstrapName(context.Background(), tenant)
	if !errors.Is(err, ErrBootstrapNameHasEnvironments) {
		t.Fatalf("err = %v, want ErrBootstrapNameHasEnvironments", err)
	}
	if updater.calls != 0 {
		t.Fatalf("expected no UpdateName call when the tenant has environments, got %d", updater.calls)
	}
}

// TestReconcileBootstrapNameReportsConflict names a repository-level unique
// violation as ErrBootstrapNameConflict, so the route can tell "another
// tenant already holds this name" apart from an ordinary persistence error.
func TestReconcileBootstrapNameReportsConflict(t *testing.T) {
	updater := &fakeTenantNameUpdater{err: repository.ErrConflict}
	svc := NewTenantService(updater, &fakeTenantEnvironmentLister{}, "frs")
	tenant := model.Tenant{TenantID: "t1", Name: "operations"}
	_, err := svc.ReconcileBootstrapName(context.Background(), tenant)
	if !errors.Is(err, ErrBootstrapNameConflict) {
		t.Fatalf("err = %v, want ErrBootstrapNameConflict", err)
	}
}
