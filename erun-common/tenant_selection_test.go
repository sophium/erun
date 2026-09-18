package eruncommon

import (
	"errors"
	"strings"
	"testing"
)

func refusingSelector(called *bool) SelectTenantFunc {
	return func([]TenantConfig) (TenantSelectionResult, error) {
		*called = true
		return TenantSelectionResult{}, nil
	}
}

func TestSelectTenantUnavailableUsesSoleTenantWithoutAsking(t *testing.T) {
	called := false
	runner := bootstrapRunner{BootstrapInitDependencies: BootstrapInitDependencies{
		TenantSelectionUnavailable: true,
		SelectTenant:               refusingSelector(&called),
	}}

	selection, err := runner.selectTenant(BootstrapInitParams{}, []TenantConfig{{Name: "erun"}})
	if err != nil {
		t.Fatalf("selectTenant: %v", err)
	}
	if called {
		t.Fatal("asked for a selection with no way to answer one: a sole tenant is not a choice")
	}
	if selection.Tenant != "erun" {
		t.Fatalf("tenant %q, want erun", selection.Tenant)
	}
}

func TestSelectTenantUnavailableRefusesARealChoiceWithoutAsking(t *testing.T) {
	called := false
	runner := bootstrapRunner{BootstrapInitDependencies: BootstrapInitDependencies{
		TenantSelectionUnavailable: true,
		SelectTenant:               refusingSelector(&called),
	}}

	_, err := runner.selectTenant(BootstrapInitParams{}, []TenantConfig{{Name: "erun"}, {Name: "frs"}, {Name: "petios"}})
	if err == nil {
		t.Fatal("expected a refusal when several tenants need a choice nobody can make")
	}
	if called {
		t.Fatal("prompted with no way to read an answer")
	}
	var unavailable TenantSelectionUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("error %v is not a TenantSelectionUnavailableError", err)
	}
	message := err.Error()
	for _, want := range []string{"--tenant", "erun", "frs", "petios"} {
		if !strings.Contains(message, want) {
			t.Fatalf("refusal %q does not name %q", message, want)
		}
	}
}

func TestSelectTenantUnavailableLeavesAnExplicitChoiceAlone(t *testing.T) {
	called := false
	runner := bootstrapRunner{BootstrapInitDependencies: BootstrapInitDependencies{
		TenantSelectionUnavailable: true,
		SelectTenant:               refusingSelector(&called),
	}}

	selection, err := runner.selectTenant(BootstrapInitParams{SelectedTenant: "frs"}, []TenantConfig{{Name: "erun"}, {Name: "frs"}})
	if err != nil {
		t.Fatalf("selectTenant: %v", err)
	}
	if called {
		t.Fatal("asked for a tenant that was already passed in")
	}
	if selection.Tenant != "frs" {
		t.Fatalf("tenant %q, want frs", selection.Tenant)
	}
}

func TestSelectTenantAvailableStillAsks(t *testing.T) {
	called := false
	runner := bootstrapRunner{BootstrapInitDependencies: BootstrapInitDependencies{
		SelectTenant: func([]TenantConfig) (TenantSelectionResult, error) {
			called = true
			return TenantSelectionResult{Tenant: "frs"}, nil
		},
	}}

	selection, err := runner.selectTenant(BootstrapInitParams{}, []TenantConfig{{Name: "erun"}, {Name: "frs"}})
	if err != nil {
		t.Fatalf("selectTenant: %v", err)
	}
	if !called {
		t.Fatal("a run that can ask must still ask")
	}
	if selection.Tenant != "frs" {
		t.Fatalf("tenant %q, want frs", selection.Tenant)
	}
}

func TestTenantSelectionUnavailableIsNotABootstrapInteraction(t *testing.T) {
	called := false
	runner := bootstrapRunner{BootstrapInitDependencies: BootstrapInitDependencies{
		TenantSelectionUnavailable: true,
		SelectTenant:               refusingSelector(&called),
	}}

	_, err := runner.selectTenant(BootstrapInitParams{}, []TenantConfig{{Name: "erun"}, {Name: "frs"}})
	if _, ok := AsBootstrapInitInteraction(err); ok {
		t.Fatal("a transport that cannot ask got asked again through an interaction request")
	}
}
