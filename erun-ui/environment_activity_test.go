package main

import (
	"context"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// The sidebar's busy line comes from the environment's own idle markers, so the
// reduction from markers to "what is keeping it busy" is what has to be right:
// a held lease is the only signal that names the work, and everything else must
// read in the operator's language rather than by its wire name.

func TestEnvironmentBusyFromIdleStatus(t *testing.T) {
	activeMarker := func(name string) eruncommon.EnvironmentIdleMarker {
		return eruncommon.EnvironmentIdleMarker{Name: name, Idle: false, Reason: "recent activity"}
	}
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name       string
		status     eruncommon.EnvironmentIdleStatus
		wantBusy   bool
		wantDetail string
	}{
		{
			name: "a quiet environment is not busy",
			status: eruncommon.EnvironmentIdleStatus{Markers: []eruncommon.EnvironmentIdleMarker{
				{Name: "working-hours", Idle: false},
				{Name: eruncommon.ActivityKindMCP, Idle: true},
			}},
		},
		{
			// working-hours is never activity — it is inverted (not idle means
			// "inside working hours"), so counting it would make every
			// environment busy all day.
			name: "inside working hours alone is not activity",
			status: eruncommon.EnvironmentIdleStatus{Markers: []eruncommon.EnvironmentIdleMarker{
				activeMarker("working-hours"),
			}},
		},
		{
			name: "a held lease names the work",
			status: eruncommon.EnvironmentIdleStatus{
				Leases: []eruncommon.EnvironmentActivityLease{
					{ID: "gradle-build", Name: "gradle-build", StartedAt: now, ExpiresAt: now.Add(time.Hour)},
				},
				Markers: []eruncommon.EnvironmentIdleMarker{activeMarker("lease")},
			},
			wantBusy:   true,
			wantDetail: "holding: gradle-build",
		},
		{
			// The lease is preferred over a bare marker precisely because it is
			// the one signal that can say what the work is.
			name: "a lease wins over a generic marker",
			status: eruncommon.EnvironmentIdleStatus{
				Leases: []eruncommon.EnvironmentActivityLease{
					{ID: "agent-run", Name: "agent-run", StartedAt: now, ExpiresAt: now.Add(time.Hour)},
				},
				Markers: []eruncommon.EnvironmentIdleMarker{activeMarker(eruncommon.ActivityKindMCP)},
			},
			wantBusy:   true,
			wantDetail: "holding: agent-run",
		},
		{
			name: "sampled processes describe themselves",
			status: eruncommon.EnvironmentIdleStatus{Markers: []eruncommon.EnvironmentIdleMarker{
				activeMarker(eruncommon.ActivityKindProcess),
			}},
			wantBusy:   true,
			wantDetail: "running build or agent processes",
		},
		{
			name: "an agent over MCP is named",
			status: eruncommon.EnvironmentIdleStatus{Markers: []eruncommon.EnvironmentIdleMarker{
				activeMarker(eruncommon.ActivityKindMCP),
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

// TestSeedEnvironmentActivitySnapshotsCarriesTheLastObservation is the
// regression for erun#1216 bug 2: a Redux reset that does not restart the Go
// process (the ErrorBoundary "Reload app" button) must not lose a busy
// environment's activity — the initial-state read model has to carry the
// poller's own memory forward, since emitEnvActivityIfChanged only re-emits
// on a transition and a still-busy env produces none.
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

// TestSeedEnvironmentActivitySnapshotsLeavesUnobservedEnvsNil guards the
// other side: an env the poller has never reached (no forward established,
// or the poller hasn't run yet) must not get a fabricated snapshot.
func TestSeedEnvironmentActivitySnapshotsLeavesUnobservedEnvsNil(t *testing.T) {
	app := &App{}
	state := uiState{Tenants: []uiTenant{{Name: "acme", Environments: []uiEnvironment{{Name: "dev"}}}}}

	app.seedEnvironmentActivitySnapshots(&state)

	if state.Tenants[0].Environments[0].Activity != nil {
		t.Fatalf("expected no activity snapshot, got %+v", state.Tenants[0].Environments[0].Activity)
	}
}

// TestEnvActivitySnapshotCopiesRatherThanAliases guards the reason this
// method exists at all: a caller assembling a read model outside any a.mu
// section of its own (orchestratorInfoFor's unlocked call sites) must not
// hold a live reference into a.envActivity, or a concurrent poller write
// would race with that caller's later reads.
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

// TestResetEnvironmentActivityObservationsClearsTheCache guards the headless
// Playwright harness's fix for #1901's shared-baseline-row race: a cached
// observation left by one spec must not survive into the next one in the
// same worker.
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

// TestLoadStateSeedsEnvironmentActivityFromThePoller exercises the full
// wiring: LoadState (what the frontend's boot() thunk actually calls) must
// carry the poller's last observation onto the env it just reloaded from
// disk, without waiting for the next poll tick.
func TestLoadStateSeedsEnvironmentActivityFromThePoller(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"acme": {Name: "acme", DefaultEnvironment: "dev"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"acme/dev": {Name: "dev", LocalRepoPath: projectRoot},
		},
	}
	app := NewApp(erunUIDeps{
		store:            store,
		findProjectRoot:  func() (string, string, error) { return "acme", projectRoot, nil },
		resolveBuildInfo: func() eruncommon.BuildInfo { return eruncommon.BuildInfo{Version: "1.0.0"} },
		resolveImageRegistry: func(context.Context, string, string) (eruncommon.RuntimeRegistryVersions, error) {
			return eruncommon.RuntimeRegistryVersions{}, nil
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

// TestTriggerEnvironmentActivitySweepRunsSynchronously locks in the contract
// a deterministic caller needs: the sweep must run to completion, and emit
// for every configured selection, within the call itself rather than waiting
// for the poller's own ticker.
func TestTriggerEnvironmentActivitySweepRunsSynchronously(t *testing.T) {
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{"acme": {Name: "acme"}},
		envs:    map[string]eruncommon.EnvConfig{"acme/dev": {Name: "dev"}},
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
