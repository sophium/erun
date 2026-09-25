package main

import (
	"context"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// orchestratorAliasTestApp is orchestratorTestApp with one erun-type cloud
// alias configured, which is what every alias write is validated against, plus
// one alias of another type, so a test can show the picker and the writer keep
// to the erun ones.
func orchestratorAliasTestApp(t *testing.T) *App {
	t.Helper()
	app := orchestratorTestApp(t)
	store := app.deps.store.(stubUIStore)
	store.config.CloudProviders = []eruncommon.CloudProviderConfig{
		{Alias: "erun+erunpaas.com@erun", Provider: eruncommon.CloudProviderERun},
		{Alias: "aws-prod", Provider: eruncommon.CloudProviderAWS},
	}
	return app
}

// TestAnOrchestratorsAliasSurvivesAnUnrelatedEditMadeInTheDesktop is the
// round-trip this field needs and a bare "the field exists" test would miss.
//
// The alias is writable from two surfaces: `erun orchestrator set-alias` and
// the desktop dialog. Both save by replacing the orchestrator's whole config
// entry, so a dialog that did not carry the alias back — because the read model
// never showed it, or because the save omitted it — would erase a value the
// operator set from the terminal on the next unrelated edit, silently.
//
// The edit here touches nothing but the name, through exactly the value the
// dialog is handed (ListOrchestrators' own alias), which is why it reproduces
// the erasure rather than merely exercising the writer: assert the read model
// carries the alias and pass "" instead and the final assertion fails.
func TestAnOrchestratorsAliasSurvivesAnUnrelatedEditMadeInTheDesktop(t *testing.T) {
	app := orchestratorAliasTestApp(t)
	defer app.shutdown(context.Background())

	const alias = "erun+erunpaas.com@erun"
	devEnvs := []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}
	created := mustCreateOrchestrator(t, app, "agent", devEnvs, "")
	if created.Alias != "" {
		t.Fatalf("a new orchestrator declared an alias of its own: %q", created.Alias)
	}

	// The terminal writer, `erun orchestrator set-alias`, over the same store.
	mustSetOrchestratorAlias(t, app, created.ID, alias)

	// What the dialog is handed when it opens for this orchestrator.
	listed := app.ListOrchestrators()
	if len(listed) != 1 {
		t.Fatalf("expected one orchestrator, got %+v", listed)
	}
	if listed[0].Alias != alias {
		t.Fatalf("the read model the dialog seeds from reported alias %q, want %q", listed[0].Alias, alias)
	}

	// An unrelated edit: the name changes, nothing else does. The alias is sent
	// back exactly as the dialog was shown it.
	updated := mustUpdateOrchestrator(t, app, created.ID, "renamed", devEnvs, listed[0].Alias)
	if updated.Alias != alias || updated.Name == "agent" {
		t.Fatalf("update returned %+v, want the new name and the kept alias", updated)
	}

	// And it is still on disk, not only in the returned snapshot.
	config := mustLoadRootConfig(t, app)
	if len(config.Orchestrators) != 1 || config.Orchestrators[0].Alias != alias {
		t.Fatalf("the alias did not survive the edit: %+v", config.Orchestrators)
	}
}

func mustCreateOrchestrator(t *testing.T, app *App, name string, envs []orchestratorEnvInput, alias string) orchestratorInfo {
	t.Helper()
	info, err := app.CreateOrchestrator(name, envs, nil, alias)
	if err != nil {
		t.Fatalf("CreateOrchestrator(%q) failed: %v", name, err)
	}
	return info
}

func mustUpdateOrchestrator(t *testing.T, app *App, id, name string, envs []orchestratorEnvInput, alias string) orchestratorInfo {
	t.Helper()
	info, err := app.UpdateOrchestrator(id, name, envs, nil, alias)
	if err != nil {
		t.Fatalf("UpdateOrchestrator(%q) failed: %v", id, err)
	}
	return info
}

