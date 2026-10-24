package main

import (
	"context"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	activitypkg "github.com/sophium/erun/erun-common/infra/activity"
)

func TestEnvironmentBusyFromIdleStatus(t *testing.T) {
	activeMarker := func(name string) model.EnvironmentIdleMarker {
		return model.EnvironmentIdleMarker{Name: name, Idle: false, Reason: "recent activity"}
	}
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name       string
		status     model.EnvironmentIdleStatus
		wantBusy   bool
		wantDetail string
	}{
		{
			name: "a quiet environment is not busy",
			status: model.EnvironmentIdleStatus{Markers: []model.EnvironmentIdleMarker{
				{Name: "working-hours", Idle: false},
				{Name: config.ActivityKindMCP, Idle: true},
			}},
		},
		{
			// working-hours is inverted, so counting it would make every env busy all day.
			name: "inside working hours alone is not activity",
			status: model.EnvironmentIdleStatus{Markers: []model.EnvironmentIdleMarker{
				activeMarker("working-hours"),
			}},
		},
		{
			name: "a held lease names the work",
			status: model.EnvironmentIdleStatus{
				Leases: []model.EnvironmentActivityLease{
					{ID: "gradle-build", Name: "gradle-build", StartedAt: now, ExpiresAt: now.Add(time.Hour)},
				},
				Markers: []model.EnvironmentIdleMarker{activeMarker("lease")},
			},
			wantBusy:   true,
			wantDetail: "holding: gradle-build",
		},
		{
			name: "a lease wins over a generic marker",
			status: model.EnvironmentIdleStatus{
				Leases: []model.EnvironmentActivityLease{
					{ID: "agent-run", Name: "agent-run", StartedAt: now, ExpiresAt: now.Add(time.Hour)},
				},
				Markers: []model.EnvironmentIdleMarker{activeMarker(config.ActivityKindMCP)},
			},
			wantBusy:   true,
			wantDetail: "holding: agent-run",
		},
		{
			name: "sampled processes describe themselves",
			status: model.EnvironmentIdleStatus{Markers: []model.EnvironmentIdleMarker{
				activeMarker(activitypkg.ActivityKindProcess),
			}},
			wantBusy:   true,
			wantDetail: "running build or agent processes",
		},
		{
			name: "an agent over MCP is named",
			status: model.EnvironmentIdleStatus{Markers: []model.EnvironmentIdleMarker{
				activeMarker(config.ActivityKindMCP),
			}},
			wantBusy:   true,
			wantDetail: "an agent is driving it over MCP",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			busy, detail := environmentBusyFromIdleStatus(testCase.status)
			if busy != testCase.wantBusy || detail != testCase.wantDetail {
				t.Errorf("got busy=%v detail=%q, want busy=%v detail=%q", busy, detail, testCase.wantBusy, testCase.wantDetail)
			}
		})
	}
}

// A frontend reset without a Go restart must keep a busy env's activity.
func TestSeedEnvironmentActivitySnapshotsCarriesTheLastObservation(t *testing.T) {
	app := &App{envActivity: map[string]environmentActivityState{
		selectionKey(uiSelection{Tenant: "acme", Environment: "dev"}): {
			reachable: true,
			observed:  true,
			busy:      true,
			detail:    "an agent is driving it over MCP",
		},
	}}
	state := uiState{Tenants: []uiTenant{{Name: "acme", Environments: []uiEnvironment{{Name: "dev"}}}}}

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
	app := &App{}
	state := uiState{Tenants: []uiTenant{{Name: "acme", Environments: []uiEnvironment{{Name: "dev"}}}}}

	app.seedEnvironmentActivitySnapshots(&state)

	if state.Tenants[0].Environments[0].Activity != nil {
		t.Fatalf("expected no activity snapshot, got %+v", state.Tenants[0].Environments[0].Activity)
	}
}

func TestEnvActivitySnapshotCopiesRatherThanAliases(t *testing.T) {
	key := selectionKey(uiSelection{Tenant: "acme", Environment: "dev"})
	app := &App{envActivity: map[string]environmentActivityState{key: {busy: true, detail: "gradle-build"}}}

	snapshot := app.envActivitySnapshot()
	if len(snapshot) != 1 || !snapshot[key].busy {
		t.Fatalf("expected the snapshot to carry the observation, got %+v", snapshot)
	}
	app.envActivity[key] = environmentActivityState{}
	if !snapshot[key].busy {
		t.Fatalf("expected the snapshot to be a copy, unaffected by a later mutation of a.envActivity")
	}
}

func TestEnvActivitySnapshotNilWhenEmpty(t *testing.T) {
	app := &App{}
	if snapshot := app.envActivitySnapshot(); snapshot != nil {
		t.Fatalf("expected a nil snapshot for an empty poller state, got %+v", snapshot)
	}
}

func TestResetEnvironmentActivityObservationsClearsTheCache(t *testing.T) {
	key := selectionKey(uiSelection{Tenant: "acme", Environment: "dev"})
	app := &App{envActivity: map[string]environmentActivityState{key: {busy: true, detail: "gradle-build"}}}

	app.ResetEnvironmentActivityObservations()

	if snapshot := app.envActivitySnapshot(); snapshot != nil {
		t.Fatalf("expected no cached observations after reset, got %+v", snapshot)
	}
}

func TestResetEnvironmentActivityObservationsOnEmptyCacheIsANoop(t *testing.T) {
	app := &App{}
	app.ResetEnvironmentActivityObservations()
	if snapshot := app.envActivitySnapshot(); snapshot != nil {
		t.Fatalf("expected no cached observations, got %+v", snapshot)
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
	app.envActivity = map[string]environmentActivityState{
		selectionKey(uiSelection{Tenant: "acme", Environment: "dev"}): {
			reachable: true,
			observed:  true,
			busy:      true,
			detail:    "an agent is driving it over MCP",
		},
	}

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

func TestTriggerEnvironmentActivitySweepRunsSynchronously(t *testing.T) {
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{"acme": {Name: "acme"}},
		envs:    map[string]config.EnvConfig{"acme/dev": {Name: "dev"}},
	}
	app := NewApp(erunUIDeps{store: store})
	t.Cleanup(func() { app.shutdown(context.Background()) })
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	app.TriggerEnvironmentActivitySweep()

	if len(emits.events(envActivityEvent)) == 0 {
		t.Fatal("triggering a sweep must observe and emit for every configured selection before returning")
	}
}
