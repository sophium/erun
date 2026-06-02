package erunmcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// TestIdleStopRecordToolWritesHostManualEntry covers the happy path
// the desktop's Stop button takes: no prior idle grace armed, plain
// reason text, single tenant/env. The recorded row must carry the
// host-manual source so the History tab renders it under the
// "Desktop manual stop" label.
func TestIdleStopRecordToolWritesHostManualEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}
	handler := idleStopRecordTool(runtime)

	_, result, err := handler(context.Background(), nil, IdleStopRecordInput{
		Reason:           "Manual stop via desktop",
		CloudContextName: "mock-cluster",
	})
	if err != nil {
		t.Fatalf("idleStopRecordTool returned err: %v", err)
	}
	if result.Tenant != "tenant-a" || result.Environment != "dev" {
		t.Fatalf("unexpected resolved target: %+v", result)
	}
	if time.Since(result.StoppedAt) > 5*time.Second {
		t.Fatalf("StoppedAt should be near time.Now(), got %v", result.StoppedAt)
	}

	entries, err := eruncommon.LoadEnvironmentStopHistory("tenant-a", "dev")
	if err != nil {
		t.Fatalf("LoadEnvironmentStopHistory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry.Source != eruncommon.StopHistorySourceHostManual {
		t.Fatalf("expected source=%q, got %q", eruncommon.StopHistorySourceHostManual, entry.Source)
	}
	if entry.Reason != "Manual stop via desktop" {
		t.Fatalf("expected reason carried through, got %q", entry.Reason)
	}
	if entry.CloudContextName != "mock-cluster" {
		t.Fatalf("expected cloud context name carried through, got %q", entry.CloudContextName)
	}
	if entry.GraceSeconds != 0 {
		t.Fatalf("expected GraceSeconds=0 without prior arm, got %d", entry.GraceSeconds)
	}
	if !entry.ArmedAt.IsZero() {
		t.Fatalf("expected ArmedAt zero without prior arm, got %v", entry.ArmedAt)
	}
}

// TestIdleStopRecordToolFoldsPendingArmedGrace covers the
// manual-stop-during-armed-grace case: when the user clicks Stop
// while the in-pod monitor has already armed a grace window, the
// recorded row should still be host-manual but include the marker
// breakdown, the armed-at timestamp, and the policy snapshot. That
// lets the History tab show what *would* have fired alongside the
// fact that the user pre-empted it.
func TestIdleStopRecordToolFoldsPendingArmedGrace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	pending := eruncommon.EnvironmentStopPending{
		Since:            time.Now().Add(-5 * time.Minute).UTC(),
		GraceSeconds:     600,
		CloudContextName: "mock-cluster",
		ReasonSummary:    "idle: terminal-stdin, ai",
		Markers: []eruncommon.EnvironmentIdleMarker{
			{Name: "terminal-stdin", Idle: true, Reason: "no input", LastActivity: time.Now().Add(-15 * time.Minute).UTC()},
			{Name: "ai", Idle: true, Reason: "no Claude session", LastActivity: time.Now().Add(-12 * time.Minute).UTC()},
		},
		Policy: eruncommon.EnvironmentIdlePolicy{
			Timeout:      10 * time.Minute,
			WorkingHours: "09:00-18:00",
			Timezone:     "Europe/Riga",
		},
	}
	if err := eruncommon.SaveEnvironmentStopPending("tenant-a", "dev", pending); err != nil {
		t.Fatalf("SaveEnvironmentStopPending: %v", err)
	}

	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}
	handler := idleStopRecordTool(runtime)

	if _, _, err := handler(context.Background(), nil, IdleStopRecordInput{
		Reason: "Manual stop via desktop",
	}); err != nil {
		t.Fatalf("idleStopRecordTool returned err: %v", err)
	}

	entries, err := eruncommon.LoadEnvironmentStopHistory("tenant-a", "dev")
	if err != nil {
		t.Fatalf("LoadEnvironmentStopHistory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry.Source != eruncommon.StopHistorySourceHostManual {
		t.Fatalf("expected source=host-manual even with armed grace, got %q", entry.Source)
	}
	if entry.GraceSeconds != 600 {
		t.Fatalf("expected GraceSeconds folded from pending, got %d", entry.GraceSeconds)
	}
	if entry.ArmedAt.IsZero() {
		t.Fatal("expected ArmedAt to carry pending.Since")
	}
	if entry.CloudContextName != "mock-cluster" {
		t.Fatalf("expected cloud context name from pending, got %q", entry.CloudContextName)
	}
	if entry.Policy.Timeout != 10*time.Minute {
		t.Fatalf("expected policy snapshot folded, got %+v", entry.Policy)
	}
	if len(entry.Markers) != 2 {
		t.Fatalf("expected 2 markers, got %d", len(entry.Markers))
	}
	// The pending file should be cleared after the record so a
	// follow-up stop-ready tick arms a fresh grace from scratch.
	pendingPath, err := eruncommon.EnvironmentStopPendingPath("tenant-a", "dev")
	if err != nil {
		t.Fatalf("EnvironmentStopPendingPath: %v", err)
	}
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Fatalf("expected pending file cleared, stat err=%v", err)
	}
	// Sanity: the file is genuinely in the temp HOME, not the
	// developer's real HOME.
	if !filepath.HasPrefix(pendingPath, home) {
		t.Fatalf("pending path escapes temp HOME: %s", pendingPath)
	}
}

// TestIdleStopRecordToolRequiresTenantAndEnvironment guards against
// the desktop calling the tool before it has resolved an env target.
func TestIdleStopRecordToolRequiresTenantAndEnvironment(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	handler := idleStopRecordTool(RuntimeConfig{})
	if _, _, err := handler(context.Background(), nil, IdleStopRecordInput{}); err == nil {
		t.Fatal("expected error when tenant and environment are both empty")
	}
}
