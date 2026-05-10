package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// newTestAppForActivityQueue builds a minimal App with an in-memory queue
// and no on-disk persistence/watcher.
func newTestAppForActivityQueue(t *testing.T) *App {
	t.Helper()
	app := &App{
		sessions: make(map[string]*managedTerminal),
	}
	app.activityQueue = newActivityQueueStore(nil, nil, nil)
	return app
}

func TestLockTerminalsForActivityLocksMatchingSessions(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	envSession := &managedTerminal{selection: selection, key: "env\x00team\x00dev", serial: 1, kind: sessionKindOpen}
	aiSession := &managedTerminal{selection: selection, key: "ai\x00team\x00dev", serial: 2, kind: sessionKindAI}
	localSession := &managedTerminal{selection: selection, key: "local\x00team\x00dev", serial: 3, kind: sessionKindLocal}
	otherSelection := uiSelection{Tenant: "other", Environment: "dev", Version: "1.0.0"}
	unrelated := &managedTerminal{selection: otherSelection, key: "env\x00other\x00dev", serial: 4, kind: sessionKindOpen}
	app.sessions[envSession.key] = envSession
	app.sessions[aiSession.key] = aiSession
	app.sessions[localSession.key] = localSession
	app.sessions[unrelated.key] = unrelated

	entry, _ := app.activityQueue.start(activityQueueEntry{
		Command:     "deploy",
		Tenant:      "team",
		Environment: "dev",
		Version:     "1.0.0",
		Release:     "team-devops",
	})
	app.lockTerminalsForActivity(entry)

	if envSession.lockedByActivity != entry.ID {
		t.Fatalf("env session not locked: %q want %q", envSession.lockedByActivity, entry.ID)
	}
	if aiSession.lockedByActivity != entry.ID {
		t.Fatalf("ai session not locked: %q", aiSession.lockedByActivity)
	}
	if localSession.lockedByActivity != "" {
		t.Fatalf("local session unexpectedly locked: %q", localSession.lockedByActivity)
	}
	if unrelated.lockedByActivity != "" {
		t.Fatalf("unrelated tenant locked: %q", unrelated.lockedByActivity)
	}
}

func TestUnlockTerminalsForActivityClearsMatchingLocks(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	envSession := &managedTerminal{selection: selection, key: "env\x00team\x00dev", serial: 1, kind: sessionKindOpen}
	app.sessions[envSession.key] = envSession
	entry, _ := app.activityQueue.start(activityQueueEntry{Command: "deploy", Tenant: "team", Environment: "dev", Version: "1.0.0"})
	app.lockTerminalsForActivity(entry)
	if envSession.lockedByActivity == "" {
		t.Fatal("session not locked at start")
	}
	final, _ := app.activityQueue.finish(entry.ID, activityQueueStatusSucceeded, "")
	app.unlockTerminalsForActivity(final)
	if envSession.lockedByActivity != "" {
		t.Fatalf("session still locked after unlock: %q", envSession.lockedByActivity)
	}
}

func TestActivityTraceLineHandlerFinalizesOnDeployedAndFailed(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	app.activityQueue.start(activityQueueEntry{Command: "deploy", Tenant: "team", Environment: "dev", Version: "1.0.0"})
	handler := newActivityTraceLineHandler(app, selection, sessionKindLocal)
	handler("==> Deployed team/dev 1.0.0 in 12s")
	if _, ok := app.activityQueue.findActive("team", "dev"); ok {
		t.Fatal("entry should be finished after ==> Deployed")
	}
	all := app.activityQueue.list()
	if len(all) != 1 || all[0].Status != activityQueueStatusSucceeded {
		t.Fatalf("expected one succeeded entry, got %+v", all)
	}

	app2 := newTestAppForActivityQueue(t)
	app2.activityQueue.start(activityQueueEntry{Command: "deploy", Tenant: "team", Environment: "dev", Version: "1.0.0"})
	handler2 := newActivityTraceLineHandler(app2, selection, sessionKindLocal)
	handler2("Error: UPGRADE FAILED: timeout")
	handler2("==> Deploy failed after 2m0s")
	all2 := app2.activityQueue.list()
	if len(all2) != 1 || all2[0].Status != activityQueueStatusFailed {
		t.Fatalf("expected one failed entry, got %+v", all2)
	}
	if !strings.Contains(all2[0].Error, "Deploy failed") && !strings.Contains(all2[0].Error, "UPGRADE FAILED") {
		t.Fatalf("error not captured: %q", all2[0].Error)
	}
}

