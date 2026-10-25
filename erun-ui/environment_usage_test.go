package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// "No pod to measure" must differ from "measured zero", which reads as healthy.

func TestSampleEnvironmentUsageOnceSkipsProbeWhenUnreachable(t *testing.T) {
	probed := false
	app := &App{deps: erunUIDeps{
		loadRuntimeUsage: func(context.Context, uimodel.Selection) (uiRuntimeUsage, error) {
			probed = true
			return uiRuntimeUsage{Available: true}, nil
		},
	}}
	selection := uimodel.Selection{Tenant: "acme", Environment: "dev"}

	app.sampleEnvironmentUsageOnce(selection, nil)

	if probed {
		t.Fatal("expected an unreachable environment to skip the usage probe entirely")
	}
	reading, ok := app.envUsage[selectionKey(selection)]
	if !ok {
		t.Fatal("expected a cached reading even when the probe was skipped")
	}
	if reading.usage.Available {
		t.Fatalf("expected an unreachable environment to report unavailable, got %+v", reading.usage)
	}
	if reading.usage.Message == "" {
		t.Fatal("expected a message explaining there is no pod to measure")
	}
}

func TestSampleEnvironmentUsageOnceSkipsProbeWhenNotReachable(t *testing.T) {
	probed := false
	app := &App{deps: erunUIDeps{
		loadRuntimeUsage: func(context.Context, uimodel.Selection) (uiRuntimeUsage, error) {
			probed = true
			return uiRuntimeUsage{Available: true}, nil
		},
	}}
	selection := uimodel.Selection{Tenant: "acme", Environment: "dev"}
	reachability := map[string]environmentActivityState{
		selectionKey(selection): {reachable: false, observed: false},
	}

	app.sampleEnvironmentUsageOnce(selection, reachability)

	if probed {
		t.Fatal("expected a known-but-not-reachable environment to skip the usage probe")
	}
}

func TestSampleEnvironmentUsageOnceProbesWhenReachable(t *testing.T) {
	var gotSelection uimodel.Selection
	app := &App{deps: erunUIDeps{
		loadRuntimeUsage: func(_ context.Context, selection uimodel.Selection) (uiRuntimeUsage, error) {
			gotSelection = selection
			return uiRuntimeUsage{
				Tenant: selection.Tenant, Environment: selection.Environment,
				Available: true, CPU: uiRuntimeCPUUsage{Available: true, Utilization: "12.0%"},
			}, nil
		},
	}}
	selection := uimodel.Selection{Tenant: "acme", Environment: "dev"}
	reachability := map[string]environmentActivityState{selectionKey(selection): {reachable: true}}

	app.sampleEnvironmentUsageOnce(selection, reachability)

	if gotSelection.Tenant != selection.Tenant || gotSelection.Environment != selection.Environment {
		t.Fatalf("expected the probe to run against %+v, got %+v", selection, gotSelection)
	}
	reading := app.envUsage[selectionKey(selection)]
	if !reading.usage.Available || reading.usage.CPU.Utilization != "12.0%" {
		t.Fatalf("expected the probe's reading to be cached verbatim, got %+v", reading.usage)
	}
}

func TestSampleEnvironmentUsageOnceReportsProbeFailureNotZero(t *testing.T) {
	app := &App{deps: erunUIDeps{
		loadRuntimeUsage: func(context.Context, uimodel.Selection) (uiRuntimeUsage, error) {
			return uiRuntimeUsage{}, errors.New("kubectl exec failed: pod not found")
		},
	}}
	selection := uimodel.Selection{Tenant: "acme", Environment: "dev"}
	reachability := map[string]environmentActivityState{selectionKey(selection): {reachable: true}}

	app.sampleEnvironmentUsageOnce(selection, reachability)

	reading := app.envUsage[selectionKey(selection)]
	if reading.usage.Available {
		t.Fatalf("expected a failed probe to report unavailable, got %+v", reading.usage)
	}
	if reading.usage.Message == "" {
		t.Fatal("expected a message explaining the probe failure")
	}
}

func TestSeedEnvironmentUsageSnapshotsCarriesTheLastReading(t *testing.T) {
	observedAt := time.Now().Add(-5 * time.Minute)
	app := &App{envUsage: map[string]environmentUsageReading{
		selectionKey(uimodel.Selection{Tenant: "acme", Environment: "dev"}): {
			usage:      uiRuntimeUsage{Available: true, CPU: uiRuntimeCPUUsage{Available: true, Utilization: "42.0%"}},
			observedAt: observedAt,
		},
	}}
	state := uiState{Tenants: []uiTenant{{Name: "acme", Environments: []uiEnvironment{{Name: "dev"}}}}}

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
	if usage.StaleAfterSeconds != int64(environmentUsageInterval/time.Second) {
		t.Fatalf("expected StaleAfterSeconds to name the sweep interval, got %d", usage.StaleAfterSeconds)
	}
}

func TestSeedEnvironmentUsageSnapshotsLeavesUnobservedEnvsNil(t *testing.T) {
	app := &App{}
	state := uiState{Tenants: []uiTenant{{Name: "acme", Environments: []uiEnvironment{{Name: "dev"}}}}}

	app.seedEnvironmentUsageSnapshots(&state)

	if state.Tenants[0].Environments[0].Usage != nil {
		t.Fatalf("expected no usage snapshot, got %+v", state.Tenants[0].Environments[0].Usage)
	}
}

func TestEnvUsageSnapshotCopiesRatherThanAliases(t *testing.T) {
	key := selectionKey(uimodel.Selection{Tenant: "acme", Environment: "dev"})
	app := &App{envUsage: map[string]environmentUsageReading{key: {usage: uiRuntimeUsage{Available: true}}}}

	snapshot := app.envUsageSnapshot()
	if len(snapshot) != 1 || !snapshot[key].usage.Available {
		t.Fatalf("expected the snapshot to carry the reading, got %+v", snapshot)
	}
	app.envUsage[key] = environmentUsageReading{}
	if !snapshot[key].usage.Available {
		t.Fatal("expected the snapshot to be a copy, unaffected by a later mutation of a.envUsage")
	}
}

func TestEnvUsageSnapshotNilWhenEmpty(t *testing.T) {
	app := &App{}
	if snapshot := app.envUsageSnapshot(); snapshot != nil {
		t.Fatalf("expected a nil snapshot for an empty sweep state, got %+v", snapshot)
	}
}

func TestResetEnvironmentUsageObservationsClearsTheCache(t *testing.T) {
	key := selectionKey(uimodel.Selection{Tenant: "acme", Environment: "dev"})
	app := &App{envUsage: map[string]environmentUsageReading{key: {usage: uiRuntimeUsage{Available: true}}}}

	app.ResetEnvironmentUsageObservations()

	if snapshot := app.envUsageSnapshot(); snapshot != nil {
		t.Fatalf("expected no cached readings after reset, got %+v", snapshot)
	}
}

func TestResetEnvironmentUsageObservationsOnEmptyCacheIsANoop(t *testing.T) {
	app := &App{}
	app.ResetEnvironmentUsageObservations()
	if snapshot := app.envUsageSnapshot(); snapshot != nil {
		t.Fatalf("expected no cached readings, got %+v", snapshot)
	}
}
