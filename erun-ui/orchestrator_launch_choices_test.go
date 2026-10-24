package main

import (
	"context"
	"errors"
	"slices"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/orchestrator"
)

func requireDefaultLaunchChoices(t *testing.T, choices orchestratorLaunchChoices) {
	t.Helper()
	wantLevels := []string{"low", "medium", "high", "xhigh", "max", "ultracode"}
	if choices.DefaultModel != "opus" || choices.DefaultEffort != "ultracode" || !slices.Equal(choices.EffortLevels, wantLevels) {
		t.Fatalf("unexpected defaults: %+v", choices)
	}
}

func TestListOrchestratorLaunchChoices(t *testing.T) {
	app, _ := launchSettingsTestApp(t)
	defer app.shutdown(context.Background())

	choices, err := app.ListOrchestratorLaunchChoices()
	mustNoErr(t, err, "ListOrchestratorLaunchChoices")
	if choices.GatewayConfigured || choices.GatewayModels == nil || len(choices.GatewayModels) != 0 || choices.GatewayDefaultModel != "" {
		t.Fatalf("expected no gateway and an empty (not null) model list, got %+v", choices)
	}
	requireDefaultLaunchChoices(t, choices)

	stageGatewayCatalog(app)
	choices, err = app.ListOrchestratorLaunchChoices()
	mustNoErr(t, err, "ListOrchestratorLaunchChoices")
	if !choices.GatewayConfigured || !slices.Equal(choices.GatewayModels, []string{"acme/coder"}) || choices.GatewayDefaultModel != "acme/coder" {
		t.Fatalf("expected the catalog's driveable models only, got %+v", choices)
	}
}

func TestTheRestartFlagComparesWhatTheSessionRunsOn(t *testing.T) {
	t.Run("naming the defaults explicitly needs no restart", func(t *testing.T) {
		app, _ := launchSettingsTestApp(t)
		defer app.shutdown(context.Background())
		created := mustCreateOrchestratorWithLaunch(t, app, orchestratorLaunchInput{})
		_, err := app.StartOrchestrator(created.ID, 80, 24)
		mustNoErr(t, err, "StartOrchestrator")

		_, err = orchestrator.SetOrchestratorModel(core.Context{}, app.deps.store, model.SetOrchestratorModelParams{OrchestratorID: created.ID, Model: "opus"})
		mustNoErr(t, err, "SetOrchestratorModel")
		requireLoneLaunchSettingsRow(t, app, "after set-model opus", orchestratorLaunchInput{Model: "opus"}, false)

		_, err = orchestrator.SetOrchestratorEffort(core.Context{}, app.deps.store, model.SetOrchestratorEffortParams{OrchestratorID: created.ID, Effort: "ultracode"})
		mustNoErr(t, err, "SetOrchestratorEffort")
		explicit := orchestratorLaunchInput{Model: "opus", Effort: "ultracode"}
		requireLoneLaunchSettingsRow(t, app, "after set-effort ultracode", explicit, false)
		again, err := app.StartOrchestrator(created.ID, 80, 24)
		mustNoErr(t, err, "StartOrchestrator on a running orchestrator")
		requireLaunchSettingsRow(t, "running snapshot", again, explicit, false)
	})

	t.Run("a moved catalog default asks for a restart", func(t *testing.T) {
		app, rec := launchSettingsTestApp(t)
		defer app.shutdown(context.Background())
		stageGatewayCatalog(app)
		devEnvs := []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}
		gateway := orchestratorLaunchInput{UseGateway: true}
		created := mustCreateOrchestratorWithLaunch(t, app, gateway)
		_, err := app.StartOrchestrator(created.ID, 80, 24)
		mustNoErr(t, err, "StartOrchestrator")
		if launch, _, _ := rec.last(t); launch.Model != "acme/coder" {
			t.Fatalf("expected the catalog default, got %q", launch.Model)
		}
		requireLoneLaunchSettingsRow(t, app, "before the catalog moved", gateway, false)

		stubStore(app).config.OpenRouter = &configpkg.OpenRouterConfig{
			BaseURL:      testGatewayURL,
			DefaultModel: "acme/other",
			Models:       []configpkg.OpenRouterModel{{ID: "acme/coder"}, {ID: "acme/other"}},
		}
		requireLoneLaunchSettingsRow(t, app, "after the catalog default moved", gateway, true)
		again, err := app.StartOrchestrator(created.ID, 80, 24)
		mustNoErr(t, err, "StartOrchestrator on a running orchestrator")
		requireLaunchSettingsRow(t, "running snapshot", again, gateway, true)
		updated, err := app.UpdateOrchestrator(created.ID, "agent", devEnvs, nil, "", gateway)
		mustNoErr(t, err, "UpdateOrchestrator")
		requireLaunchSettingsRow(t, "save while running", updated, gateway, true)

		restarted, err := app.RestartOrchestrator(created.ID, 80, 24)
		mustNoErr(t, err, "RestartOrchestrator")
		requireLaunchSettingsRow(t, "after restart", restarted, gateway, false)
		requireLoneLaunchSettingsRow(t, app, "polled after restart", gateway, false)
	})

	t.Run("a moved gateway endpoint asks for a restart", func(t *testing.T) {
		app, _ := launchSettingsTestApp(t)
		defer app.shutdown(context.Background())
		stageGatewayCatalog(app)
		gateway := orchestratorLaunchInput{UseGateway: true, Model: "acme/coder"}
		created := mustCreateOrchestratorWithLaunch(t, app, gateway)
		_, err := app.StartOrchestrator(created.ID, 80, 24)
		mustNoErr(t, err, "StartOrchestrator")
		stubStore(app).config.OpenRouter = &configpkg.OpenRouterConfig{
			BaseURL: "https://other-gateway.example.test",
			Models:  []configpkg.OpenRouterModel{{ID: "acme/coder"}},
		}
		requireLoneLaunchSettingsRow(t, app, "after the endpoint moved", gateway, true)
	})

	t.Run("a definition that no longer resolves asks for a restart", func(t *testing.T) {
		app, _ := launchSettingsTestApp(t)
		defer app.shutdown(context.Background())
		stageGatewayCatalog(app)
		gateway := orchestratorLaunchInput{UseGateway: true}
		created := mustCreateOrchestratorWithLaunch(t, app, gateway)
		_, err := app.StartOrchestrator(created.ID, 80, 24)
		mustNoErr(t, err, "StartOrchestrator")
		stubStore(app).config.OpenRouter = nil
		requireLoneLaunchSettingsRow(t, app, "after the catalog was removed", gateway, true)
	})
}