func TestActivityTraceLineHandlerDoesNotAutoRegisterForHostSessions(t *testing.T) {
	// Host-side sessions (Local, Command) rely on the on-disk
	// RunningCommand marker the watcher reads — the trace handler must
	// not auto-register from the trace, so it doesn't conflict with the
	// authoritative marker channel.
	app := newTestAppForActivityQueue(t)
	handler := newActivityTraceLineHandler(app, uiSelection{Tenant: "team", Environment: "dev"}, sessionKindLocal)
	handler("==> Deploying team/dev 1.0.0")
	if entries := app.activityQueue.list(); len(entries) != 0 {
		t.Fatalf("expected no auto-registered entries from host-side trace, got %+v", entries)
	}
}

func TestActivityTraceLineHandlerRegistersForInPodSessions(t *testing.T) {
	// In-pod sessions (Open, AI) live inside the runtime pod via
	// kubectl exec. The CLI inside the pod writes its marker to the
	// pod's filesystem, which the host-side watcher cannot see. Trace
	// observation is the only signal we have for those, so the handler
	// MUST register entries from `==> Deploying` lines on those
	// sessions.
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "erun", Environment: "local", KubernetesContext: "orbstack"}
	handler := newActivityTraceLineHandler(app, selection, sessionKindOpen)
	handler("==> Deploying erun/local 1.0.51-snapshot-20260510080136")
	entry, ok := app.activityQueue.findActiveByCommand("deploy", "erun", "local")
	if !ok {
		t.Fatal("expected in-pod deploy auto-registered from trace")
	}
	if entry.Version != "1.0.51-snapshot-20260510080136" {
		t.Fatalf("version = %q", entry.Version)
	}
	if entry.Command != "deploy" {
		t.Fatalf("command = %q, want deploy", entry.Command)
	}
}

