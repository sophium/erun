package main

import (
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

func TestLoadEnvironmentConfigExposesClaudeDefaultsAndOverrides(t *testing.T) {
	projectRoot := t.TempDir()
	useBedrock := false
	maxTokens := 8192
	store := stubUIStore{
		tenants: map[string]configpkg.TenantConfig{
			"frs": {Name: "frs"},
		},
		envs: map[string]configpkg.EnvConfig{
			"frs/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-local",
				Claude: configpkg.EnvironmentClaudeConfig{
					UseBedrock:      &useBedrock,
					Models:          []string{"opus", "sonnet"},
					MaxOutputTokens: &maxTokens,
				},
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	got, err := app.LoadEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "local"})
	if err != nil {
		t.Fatalf("LoadEnvironmentConfig failed: %v", err)
	}

	assertClaudeOverrides(t, got.Claude)
	assertClaudeDefaults(t, got.ClaudeDefaults)
}

func assertClaudeOverrides(t *testing.T, claude uiClaudeConfig) {
	t.Helper()

	if claude.UseBedrock == nil || *claude.UseBedrock {
		t.Fatalf("expected Claude.UseBedrock=false, got %+v", claude)
	}
	if claude.UseMantle != nil {
		t.Fatalf("expected Claude.UseMantle to remain unset, got %+v", claude)
	}
	if claude.MaxOutputTokens == nil || *claude.MaxOutputTokens != 8192 {
		t.Fatalf("expected Claude.MaxOutputTokens=8192, got %+v", claude)
	}
	if !equalStringSlices(claude.Models, []string{"opus", "sonnet"}) {
		t.Fatalf("expected Claude.Models=[opus sonnet], got %+v", claude.Models)
	}
}

func assertClaudeDefaults(t *testing.T, defaults uiClaudeDefaults) {
	t.Helper()

	if defaults.MaxOutputTokens != configpkg.DefaultClaudeMaxOutputTokens {
		t.Fatalf("expected default max output tokens, got %d", defaults.MaxOutputTokens)
	}
	if !equalStringSlices(defaults.Models, configpkg.DefaultClaudeAvailableModels()) {
		t.Fatalf("expected default models, got %+v", defaults.Models)
	}
	if !equalStringSlices(defaults.KnownModels, configpkg.KnownClaudeModels()) {
		t.Fatalf("expected known models list, got %+v", defaults.KnownModels)
	}
}

func TestSaveEnvironmentConfigRoundTripsClaudeOverrides(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]configpkg.TenantConfig{
			"frs": {Name: "frs"},
		},
		envs: map[string]configpkg.EnvConfig{
			"frs/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-local",
				RuntimePod: configpkg.RuntimePodResources{
					CPU:    "4",
					Memory: "8916Mi",
				},
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	useMantle := false
	maxTokens := 16384
	saved, err := app.SaveEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "local"}, uiEnvironmentConfig{
		Name:              "local",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
		RuntimePod: uiRuntimePodConfig{
			CPU:    "4",
			Memory: "8916Mi",
		},
		Idle: uiIdleConfig{
			Timeout:      configpkg.DefaultEnvironmentIdleTimeout.String(),
			WorkingHours: configpkg.DefaultEnvironmentWorkingHours,
		},
		Claude: uiClaudeConfig{
			UseMantle:       &useMantle,
			Models:          []string{"opus"},
			MaxOutputTokens: &maxTokens,
		},
	})
	if err != nil {
		t.Fatalf("SaveEnvironmentConfig failed: %v", err)
	}

	if saved.Claude.UseMantle == nil || *saved.Claude.UseMantle {
		t.Fatalf("expected saved UseMantle=false, got %+v", saved.Claude)
	}
	if saved.Claude.UseBedrock != nil {
		t.Fatalf("expected saved UseBedrock unset, got %+v", saved.Claude)
	}
	stored := store.envs["frs/local"].Claude
	if stored.UseMantle == nil || *stored.UseMantle {
		t.Fatalf("expected stored UseMantle=false, got %+v", stored)
	}
	if stored.MaxOutputTokens == nil || *stored.MaxOutputTokens != 16384 {
		t.Fatalf("expected stored MaxOutputTokens=16384, got %+v", stored)
	}
	if !equalStringSlices(stored.Models, []string{"opus"}) {
		t.Fatalf("expected stored Models=[opus], got %+v", stored.Models)
	}
}

