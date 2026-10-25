package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-ui/internal/runtimeview"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// seedTestApp keeps the usage history in the test's own directory.
func seedTestApp(t *testing.T) *App {
	t.Helper()
	return NewApp(erunUIDeps{
		store:                       stubUIStore{},
		environmentUsageHistoryPath: filepath.Join(t.TempDir(), "environment-usage-history.json"),
	})
}

func seedTestState() uiState {
	return uiState{Tenants: []uiTenant{{Name: "acme", Environments: []uiEnvironment{{Name: "dev"}}}}}
}

// A frontend reset without a Go restart must keep a busy env's activity.
func TestSeedEnvironmentActivitySnapshotsCarriesTheLastObservation(t *testing.T) {
	app := seedTestApp(t)
	app.runtime.PublishActivity(uimodel.Selection{Tenant: "acme", Environment: "dev"}, runtimeview.ActivityState{
		Reachable: true,
		Observed:  true,
		Busy:      true,
		Detail:    "an agent is driving it over MCP",
	})
	state := seedTestState()

	app.seedEnvironmentActivitySnapshots(&state)

	activity := state.Tenants[0].Environments[0].Activity
	if activity == nil {
		t.Fatal("expected the busy observation to be seeded onto the env")
	}
	if !activity.Reachable || !activity.Observed || !activity.Busy || activity.Detail != "an agent is driving it over MCP" {
		t.Fatalf("unexpected snapshot: %+v", activity)
	}
}

func TestSeedEnvironmentActivitySnapshotsLeavesUnobservedEnvsNil(t *testing.T) {
	app := seedTestApp(t)
	state := seedTestState()

	app.seedEnvironmentActivitySnapshots(&state)

	if state.Tenants[0].Environments[0].Activity != nil {
		t.Fatalf("expected no activity snapshot, got %+v", state.Tenants[0].Environments[0].Activity)
	}
}

func TestLoadStateSeedsEnvironmentActivityFromThePoller(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"acme": {Name: "acme", DefaultEnvironment: "dev"},
		},
		envs: map[string]config.EnvConfig{
			"acme/dev": {Name: "dev", LocalRepoPath: projectRoot},
		},
	}
	app := NewApp(erunUIDeps{
		store:            store,
		findProjectRoot:  func() (string, string, error) { return "acme", projectRoot, nil },
		resolveBuildInfo: func() model.BuildInfo { return model.BuildInfo{Version: "1.0.0"} },
		resolveImageRegistry: func(context.Context, string, string) (model.RuntimeRegistryVersions, error) {
			return model.RuntimeRegistryVersions{}, nil
		},
	})
	app.runtime.PublishActivity(uimodel.Selection{Tenant: "acme", Environment: "dev"}, runtimeview.ActivityState{
		Reachable: true,
		Observed:  true,
		Busy:      true,
		Detail:    "an agent is driving it over MCP",
	})

	state, err := app.LoadState()
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}
	if len(state.Tenants) != 1 || len(state.Tenants[0].Environments) != 1 {
		t.Fatalf("unexpected state shape: %+v", state)
	}
	activity := state.Tenants[0].Environments[0].Activity
	if activity == nil || !activity.Busy {
		t.Fatalf("expected LoadState to seed the busy observation, got %+v", activity)
	}
}

func TestSeedEnvironmentUsageSnapshotsCarriesTheLastReading(t *testing.T) {
	observedAt := time.Now().Add(-5 * time.Minute)
	app := seedTestApp(t)
	app.runtime.PublishUsage(uimodel.Selection{Tenant: "acme", Environment: "dev"}, runtimeview.UsageReading{
		Usage:      runtimeview.Usage{Available: true, CPU: runtimeview.CPUUsage{Available: true, Utilization: "42.0%"}},
		ObservedAt: observedAt,
	})
	state := seedTestState()

	app.seedEnvironmentUsageSnapshots(&state)

	usage := state.Tenants[0].Environments[0].Usage
	if usage == nil {
		t.Fatal("expected the cached reading to be seeded onto the env")
	}
	if usage.Usage.CPU.Utilization != "42.0%" {
		t.Fatalf("expected the reading to survive the seed, got %+v", usage.Usage)
	}
	if usage.ObservedAtUnix != observedAt.Unix() {
		t.Fatalf("expected ObservedAtUnix=%d, got %d", observedAt.Unix(), usage.ObservedAtUnix)
	}
}

func TestSeedEnvironmentUsageSnapshotsLeavesUnobservedEnvsNil(t *testing.T) {
	app := seedTestApp(t)
	state := seedTestState()

	app.seedEnvironmentUsageSnapshots(&state)

	if state.Tenants[0].Environments[0].Usage != nil {
		t.Fatalf("expected no usage snapshot, got %+v", state.Tenants[0].Environments[0].Usage)
	}
}

// Through NewApp, the real restart path: a reading persisted before a restart
// is seeded onto the read model with its true age.
func TestEnvironmentUsageHistorySurvivesARestartIntoTheReadModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "environment-usage-history.json")
	selection := uimodel.Selection{Tenant: "acme", Environment: "dev"}
	reading := runtimeview.UsageReading{
		Usage:      runtimeview.Usage{Available: true, CPU: runtimeview.CPUUsage{Available: true, Utilization: "17.0%"}},
		ObservedAt: time.Now().Add(-10 * time.Minute).Truncate(time.Second),
	}
	NewApp(erunUIDeps{store: stubUIStore{}, environmentUsageHistoryPath: path}).runtime.PublishUsage(selection, reading)

	after := NewApp(erunUIDeps{store: stubUIStore{}, environmentUsageHistoryPath: path})
	state := seedTestState()
	after.seedEnvironmentUsageSnapshots(&state)

	usage := state.Tenants[0].Environments[0].Usage
	if usage == nil {
		t.Fatal("expected seedEnvironmentUsageSnapshots to attach the restored reading")
	}
	if usage.Usage.CPU.Utilization != "17.0%" || usage.ObservedAtUnix != reading.ObservedAt.Unix() {
		t.Fatalf("expected the restored reading with its real age, got %+v", usage)
	}
}
