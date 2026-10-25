package runtimeview

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
	service := newTestService(Deps{
		LoadUsage: func(context.Context, uimodel.Selection) (Usage, error) {
			probed = true
			return Usage{Available: true}, nil
		},
	})
	selection := uimodel.Selection{Tenant: "acme", Environment: "dev"}

	service.sampleEnvironmentUsageOnce(selection, nil)

	if probed {
		t.Fatal("expected an unreachable environment to skip the usage probe entirely")
	}
	reading, ok := service.usage[uimodel.SelectionKey(selection)]
	if !ok {
		t.Fatal("expected a cached reading even when the probe was skipped")
	}
	if reading.Usage.Available {
		t.Fatalf("expected an unreachable environment to report unavailable, got %+v", reading.Usage)
	}
	if reading.Usage.Message == "" {
		t.Fatal("expected a message explaining there is no pod to measure")
	}
}

func TestSampleEnvironmentUsageOnceSkipsProbeWhenNotReachable(t *testing.T) {
	probed := false
	service := newTestService(Deps{
		LoadUsage: func(context.Context, uimodel.Selection) (Usage, error) {
			probed = true
			return Usage{Available: true}, nil
		},
	})
	selection := uimodel.Selection{Tenant: "acme", Environment: "dev"}
	reachability := map[string]ActivityState{
		uimodel.SelectionKey(selection): {Reachable: false, Observed: false},
	}

	service.sampleEnvironmentUsageOnce(selection, reachability)

	if probed {
		t.Fatal("expected a known-but-not-reachable environment to skip the usage probe")
	}
}

func TestSampleEnvironmentUsageOnceProbesWhenReachable(t *testing.T) {
	var gotSelection uimodel.Selection
	service := newTestService(Deps{
		LoadUsage: func(_ context.Context, selection uimodel.Selection) (Usage, error) {
			gotSelection = selection
			return Usage{
				Tenant: selection.Tenant, Environment: selection.Environment,
				Available: true, CPU: CPUUsage{Available: true, Utilization: "12.0%"},
			}, nil
		},
	})
	selection := uimodel.Selection{Tenant: "acme", Environment: "dev"}
	reachability := map[string]ActivityState{uimodel.SelectionKey(selection): {Reachable: true}}

	service.sampleEnvironmentUsageOnce(selection, reachability)

	if gotSelection.Tenant != selection.Tenant || gotSelection.Environment != selection.Environment {
		t.Fatalf("expected the probe to run against %+v, got %+v", selection, gotSelection)
	}
	reading := service.usage[uimodel.SelectionKey(selection)]
	if !reading.Usage.Available || reading.Usage.CPU.Utilization != "12.0%" {
		t.Fatalf("expected the probe's reading to be cached verbatim, got %+v", reading.Usage)
	}
}

func TestSampleEnvironmentUsageOnceReportsProbeFailureNotZero(t *testing.T) {
	service := newTestService(Deps{
		LoadUsage: func(context.Context, uimodel.Selection) (Usage, error) {
			return Usage{}, errors.New("kubectl exec failed: pod not found")
		},
	})
	selection := uimodel.Selection{Tenant: "acme", Environment: "dev"}
	reachability := map[string]ActivityState{uimodel.SelectionKey(selection): {Reachable: true}}

	service.sampleEnvironmentUsageOnce(selection, reachability)

	reading := service.usage[uimodel.SelectionKey(selection)]
	if reading.Usage.Available {
		t.Fatalf("expected a failed probe to report unavailable, got %+v", reading.Usage)
	}
	if reading.Usage.Message == "" {
		t.Fatal("expected a message explaining the probe failure")
	}
}

func TestEnvUsageSnapshotCopiesRatherThanAliases(t *testing.T) {
	key := uimodel.SelectionKey(uimodel.Selection{Tenant: "acme", Environment: "dev"})
	service := newTestService(Deps{})
	service.usage = map[string]UsageReading{key: {Usage: Usage{Available: true}}}

	snapshot := service.UsageSnapshot()
	if len(snapshot) != 1 || !snapshot[key].Usage.Available {
		t.Fatalf("expected the snapshot to carry the reading, got %+v", snapshot)
	}
	service.usage[key] = UsageReading{}
	if !snapshot[key].Usage.Available {
		t.Fatal("expected the snapshot to be a copy, unaffected by a later mutation of the cache")
	}
}

func TestEnvUsageSnapshotNilWhenEmpty(t *testing.T) {
	service := newTestService(Deps{})
	if snapshot := service.UsageSnapshot(); snapshot != nil {
		t.Fatalf("expected a nil snapshot for an empty sweep state, got %+v", snapshot)
	}
}

func TestResetEnvironmentUsageObservationsClearsTheCache(t *testing.T) {
	key := uimodel.SelectionKey(uimodel.Selection{Tenant: "acme", Environment: "dev"})
	service := newTestService(Deps{})
	service.usage = map[string]UsageReading{key: {Usage: Usage{Available: true}}}

	service.ResetEnvironmentUsageObservations()

	if snapshot := service.UsageSnapshot(); snapshot != nil {
		t.Fatalf("expected no cached readings after reset, got %+v", snapshot)
	}
}

func TestResetEnvironmentUsageObservationsOnEmptyCacheIsANoop(t *testing.T) {
	service := newTestService(Deps{})
	service.ResetEnvironmentUsageObservations()
	if snapshot := service.UsageSnapshot(); snapshot != nil {
		t.Fatalf("expected no cached readings, got %+v", snapshot)
	}
}

func TestUsageSnapshotFromNamesTheSweepInterval(t *testing.T) {
	observedAt := time.Unix(1000, 0)
	snapshot := UsageSnapshotFrom(UsageReading{Usage: Usage{Available: true}, ObservedAt: observedAt})
	if snapshot.ObservedAtUnix != observedAt.Unix() {
		t.Fatalf("expected ObservedAtUnix=%d, got %d", observedAt.Unix(), snapshot.ObservedAtUnix)
	}
	if snapshot.StaleAfterSeconds != int64(environmentUsageInterval/time.Second) {
		t.Fatalf("expected StaleAfterSeconds to name the sweep interval, got %d", snapshot.StaleAfterSeconds)
	}
}