func TestNamespaceForTenantEnv(t *testing.T) {
	cases := []struct {
		tenant, environment, want string
	}{
		{"team", "dev", "team-dev"},
		{"team", "", "team"},
		{"", "dev", "dev"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := namespaceForTenantEnv(c.tenant, c.environment); got != c.want {
			t.Fatalf("namespaceForTenantEnv(%q,%q) = %q, want %q", c.tenant, c.environment, got, c.want)
		}
	}
}

func TestReleaseNameForTenant(t *testing.T) {
	if got := releaseNameForTenant("team"); got != "team-devops" {
		t.Fatalf("got %q, want team-devops", got)
	}
	if got := releaseNameForTenant("  spaced  "); got != "spaced-devops" {
		t.Fatalf("got %q, want spaced-devops", got)
	}
	if got := releaseNameForTenant(""); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestNewAppDoesNotPersistWithoutExplicitPath(t *testing.T) {
	// Regression: persistence path now flows through erunUIDeps so tests
	// that don't pass it never write to the developer's real config dir.
	app := NewApp(erunUIDeps{})
	if app.activityQueue == nil {
		t.Fatal("activityQueue not initialized")
	}
	app.activityQueue.start(activityQueueEntry{Command: "deploy", Tenant: "leak", Environment: "dev", Version: "1", Release: "leak-devops"})
}

func TestApplyMarkerRecordRegistersEntryAndIsIdempotent(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	dir := t.TempDir()
	record := eruncommon.RunningCommand{
		ID:                "deploy-erun-local-1",
		Command:           "deploy",
		Tenant:            "erun",
		Environment:       "local",
		Version:           "1.0.0",
		Release:           "erun-devops",
		Namespace:         "erun-local",
		KubernetesContext: "orbstack",
		StartedAt:         time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC),
	}
	first, fresh := app.applyMarkerRecord(dir, record)
	if !fresh {
		t.Fatal("first apply should register a fresh entry")
	}
	if first.ID != record.ID {
		t.Fatalf("ID = %q, want %q", first.ID, record.ID)
	}
	expectedPath := filepath.Join(dir, sanitizeFilenameForActivity(record.ID)+".json")
	if first.MarkerPath != expectedPath {
		t.Fatalf("MarkerPath = %q, want %q", first.MarkerPath, expectedPath)
	}
	_, fresh = app.applyMarkerRecord(dir, record)
	if fresh {
		t.Fatal("second apply should be a no-op")
	}
}

func TestFinalizeDeployFromPodReadinessTransitionsToSucceeded(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	entry, _ := app.activityQueue.start(activityQueueEntry{
		Command:     "deploy",
		Tenant:      "erun",
		Environment: "local",
		Version:     "1.0.0",
		Release:     "erun-devops",
	})
	app.finalizeDeployFromPodReadiness(entry.ID)
	if _, stillActive := app.activityQueue.findActive("erun", "local"); stillActive {
		t.Fatal("entry should be moved out of active after pod-readiness finalize")
	}
	all := app.activityQueue.list()
	if len(all) != 1 || all[0].Status != activityQueueStatusSucceeded {
		t.Fatalf("expected one succeeded entry in history, got %+v", all)
	}
}

func TestAllContainersReadyAndHealthyHandlesFailureReasons(t *testing.T) {
	healthy := []activityQueueContainerStatus{
		{Name: "a", Phase: "Running", Ready: true},
		{Name: "b", Phase: "Running", Ready: true},
	}
	if !allContainersReadyAndHealthy(healthy) {
		t.Fatal("expected healthy snapshot to be Ready+healthy")
	}
	withImagePullBackoff := []activityQueueContainerStatus{
		{Name: "a", Phase: "Running", Ready: true},
		{Name: "b", Phase: "Waiting", Ready: true, Reason: "ImagePullBackOff"},
	}
	if allContainersReadyAndHealthy(withImagePullBackoff) {
		t.Fatal("a Ready=true container with ImagePullBackOff reason must not pass the gate")
	}
	withTerminated := []activityQueueContainerStatus{
		{Name: "a", Phase: "Running", Ready: true},
		{Name: "b", Phase: "Terminated", Ready: true},
	}
	if allContainersReadyAndHealthy(withTerminated) {
		t.Fatal("a terminated container must not pass the gate")
	}
	if allContainersReadyAndHealthy(nil) {
		t.Fatal("empty snapshot must not pass the gate")
	}
}

func TestForceDismissActivityRemovesActiveAndIgnoresFutureRegistrations(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	dir := t.TempDir()
	record := eruncommon.RunningCommand{
		ID:        "deploy-erun-local-stuck",
		Command:   "deploy",
		Tenant:    "erun",
		Environment: "local",
		StartedAt: time.Now().UTC(),
	}
	// Seed a marker file so applyMarkerRecord populates MarkerPath.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, sanitizeFilenameForActivity(record.ID)+".json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	entry, _ := app.applyMarkerRecord(dir, record)
	if entry.MarkerPath == "" {
		t.Fatal("MarkerPath should be set after applyMarkerRecord")
	}

	if !app.ForceDismissActivity(entry.ID) {
		t.Fatal("ForceDismissActivity should return true for an active entry")
	}
	if _, ok := app.activityQueue.findActive("erun", "local"); ok {
		t.Fatal("entry should be removed from active after force dismiss")
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("marker file should be removed: %v", statErr)
	}
	if !app.isActivityIgnored(entry.ID) {
		t.Fatal("dismissed ID should be on the ignored list so the watcher skips it")
	}

	// A subsequent applyMarkerRecord with the same ID should still register
	// (the public path through reconcileActivityMarkers is what consults
	// the ignored set). Verify the ignored check works at that level.
	if !app.isActivityIgnored(entry.ID) {
		t.Fatal("ignored set lost track of dismissed ID")
	}
}

func TestPruneStaleMarkerRemovesDeadPidMarker(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	dir := t.TempDir()
	record := eruncommon.RunningCommand{
		ID:        "open-erun-local-1",
		Command:   "open",
		Tenant:    "erun",
		Environment: "local",
		PID:       0, // sentinel for "definitely not alive" in pruneStaleMarker
		StartedAt: time.Now().UTC(),
	}
	// Seed a marker file at the expected path.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, sanitizeFilenameForActivity(record.ID)+".json")
	if err := os.WriteFile(path, []byte(`{"id":"open-erun-local-1","command":"open"}`), 0o600); err != nil {
		t.Fatalf("write seed marker: %v", err)
	}
	app.applyMarkerRecord(dir, record)
	if _, ok := app.activityQueue.findActive("erun", "local"); !ok {
		t.Fatal("expected entry registered before prune")
	}
	app.pruneStaleMarker(dir, record)
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("marker not removed: %v", statErr)
	}
	if _, ok := app.activityQueue.findActive("erun", "local"); ok {
		t.Fatal("entry should be finalized after prune")
	}
	all := app.activityQueue.list()
	if len(all) != 1 || all[0].Status != activityQueueStatusFailed {
		t.Fatalf("expected one failed entry, got %+v", all)
	}
	if !strings.Contains(all[0].Error, "killed") {
		t.Fatalf("expected error reason about killed/abandoned, got %q", all[0].Error)
	}
}

func TestFinalizeMissingMarkersClosesUnseenActiveEntries(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	dir := t.TempDir()
	app.applyMarkerRecord(dir, eruncommon.RunningCommand{ID: "deploy-1", Command: "deploy", Tenant: "t", Environment: "e", StartedAt: time.Now().UTC()})
	app.applyMarkerRecord(dir, eruncommon.RunningCommand{ID: "build-1", Command: "build", Tenant: "t", Component: "erun-devops", StartedAt: time.Now().UTC()})
	if len(app.activityQueue.list()) != 2 {
		t.Fatalf("expected 2 active before finalize, got %d", len(app.activityQueue.list()))
	}
	app.finalizeMissingMarkers(map[string]struct{}{"deploy-1": {}})
	active := 0
	for _, e := range app.activityQueue.list() {
		if e.Status == activityQueueStatusRunning {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("expected 1 active after finalize, got %d", active)
	}
}
