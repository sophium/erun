package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// TestStopHistoryRollsToCapAndKeepsNewestFirst writes more than
// stopHistoryCap entries via writeLastStopEvent and verifies that
// LoadStopHistory returns exactly stopHistoryCap entries, newest
// first, and that the oldest entries have been dropped. The test
// drives the App's clock via SetNowFunc so the StoppedAt timestamps
// are deterministic without sleeping.
func TestStopHistoryRollsToCapAndKeepsNewestFirst(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tempHome, ".config"))

	app := NewApp(erunUIDeps{})
	defer app.shutdown(context.Background())

	t0 := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	current := t0
	app.SetNowFunc(func() time.Time { return current })

	// Write 15 entries; the file should keep only the 10 most recent.
	for i := 0; i < 15; i++ {
		current = t0.Add(time.Duration(i) * time.Minute)
		entry := idleStopPendingEntry{
			since:             current.Add(-10 * time.Minute),
			graceSeconds:      600,
			cloudContextName:  "cloud-ctx",
			cloudContextLabel: "cluster-cloud",
			tenant:            "team-a",
			environment:       "dev",
			reasonSummary:     "idle: terminal-stdin",
			markers: []eruncommon.EnvironmentIdleMarker{
				{Name: "terminal-stdin", Idle: true, Reason: "no input for 10m"},
				{Name: "working-hours", Idle: true},
			},
		}
		if err := app.writeLastStopEvent(entry); err != nil {
			t.Fatalf("writeLastStopEvent iteration %d: %v", i, err)
		}
	}

	history, err := app.LoadStopHistory(uiSelection{Tenant: "team-a", Environment: "dev"})
	if err != nil {
		t.Fatalf("LoadStopHistory failed: %v", err)
	}
	if len(history) != stopHistoryCap {
		t.Fatalf("history length = %d, want %d", len(history), stopHistoryCap)
	}
	// Newest first: the most recent write was at t0 + 14min.
	got := history[0].StoppedAt
	want := t0.Add(14 * time.Minute).Format(time.RFC3339)
	if got != want {
		t.Fatalf("history[0].StoppedAt = %q, want %q (newest-first ordering)", got, want)
	}
	// Oldest kept entry was written at t0 + 5min (drops 0..4).
	gotOldest := history[stopHistoryCap-1].StoppedAt
	wantOldest := t0.Add(5 * time.Minute).Format(time.RFC3339)
	if gotOldest != wantOldest {
		t.Fatalf("history[%d].StoppedAt = %q, want %q (oldest still-kept)", stopHistoryCap-1, gotOldest, wantOldest)
	}
	// Per-marker breakdown survives the round-trip, with working-hours
	// filtered out by writeLastStopEvent.
	first := history[0]
	if first.Reason != "idle: terminal-stdin" {
		t.Fatalf("history[0].Reason = %q, want %q", first.Reason, "idle: terminal-stdin")
	}
	if len(first.Markers) != 1 {
		t.Fatalf("history[0].Markers = %+v, want exactly the non-working-hours entry", first.Markers)
	}
	if first.Markers[0].Name != "terminal-stdin" || !first.Markers[0].Idle {
		t.Fatalf("history[0].Markers[0] = %+v, want terminal-stdin idle=true", first.Markers[0])
	}
}

// TestLoadStopHistoryReturnsEmptyForUnrecordedEnv documents the
// no-history contract: a fresh env returns an empty slice and no
// error, so the React tree can render an empty-state placeholder
// without distinguishing "load failed" from "no records yet".
func TestLoadStopHistoryReturnsEmptyForUnrecordedEnv(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tempHome, ".config"))

	app := NewApp(erunUIDeps{})
	defer app.shutdown(context.Background())

	history, err := app.LoadStopHistory(uiSelection{Tenant: "team-a", Environment: "dev"})
	if err != nil {
		t.Fatalf("LoadStopHistory on a fresh env errored: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("LoadStopHistory on a fresh env = %+v, want empty slice", history)
	}
}

// TestStopHistoryFileSerialization locks the on-disk format. The
// React tree decodes it via Wails-generated bindings, but a future
// schema regression would land as a backward-compat issue with users'
// existing stop-history.json on upgrade.
func TestStopHistoryFileSerialization(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tempHome, ".config"))

	app := NewApp(erunUIDeps{})
	defer app.shutdown(context.Background())
	stopAt := time.Date(2026, 5, 31, 9, 0, 0, 0, time.UTC)
	app.SetNowFunc(func() time.Time { return stopAt })

	entry := idleStopPendingEntry{
		since:             stopAt.Add(-10 * time.Minute),
		graceSeconds:      600,
		cloudContextName:  "cloud-ctx",
		cloudContextLabel: "cluster-cloud",
		tenant:            "team-a",
		environment:       "dev",
		reasonSummary:     "idle: terminal-stdin",
		markers: []eruncommon.EnvironmentIdleMarker{
			{Name: "terminal-stdin", Idle: true, Reason: "no input for 10m"},
		},
	}
	if err := app.writeLastStopEvent(entry); err != nil {
		t.Fatalf("writeLastStopEvent: %v", err)
	}
	dir, err := lastStopDir("team-a", "dev")
	if err != nil {
		t.Fatalf("lastStopDir: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "stop-history.json"))
	if err != nil {
		t.Fatalf("read stop-history.json: %v", err)
	}
	var raw []map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, string(body))
	}
	if len(raw) != 1 {
		t.Fatalf("expected one entry, got %d", len(raw))
	}
	first := raw[0]
	if got := first["stoppedAt"]; got != stopAt.Format(time.RFC3339) {
		t.Fatalf("stoppedAt = %v, want %v", got, stopAt.Format(time.RFC3339))
	}
	if got := first["graceSeconds"]; got != float64(600) {
		t.Fatalf("graceSeconds = %v, want 600", got)
	}
	if got := first["reason"]; got != "idle: terminal-stdin" {
		t.Fatalf("reason = %v, want %v", got, "idle: terminal-stdin")
	}
}
