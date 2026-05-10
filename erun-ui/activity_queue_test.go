package main

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestActivityQueueStore(t *testing.T) *activityQueueStore {
	t.Helper()
	return newActivityQueueStore(nil, nil, func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) })
}

func TestDeployQueueStartTracksEntry(t *testing.T) {
	store := newTestActivityQueueStore(t)
	seed := activityQueueEntry{
		Tenant:      "team",
		Environment: "dev",
		Version:     "1.0.0",
		Release:     "team-devops",
	}
	entry, fresh := store.start(seed)
	if !fresh {
		t.Fatalf("expected fresh entry, got join")
	}
	if entry.Status != activityQueueStatusRunning {
		t.Fatalf("status = %q, want running", entry.Status)
	}
	if entry.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	all := store.list()
	if len(all) != 1 {
		t.Fatalf("list len = %d, want 1", len(all))
	}
}

func TestDeployQueueDuplicateStartsReturnExisting(t *testing.T) {
	store := newTestActivityQueueStore(t)
	seed := activityQueueEntry{Tenant: "team", Environment: "dev", Version: "1.0.0", Release: "team-devops"}
	first, fresh := store.start(seed)
	if !fresh {
		t.Fatal("first call should be fresh")
	}
	second, fresh := store.start(seed)
	if fresh {
		t.Fatal("second identical start should join, not produce a fresh entry")
	}
	if second.ID != first.ID {
		t.Fatalf("ID drift: first=%s second=%s", first.ID, second.ID)
	}
}

func TestDeployQueueFinishMovesToHistory(t *testing.T) {
	store := newTestActivityQueueStore(t)
	entry, _ := store.start(activityQueueEntry{Tenant: "t", Environment: "e", Version: "1", Release: "t-devops"})
	if final, ok := store.finish(entry.ID, activityQueueStatusSucceeded, ""); !ok {
		t.Fatal("finish returned ok=false")
	} else if final.Status != activityQueueStatusSucceeded {
		t.Fatalf("status = %q, want succeeded", final.Status)
	}
	if _, ok := store.findActive("t", "e"); ok {
		t.Fatal("entry should be moved out of active")
	}
	all := store.list()
	if len(all) != 1 {
		t.Fatalf("list len = %d, want 1 (history)", len(all))
	}
	if all[0].EndedAt == nil {
		t.Fatal("history entry missing EndedAt")
	}
}

func TestDeployQueueFinishIsIdempotent(t *testing.T) {
	store := newTestActivityQueueStore(t)
	entry, _ := store.start(activityQueueEntry{Tenant: "t", Environment: "e", Version: "1", Release: "t-devops"})
	if _, ok := store.finish(entry.ID, activityQueueStatusFailed, "x"); !ok {
		t.Fatal("first finish failed")
	}
	if _, ok := store.finish(entry.ID, activityQueueStatusSucceeded, ""); ok {
		t.Fatal("second finish should be a no-op (return false)")
	}
}

func TestDeployQueueDismissRemovesFromHistory(t *testing.T) {
	store := newTestActivityQueueStore(t)
	entry, _ := store.start(activityQueueEntry{Tenant: "t", Environment: "e", Version: "1", Release: "t-devops"})
	if _, ok := store.finish(entry.ID, activityQueueStatusSucceeded, ""); !ok {
		t.Fatal("finish failed")
	}
	if !store.dismiss(entry.ID) {
		t.Fatal("dismiss should succeed for finished entry")
	}
	if len(store.list()) != 0 {
		t.Fatal("list should be empty after dismiss")
	}
}

func TestDeployQueueDismissDoesNotRemoveActive(t *testing.T) {
	store := newTestActivityQueueStore(t)
	entry, _ := store.start(activityQueueEntry{Tenant: "t", Environment: "e", Version: "1", Release: "t-devops"})
	if store.dismiss(entry.ID) {
		t.Fatal("dismiss should refuse active entry")
	}
}

