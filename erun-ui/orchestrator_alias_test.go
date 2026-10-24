package main

import (
	"context"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/orchestrator"
)

// orchestratorAliasTestApp configures one erun alias and one of another type.
func orchestratorAliasTestApp(t *testing.T) *App {
	t.Helper()
	app := orchestratorTestApp(t)
	store := stubStore(app)
	store.config.CloudProviders = []model.CloudProviderConfig{
		{Alias: "erun+erunpaas.com@erun", Provider: configpkg.CloudProviderERun},
		{Alias: "aws-prod", Provider: configpkg.CloudProviderAWS},
	}
	return app
}

// The dialog saves the whole entry, so an alias it does not carry back would erase
// one set from the terminal.
func TestAnOrchestratorsAliasSurvivesAnUnrelatedEditMadeInTheDesktop(t *testing.T) {
	app := orchestratorAliasTestApp(t)
	defer app.shutdown(context.Background())

	const alias = "erun+erunpaas.com@erun"
	devEnvs := []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}
	created := mustCreateOrchestrator(t, app, "agent", devEnvs, "")
	if created.Alias != "" {
		t.Fatalf("a new orchestrator declared an alias of its own: %q", created.Alias)
	}

	mustSetOrchestratorAlias(t, app, created.ID, alias)

	listed := app.ListOrchestrators()
	if len(listed) != 1 {
		t.Fatalf("expected one orchestrator, got %+v", listed)
	}
	if listed[0].Alias != alias {
		t.Fatalf("the read model the dialog seeds from reported alias %q, want %q", listed[0].Alias, alias)
	}

	updated := mustUpdateOrchestrator(t, app, created.ID, "renamed", devEnvs, listed[0].Alias)
	if updated.Alias != alias || updated.Name == "agent" {
		t.Fatalf("update returned %+v, want the new name and the kept alias", updated)
	}

	config := mustLoadRootConfig(t, app)
	if len(config.Orchestrators) != 1 || config.Orchestrators[0].Alias != alias {
		t.Fatalf("the alias did not survive the edit: %+v", config.Orchestrators)
	}
}

func mustCreateOrchestrator(t *testing.T, app *App, name string, envs []orchestratorEnvInput, alias string) orchestratorInfo {
	t.Helper()
	info, err := app.CreateOrchestrator(name, envs, nil, alias, orchestratorLaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator(%q) failed: %v", name, err)
	}
	return info
}

func mustUpdateOrchestrator(t *testing.T, app *App, id, name string, envs []orchestratorEnvInput, alias string) orchestratorInfo {
	t.Helper()
	info, err := app.UpdateOrchestrator(id, name, envs, nil, alias, orchestratorLaunchInput{})
	if err != nil {
		t.Fatalf("UpdateOrchestrator(%q) failed: %v", id, err)
	}
	return info
}

func mustSetOrchestratorAlias(t *testing.T, app *App, id, alias string) {
	t.Helper()
	if _, err := orchestrator.SetOrchestratorAlias(core.Context{}, app.deps.store, model.SetOrchestratorAliasParams{
		OrchestratorID: id,
		Alias:          alias,
	}); err != nil {
		t.Fatalf("SetOrchestratorAlias(%q) failed: %v", id, err)
	}
}

func mustLoadRootConfig(t *testing.T, app *App) configpkg.ERunConfig {
	t.Helper()
	config, _, err := app.deps.store.LoadERunConfig()
	if err != nil {
		t.Fatalf("LoadERunConfig failed: %v", err)
	}
	return config
}

func TestARunningOrchestratorsAliasReachesTheDialogItIsEditedFrom(t *testing.T) {
	app := orchestratorAliasTestApp(t)
	defer app.shutdown(context.Background())

	const alias = "erun+erunpaas.com@erun"
	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, alias, orchestratorLaunchInput{})
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

func TestTheDesktopRefusesAnAliasThisHostCannotResolve(t *testing.T) {
	app := orchestratorAliasTestApp(t)
	defer app.shutdown(context.Background())

	_, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "erun+elsewhere.example@erun", orchestratorLaunchInput{})
	if err == nil {
		t.Fatal("CreateOrchestrator accepted an alias this host does not have configured")
	}
	if !strings.Contains(err.Error(), "cloud provider alias") {
		t.Fatalf("error %v does not name the alias it could not resolve", err)
	}

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "erun+erunpaas.com@erun", orchestratorLaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator with a configured alias failed: %v", err)
	}
	if _, err := app.UpdateOrchestrator(created.ID, "agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "aws-prod", orchestratorLaunchInput{}); err == nil {
		t.Fatal("UpdateOrchestrator accepted a non-erun alias")
	}
}

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