// mustSetOrchestratorAlias writes the alias the way the terminal does, through
// the shared writer rather than through the dialog's own path: the round trip
// this covers is precisely a value set outside the desktop surviving an edit
// made inside it.
func mustSetOrchestratorAlias(t *testing.T, app *App, id, alias string) {
	t.Helper()
	if _, err := eruncommon.SetOrchestratorAlias(eruncommon.Context{}, app.deps.store, eruncommon.SetOrchestratorAliasParams{
		OrchestratorID: id,
		Alias:          alias,
	}); err != nil {
		t.Fatalf("SetOrchestratorAlias(%q) failed: %v", id, err)
	}
}

func mustLoadRootConfig(t *testing.T, app *App) eruncommon.ERunConfig {
	t.Helper()
	config, _, err := app.deps.store.LoadERunConfig()
	if err != nil {
		t.Fatalf("LoadERunConfig failed: %v", err)
	}
	return config
}

// TestARunningOrchestratorsAliasReachesTheDialogItIsEditedFrom covers the other
// source the dialog is seeded from. A running orchestrator's row is built from
// its live session, not from the persisted definition, so an alias the session
// did not carry would read as none and be erased on the next save — the same
// loss as the persisted path, reached through a different field.
func TestARunningOrchestratorsAliasReachesTheDialogItIsEditedFrom(t *testing.T) {
	app := orchestratorAliasTestApp(t)
	defer app.shutdown(context.Background())

	const alias = "erun+erunpaas.com@erun"
	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, alias)
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	started, err := app.StartOrchestrator(created.ID, 80, 24)
	if err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	if started.Alias != alias {
		t.Fatalf("a started orchestrator reported alias %q, want %q", started.Alias, alias)
	}

	info, ok := app.runningOrchestratorInfo(created.ID)
	if !ok || info.Alias != alias {
		t.Fatalf("the running snapshot reported %+v (ok=%v), want the alias it was started with", info, ok)
	}
	listed := app.ListOrchestrators()
	if len(listed) != 1 || listed[0].Alias != alias {
		t.Fatalf("the listed running orchestrator reported %+v, want alias %q", listed, alias)
	}
}

// TestTheDesktopRefusesAnAliasThisHostCannotResolve mirrors the writer's own
// gate: the dialog offers the resolved list, but a caller that bypasses the
// picker must not be able to store a declaration nothing could honour.
func TestTheDesktopRefusesAnAliasThisHostCannotResolve(t *testing.T) {
	app := orchestratorAliasTestApp(t)
	defer app.shutdown(context.Background())

	_, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "erun+elsewhere.example@erun")
	if err == nil {
		t.Fatal("CreateOrchestrator accepted an alias this host does not have configured")
	}
	if !strings.Contains(err.Error(), "cloud provider alias") {
		t.Fatalf("error %v does not name the alias it could not resolve", err)
	}

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "erun+erunpaas.com@erun")
	if err != nil {
		t.Fatalf("CreateOrchestrator with a configured alias failed: %v", err)
	}
	if _, err := app.UpdateOrchestrator(created.ID, "agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "aws-prod"); err == nil {
		t.Fatal("UpdateOrchestrator accepted a non-erun alias")
	}
}

// TestListOrchestratorAliasChoicesOffersOnlyERunAliases keeps the picker to what
// the writer will actually accept.
func TestListOrchestratorAliasChoicesOffersOnlyERunAliases(t *testing.T) {
	app := orchestratorAliasTestApp(t)
	defer app.shutdown(context.Background())

	choices, err := app.ListOrchestratorAliasChoices()
	if err != nil {
		t.Fatalf("ListOrchestratorAliasChoices failed: %v", err)
	}
	if len(choices) != 1 || choices[0] != "erun+erunpaas.com@erun" {
		t.Fatalf("choices = %+v, want only the erun-type alias", choices)
	}
}
