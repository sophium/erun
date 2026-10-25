package setup

import (
	"errors"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/bootstrap"
)

func refusingSelector(called *bool) SelectTenantFunc {
	return func([]model.TenantConfig) (model.TenantSelectionResult, error) {
		*called = true
		return model.TenantSelectionResult{}, nil
	}
}

func TestSelectTenantUnavailableUsesSoleTenantWithoutAsking(t *testing.T) {
	called := false
	runner := BootstrapRunner{BootstrapInitDependencies: BootstrapInitDependencies{
		TenantSelectionUnavailable: true,
		SelectTenant:               refusingSelector(&called),
	}}

	selection, err := runner.selectTenant(bootstrap.BootstrapInitParams{}, []model.TenantConfig{{Name: "erun"}})
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
	runner := BootstrapRunner{BootstrapInitDependencies: BootstrapInitDependencies{
		TenantSelectionUnavailable: true,
		SelectTenant:               refusingSelector(&called),
	}}

	_, err := runner.selectTenant(bootstrap.BootstrapInitParams{}, []model.TenantConfig{{Name: "erun"}, {Name: "frs"}, {Name: "petios"}})
	if err == nil {
		t.Fatal("expected a refusal when several tenants need a choice nobody can make")
	}
	if called {
		t.Fatal("prompted with no way to read an answer")
	}
	var unavailable model.TenantSelectionUnavailableError
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
	runner := BootstrapRunner{BootstrapInitDependencies: BootstrapInitDependencies{
		TenantSelectionUnavailable: true,
		SelectTenant:               refusingSelector(&called),
	}}

	selection, err := runner.selectTenant(bootstrap.BootstrapInitParams{SelectedTenant: "frs"}, []model.TenantConfig{{Name: "erun"}, {Name: "frs"}})
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

func TestTenantSelectionUnavailableIsNotABootstrapInteraction(t *testing.T) {
	called := false
	runner := BootstrapRunner{BootstrapInitDependencies: BootstrapInitDependencies{
		TenantSelectionUnavailable: true,
		SelectTenant:               refusingSelector(&called),
	}}

	_, err := runner.selectTenant(bootstrap.BootstrapInitParams{}, []model.TenantConfig{{Name: "erun"}, {Name: "frs"}})
	if _, ok := AsBootstrapInitInteraction(err); ok {
		t.Fatal("a transport that cannot ask got asked again through an interaction request")
	}
}
