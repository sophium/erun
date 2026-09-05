package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestEnvironmentUsageHistorySurvivesRestart is the issue's first Go
// validation criterion: a reading persisted before shutdown is present in a
// freshly constructed App's envUsage (the same "gone, then reattached" shape
// a desktop restart leaves behind -- see orchestrator_nudge_history_test.go's
// TestOrchestratorNudgeHistorySurvivesStopThenStart), and
// seedEnvironmentUsageSnapshots attaches it without a fresh probe.
func TestEnvironmentUsageHistorySurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "environment-usage-history.json")
	selection := uiSelection{Tenant: "acme", Environment: "dev"}
	before := &App{deps: erunUIDeps{environmentUsageHistoryPath: path}}
	reading := environmentUsageReading{
		usage:      uiRuntimeUsage{Available: true, CPU: uiRuntimeCPUUsage{Available: true, Utilization: "17.0%"}},
		observedAt: time.Now().Add(-10 * time.Minute).Truncate(time.Second),
	}
	before.persistEnvironmentUsage(selection, reading)

	// NewApp is the real construction path a restart takes -- go through it
	// rather than calling loadPersistedEnvironmentUsage directly, so this
	// exercises the same wiring the desktop actually boots with.
	after := NewApp(erunUIDeps{environmentUsageHistoryPath: path})

	restored, ok := after.envUsage[selectionKey(selection)]
	if !ok {
		t.Fatal("expected the persisted reading to be present after a simulated restart")
	}
	if restored.usage.CPU.Utilization != "17.0%" {
		t.Fatalf("expected the reading's figures to survive, got %+v", restored.usage)
	}
	if !restored.observedAt.Equal(reading.observedAt) {
		t.Fatalf("expected observedAt=%v to be preserved, got %v", reading.observedAt, restored.observedAt)
	}

	state := uiState{Tenants: []uiTenant{{Name: "acme", Environments: []uiEnvironment{{Name: "dev"}}}}}
	after.seedEnvironmentUsageSnapshots(&state)
	usage := state.Tenants[0].Environments[0].Usage
	if usage == nil {
		t.Fatal("expected seedEnvironmentUsageSnapshots to attach the restored reading")
	}
	if usage.ObservedAtUnix != reading.observedAt.Unix() {
		t.Fatalf("expected the restored reading to render as stale with its real age, got observedAtUnix=%d", usage.ObservedAtUnix)
	}
}

// TestEnvironmentUsageHistoryFreshEnvironmentStartsUnobserved is the paired
// criterion: an environment with no prior record reports no reading, so "Not
// yet observed" stays true for one that genuinely never was.
func TestEnvironmentUsageHistoryFreshEnvironmentStartsUnobserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "environment-usage-history.json")
	app := NewApp(erunUIDeps{environmentUsageHistoryPath: path})

	if _, ok := app.envUsage[selectionKey(uiSelection{Tenant: "acme", Environment: "dev"})]; ok {
		t.Fatal("expected no persisted reading for an environment that was never sampled")
	}
}

// TestEnvironmentUsageHistoryUnreadableDoesNotFabricateAReading mirrors
// readOrchestratorNudgeHistoryEntries' contract: a file that exists but
// cannot be parsed must not fabricate a reading from data that could not
// actually be read. It is logged (see loadPersistedEnvironmentUsage) and
// degrades to the same "not yet observed" state a missing file produces,
// which is never worse than the in-memory-only behaviour this replaces.
func TestEnvironmentUsageHistoryUnreadableDoesNotFabricateAReading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "environment-usage-history.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("failed to seed an unreadable history file: %v", err)
	}

	seeded := loadPersistedEnvironmentUsage(path)

	if len(seeded) != 0 {
		t.Fatalf("expected an unreadable file to seed nothing, got %+v", seeded)
	}
}

// TestWriteEnvironmentUsageHistoryEntryUpsertsWithoutClobberingOthers checks
// the multi-environment shape a real fleet exercises: persisting one
// environment's reading must not disturb another's already on disk.
func TestWriteEnvironmentUsageHistoryEntryUpsertsWithoutClobberingOthers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "environment-usage-history.json")
	first := uiSelection{Tenant: "acme", Environment: "dev"}
	second := uiSelection{Tenant: "acme", Environment: "staging"}
	reading := environmentUsageReading{usage: uiRuntimeUsage{Available: true}, observedAt: time.Now().Truncate(time.Second)}

	if err := writeEnvironmentUsageHistoryEntry(path, first, reading); err != nil {
		t.Fatalf("writeEnvironmentUsageHistoryEntry(first) failed: %v", err)
	}
	if err := writeEnvironmentUsageHistoryEntry(path, second, reading); err != nil {
		t.Fatalf("writeEnvironmentUsageHistoryEntry(second) failed: %v", err)
	}

	seeded := loadPersistedEnvironmentUsage(path)
	if len(seeded) != 2 {
		t.Fatalf("expected both environments' readings to survive, got %+v", seeded)
	}
	updated := environmentUsageReading{usage: uiRuntimeUsage{Available: true, Message: "Not running, or not open here: there is no runtime pod to measure."}, observedAt: time.Now().Truncate(time.Second)}
	if err := writeEnvironmentUsageHistoryEntry(path, first, updated); err != nil {
		t.Fatalf("writeEnvironmentUsageHistoryEntry(update) failed: %v", err)
	}
	seeded = loadPersistedEnvironmentUsage(path)
	if len(seeded) != 2 {
		t.Fatalf("expected the update to upsert rather than duplicate, got %+v", seeded)
	}
	if seeded[selectionKey(first)].usage.Message == "" {
		t.Fatalf("expected first's updated message to be persisted, got %+v", seeded[selectionKey(first)].usage)
	}
	if seeded[selectionKey(second)].usage.Message != "" {
		t.Fatalf("expected second to be untouched by first's update, got %+v", seeded[selectionKey(second)].usage)
	}
}