func TestActivityQueueLoadCoercesRunningToHistoryFailed(t *testing.T) {
	// Persisted "running" entries from a prior desktop session are
	// always stale: the desktop process that owned them is dead. Load()
	// coerces them to history (failed) with a clear reason; the
	// marker-watcher will re-register if the underlying CLI is still
	// alive. This eliminates phantom-running entries that used to
	// persist across desktop restarts and never get cleaned up.
	store := newActivityQueueStore(nil, nil, func() time.Time { return time.Date(2026, 5, 10, 13, 0, 0, 0, time.UTC) })
	store.load([]*activityQueueEntry{{
		ID:          "ghost",
		Tenant:      "t",
		Environment: "e",
		Version:     "1",
		Status:      activityQueueStatusRunning,
		StartedAt:   time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC),
		LastUpdated: time.Date(2026, 5, 10, 12, 30, 0, 0, time.UTC),
	}})
	all := store.list()
	if len(all) != 1 {
		t.Fatalf("list len = %d, want 1", len(all))
	}
	if all[0].Status != activityQueueStatusFailed {
		t.Fatalf("running entry on load should become failed, got %q", all[0].Status)
	}
	if all[0].Error == "" {
		t.Fatal("loaded running entry should carry a lost-state reason")
	}
	if _, stillActive := store.findActive("t", "e"); stillActive {
		t.Fatal("loaded running entry must not stay in the active map")
	}
}

func TestActivityQueuePersistenceOnlyKeepsHistory(t *testing.T) {
	// Active entries should not survive a desktop restart. Only the
	// history (recent succeeded/failed/skipped) is durable. Round-trip
	// confirms a still-active entry is dropped from the persisted file
	// while a finished entry survives.
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy_queue.json")
	store := newActivityQueueStore(
		func(entries []*activityQueueEntry) error {
			return writeActivityQueueStateAtomic(path, entries)
		},
		nil,
		func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) },
	)
	active, _ := store.start(activityQueueEntry{Command: "deploy", Tenant: "team", Environment: "dev", Version: "1.0.0", Release: "team-devops"})
	finished, _ := store.start(activityQueueEntry{Command: "build", Tenant: "team", Environment: "dev", Version: "1.0.0", Component: "erun-devops"})
	if _, ok := store.finish(finished.ID, activityQueueStatusSucceeded, ""); !ok {
		t.Fatal("finish failed")
	}
	// `active` is still running; persisted file must NOT include it.
	loaded, err := loadActivityQueueStateFromDisk(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 persisted history entry (running entries skipped), got %d", len(loaded))
	}
	if loaded[0].ID != finished.ID {
		t.Fatalf("persisted entry mismatch: got %s want %s (the active entry must be excluded)", loaded[0].ID, finished.ID)
	}
	if loaded[0].ID == active.ID {
		t.Fatalf("active entry %s leaked into persisted file", active.ID)
	}
}

func TestParseDeployContainerStatusesExtractsRunningWaitingTerminated(t *testing.T) {
	raw := []byte(`{
  "items": [
    {
      "spec": {
        "containers": [
          {"name": "erun-devops", "image": "img:1"},
          {"name": "erun-mcp", "image": "img:2"},
          {"name": "erun-dind", "image": "img:3"}
        ]
      },
      "status": {
        "containerStatuses": [
          {"name": "erun-devops", "image": "img:1", "ready": true,  "restartCount": 0, "state": {"running": {"startedAt": "2026-05-10T12:00:00Z"}}},
          {"name": "erun-mcp",    "image": "img:2", "ready": false, "restartCount": 0, "state": {"waiting": {"reason": "ContainerCreating"}}},
          {"name": "erun-dind",   "image": "img:3", "ready": false, "restartCount": 3, "state": {"terminated": {"reason": "Error", "exitCode": 137, "message": "OOMKilled"}}}
        ]
      }
    }
  ]
}`)
	statuses, err := parseActivityContainerStatuses(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(statuses) != 3 {
		t.Fatalf("len = %d, want 3", len(statuses))
	}
	wantPhases := map[string]string{"erun-devops": "Running", "erun-mcp": "Waiting", "erun-dind": "Terminated"}
	for _, s := range statuses {
		if got := s.Phase; got != wantPhases[s.Name] {
			t.Fatalf("%s phase = %q, want %q", s.Name, got, wantPhases[s.Name])
		}
	}
}

func TestStripDeployTraceANSI(t *testing.T) {
	if got := stripActivityTraceANSI("\x1b[31m==> Deployed team/dev\x1b[0m"); got != "==> Deployed team/dev" {
		t.Fatalf("unexpected: %q", got)
	}
	if got := stripActivityTraceANSI("plain text"); got != "plain text" {
		t.Fatalf("unexpected: %q", got)
	}
}