// TestSaveEnvironmentConfigRoundTripsClaudeLaunchFlags pins the desktop round-
// trip of the per-env Claude launch fields (default model, verbose+debug).
func TestSaveEnvironmentConfigRoundTripsClaudeLaunchFlags(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]configpkg.TenantConfig{
			"frs": {Name: "frs"},
		},
		envs: map[string]configpkg.EnvConfig{
			"frs/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-local",
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	defaultModel := "fable"
	saved, err := app.SaveEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "local"}, uiEnvironmentConfig{
		Name:              "local",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
		Idle: uiIdleConfig{
			Timeout:      configpkg.DefaultEnvironmentIdleTimeout.String(),
			WorkingHours: configpkg.DefaultEnvironmentWorkingHours,
		},
		Claude: uiClaudeConfig{
			Models:       []string{"opus", "fable"},
			DefaultModel: &defaultModel,
			VerboseDebug: true,
		},
	})
	if err != nil {
		t.Fatalf("SaveEnvironmentConfig failed: %v", err)
	}

	if saved.Claude.DefaultModel == nil || *saved.Claude.DefaultModel != "fable" {
		t.Fatalf("expected saved DefaultModel=fable, got %+v", saved.Claude)
	}
	if !saved.Claude.VerboseDebug {
		t.Fatalf("expected saved VerboseDebug=true, got %+v", saved.Claude)
	}
	stored := store.envs["frs/local"].Claude
	if stored.DefaultModel == nil || *stored.DefaultModel != "fable" {
		t.Fatalf("expected stored DefaultModel=fable, got %+v", stored)
	}
	if !stored.VerboseDebug {
		t.Fatalf("expected stored VerboseDebug=true, got %+v", stored)
	}
}

func TestSetEnvironmentAutoStartPersistsTriStateValue(t *testing.T) {
	// AutoStart is the desktop's per-env auto-start gate. The three modes
	// map to *bool: ask=nil (prompt on next open), always=true, never=false.
	// SetEnvironmentAutoStart must round-trip each mode through the store
	// without rewriting unrelated fields.
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]configpkg.TenantConfig{
			"frs": {Name: "frs"},
		},
		envs: map[string]configpkg.EnvConfig{
			"frs/prod": {
				Name:                "prod",
				LocalRepoPath:       projectRoot,
				KubernetesContext:   "cluster-prod",
				ContainerRegistries: configpkg.SingleContainerRegistries("registry.example/keep"),
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	never := false
	saved := setAutoStartMode(t, app, "never")
	assertReturnedAutoStart(t, saved.AutoStart, &never, "false")
	assertStoredAutoStart(t, store.envs["frs/prod"].AutoStart, &never, "false")
	if keptRegistry, _ := store.envs["frs/prod"].ContainerRegistries.BuildRegistry(); keptRegistry != "registry.example/keep" {
		t.Fatalf("SetEnvironmentAutoStart must not rewrite unrelated fields, got %+v", store.envs["frs/prod"])
	}

	always := true
	saved = setAutoStartMode(t, app, "always")
	assertReturnedAutoStart(t, saved.AutoStart, &always, "true")

	saved = setAutoStartMode(t, app, "ask")
	assertReturnedAutoStart(t, saved.AutoStart, nil, "nil after ask")
	assertStoredAutoStart(t, store.envs["frs/prod"].AutoStart, nil, "nil after ask")

	if _, err := app.SetEnvironmentAutoStart(uiSelection{Tenant: "frs", Environment: "prod"}, "bogus"); err == nil {
		t.Fatal("expected unknown auto-start mode to be rejected")
	}
}

func setAutoStartMode(t *testing.T, app *App, mode string) uiEnvironmentConfig {
	t.Helper()

	saved, err := app.SetEnvironmentAutoStart(uiSelection{Tenant: "frs", Environment: "prod"}, mode)
	if err != nil {
		t.Fatalf("SetEnvironmentAutoStart(%s) failed: %v", mode, err)
	}
	return saved
}

func assertReturnedAutoStart(t *testing.T, got, want *bool, label string) {
	t.Helper()

	if !equalBoolPtr(got, want) {
		t.Fatalf("expected returned AutoStart=%s, got %+v", label, got)
	}
}

func assertStoredAutoStart(t *testing.T, got, want *bool, label string) {
	t.Helper()

	if !equalBoolPtr(got, want) {
		t.Fatalf("expected stored AutoStart=%s, got %+v", label, got)
	}
}

func equalBoolPtr(a, b *bool) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
