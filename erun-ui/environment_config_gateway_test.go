package main

import (
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

func TestEnvironmentConfigGatewayChoiceReachesTheLaunch(t *testing.T) {
	store := stubUIStore{
		config: &configpkg.ERunConfig{OpenRouter: &configpkg.OpenRouterConfig{
			BaseURL:      "https://gateway.example/api",
			DefaultModel: "vendor/model-x",
			Models:       []configpkg.OpenRouterModel{{ID: "vendor/model-x"}},
		}},
		tenants: map[string]configpkg.TenantConfig{"frs": {Name: "frs"}},
		envs: map[string]configpkg.EnvConfig{
			"frs/local": {Name: "local", LocalRepoPath: t.TempDir(), KubernetesContext: "cluster-local"},
		},
	}
	app := NewApp(erunUIDeps{store: store})
	selection := uiSelection{Tenant: "frs", Environment: "local"}

	onGateway, err := app.LoadEnvironmentConfig(selection)
	if err != nil {
		t.Fatalf("LoadEnvironmentConfig failed: %v", err)
	}
	assertGatewayModelChoices(t, onGateway)

	off := false
	opus := "opus"
	edited := onGateway
	edited.Claude.UseGateway = &off
	edited.Claude.DefaultModel = &opus
	saved, err := app.SaveEnvironmentConfig(selection, edited)
	if err != nil {
		t.Fatalf("SaveEnvironmentConfig failed: %v", err)
	}
	if stored := store.envs["frs/local"].Claude.UseGateway; stored == nil || *stored {
		t.Fatalf("the opt-out should reach the env config, got %v", stored)
	}
	assertLeftGatewayLaunch(t, onGateway, saved)
}

func assertGatewayModelChoices(t *testing.T, config uiEnvironmentConfig) {
	t.Helper()
	defaults := config.ClaudeDefaults
	if !defaults.GatewayConfigured || !equalStringSlices(defaults.GatewayModels, []string{"vendor/model-x"}) || defaults.GatewayDefaultModel != "vendor/model-x" {
		t.Fatalf("the catalog's models should be offered beside the environment's own, got %+v", defaults)
	}
	if !equalStringSlices(defaults.Models, configpkg.DefaultClaudeAvailableModels()) {
		t.Fatalf("an environment's own models must not be replaced by the catalog, got %+v", defaults.Models)
	}
	if !strings.Contains(config.AILaunchSignature, "--model vendor/model-x") {
		t.Fatalf("an environment on the gateway should launch the catalog's model, got %q", config.AILaunchSignature)
	}
}

func assertLeftGatewayLaunch(t *testing.T, before, saved uiEnvironmentConfig) {
	t.Helper()
	if saved.Claude.UseGateway == nil || *saved.Claude.UseGateway {
		t.Fatalf("the saved config should carry the opt-out back, got %+v", saved.Claude)
	}
	if !strings.Contains(saved.AILaunchSignature, "--model opus") || !strings.Contains(saved.AILaunchSignature, "--remote-control") {
		t.Fatalf("an environment that left the gateway should launch its own model on its own sign-in, got %q", saved.AILaunchSignature)
	}
	if saved.AILaunchSignature == before.AILaunchSignature {
		t.Fatal("leaving the gateway must change the launch signature, or the open AI tabs keep the gateway launch")
	}
}
