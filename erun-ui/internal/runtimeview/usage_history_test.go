package runtimeview

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestEnvironmentUsageHistorySurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "environment-usage-history.json")
	selection := uimodel.Selection{Tenant: "acme", Environment: "dev"}
	before := newTestService(Deps{UsageHistoryPath: path})
	reading := UsageReading{
		Usage:      Usage{Available: true, CPU: CPUUsage{Available: true, Utilization: "17.0%"}},
		ObservedAt: time.Now().Add(-10 * time.Minute).Truncate(time.Second),
	}
	before.PublishUsage(selection, reading)

	after := newTestService(Deps{UsageHistoryPath: path})

	restored, ok := after.UsageSnapshot()[uimodel.SelectionKey(selection)]
	if !ok {
		t.Fatal("expected the persisted reading to be present after a simulated restart")
	}
	if restored.Usage.CPU.Utilization != "17.0%" {
		t.Fatalf("expected the reading's figures to survive, got %+v", restored.Usage)
	}
	if !restored.ObservedAt.Equal(reading.ObservedAt) {
		t.Fatalf("expected ObservedAt=%v to be preserved, got %v", reading.ObservedAt, restored.ObservedAt)
	}
	if snapshot := UsageSnapshotFrom(restored); snapshot.ObservedAtUnix != reading.ObservedAt.Unix() {
		t.Fatalf("expected the restored reading to render as stale with its real age, got observedAtUnix=%d", snapshot.ObservedAtUnix)
	}
}

func TestEnvironmentUsageHistoryFreshEnvironmentStartsUnobserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "environment-usage-history.json")
	service := newTestService(Deps{UsageHistoryPath: path})

	if _, ok := service.UsageSnapshot()[uimodel.SelectionKey(uimodel.Selection{Tenant: "acme", Environment: "dev"})]; ok {
		t.Fatal("expected no persisted reading for an environment that was never sampled")
	}
}

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

func TestWriteEnvironmentUsageHistoryEntryUpsertsWithoutClobberingOthers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "environment-usage-history.json")
	first := uimodel.Selection{Tenant: "acme", Environment: "dev"}
	second := uimodel.Selection{Tenant: "acme", Environment: "staging"}
	reading := UsageReading{Usage: Usage{Available: true}, ObservedAt: time.Now().Truncate(time.Second)}

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
	updated := UsageReading{Usage: Usage{Available: true, Message: "Not running, or not open here: there is no runtime pod to measure."}, ObservedAt: time.Now().Truncate(time.Second)}
	if err := writeEnvironmentUsageHistoryEntry(path, first, updated); err != nil {
		t.Fatalf("writeEnvironmentUsageHistoryEntry(update) failed: %v", err)
	}
	seeded = loadPersistedEnvironmentUsage(path)
	if len(seeded) != 2 {
		t.Fatalf("expected the update to upsert rather than duplicate, got %+v", seeded)
	}
	if seeded[uimodel.SelectionKey(first)].Usage.Message == "" {
		t.Fatalf("expected first's updated message to be persisted, got %+v", seeded[uimodel.SelectionKey(first)].Usage)
	}
	if seeded[uimodel.SelectionKey(second)].Usage.Message != "" {
		t.Fatalf("expected second to be untouched by first's update, got %+v", seeded[uimodel.SelectionKey(second)].Usage)
	}
}