// "default" is the CLI's spelling of the empty default; stored it would launch `--model default`.
func TestTheDialogNeverStoresTheClearingToken(t *testing.T) {
	app, _ := launchSettingsTestApp(t)
	defer app.shutdown(context.Background())
	devEnvs := []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}
	created := mustCreateOrchestratorWithLaunch(t, app, orchestratorLaunchInput{Model: "Default", Effort: " DEFAULT "})
	requireLaunchSettingsRow(t, "create", created, orchestratorLaunchInput{}, false)
	requireStoredLaunchSettings(t, app, "after create", orchestratorLaunchInput{})

	_, err := app.UpdateOrchestrator(created.ID, "agent", devEnvs, nil, "", orchestratorLaunchInput{Model: "sonnet", Effort: "high"})
	mustNoErr(t, err, "UpdateOrchestrator")
	updated, err := app.UpdateOrchestrator(created.ID, "agent", devEnvs, nil, "", orchestratorLaunchInput{Model: "default", Effort: "default"})
	mustNoErr(t, err, "UpdateOrchestrator back to default")
	requireLaunchSettingsRow(t, "update", updated, orchestratorLaunchInput{}, false)
	requireStoredLaunchSettings(t, app, "after update", orchestratorLaunchInput{})
}

func TestLaunchChoicesNameTheCatalogsUndriveableModels(t *testing.T) {
	app, _ := launchSettingsTestApp(t)
	defer app.shutdown(context.Background())
	choices, err := app.ListOrchestratorLaunchChoices()
	mustNoErr(t, err, "ListOrchestratorLaunchChoices")
	if choices.GatewayUndriveableModels == nil || len(choices.GatewayUndriveableModels) != 0 {
		t.Fatalf("expected an empty (not null) undriveable list with no catalog, got %#v", choices.GatewayUndriveableModels)
	}

	stubStore(app).config.OpenRouter = &configpkg.OpenRouterConfig{
		BaseURL: testGatewayURL,
		Models: []configpkg.OpenRouterModel{
			{ID: "acme/coder"},
			{ID: "acme/thinker", RequiresReasoningEcho: true},
			{ID: " acme/thinker ", RequiresReasoningEcho: true},
			{ID: "deepseek/deepseek-v4.1-flash"},
		},
	}
	choices, err = app.ListOrchestratorLaunchChoices()
	mustNoErr(t, err, "ListOrchestratorLaunchChoices")
	if want := []string{"acme/thinker", "deepseek/deepseek-v4.1-flash"}; !slices.Equal(choices.GatewayUndriveableModels, want) {
		t.Fatalf("expected the declared and the known undriveable entries %q, got %q", want, choices.GatewayUndriveableModels)
	}
	if !slices.Equal(choices.GatewayModels, []string{"acme/coder"}) {
		t.Fatalf("expected only the driveable entry offered, got %q", choices.GatewayModels)
	}
}

type unreadableRootConfigStore struct{ stubUIStore }

func (unreadableRootConfigStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return configpkg.ERunConfig{}, "", errors.New("permission denied")
}

func TestLaunchChoicesHeadlineWhatCouldNotBeRead(t *testing.T) {
	app, _ := launchSettingsTestApp(t)
	defer app.shutdown(context.Background())
	app.deps.store = unreadableRootConfigStore{stubStore(app)}
	_, err := app.ListOrchestratorLaunchChoices()
	if err == nil || err.Error() != "cannot read the gateway catalog from the ERun settings: permission denied" {
		t.Fatalf("expected one headline naming what could not be read before the cause, got %v", err)
	}
}
