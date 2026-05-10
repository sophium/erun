package main

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestDeployQueueStore(t *testing.T) *deployQueueStore {
	t.Helper()
	return newDeployQueueStore(nil, nil, func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) })
}

func TestDeployQueueStartTracksEntry(t *testing.T) {
	store := newTestDeployQueueStore(t)
	seed := deployQueueEntry{
		Tenant:      "team",
		Environment: "dev",
		Version:     "1.0.0",
		Release:     "team-devops",
	}
	entry, fresh := store.start(seed)
	if !fresh {
		t.Fatalf("expected fresh entry, got join")
	}
	if entry.Status != deployQueueStatusRunning {
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
	store := newTestDeployQueueStore(t)
	seed := deployQueueEntry{Tenant: "team", Environment: "dev", Version: "1.0.0", Release: "team-devops"}
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
	store := newTestDeployQueueStore(t)
	entry, _ := store.start(deployQueueEntry{Tenant: "t", Environment: "e", Version: "1", Release: "t-devops"})
	if final, ok := store.finish(entry.ID, deployQueueStatusSucceeded, ""); !ok {
		t.Fatal("finish returned ok=false")
	} else if final.Status != deployQueueStatusSucceeded {
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
	store := newTestDeployQueueStore(t)
	entry, _ := store.start(deployQueueEntry{Tenant: "t", Environment: "e", Version: "1", Release: "t-devops"})
	if _, ok := store.finish(entry.ID, deployQueueStatusFailed, "x"); !ok {
		t.Fatal("first finish failed")
	}
	if _, ok := store.finish(entry.ID, deployQueueStatusSucceeded, ""); ok {
		t.Fatal("second finish should be a no-op (return false)")
	}
}

func TestDeployQueueDismissRemovesFromHistory(t *testing.T) {
	store := newTestDeployQueueStore(t)
	entry, _ := store.start(deployQueueEntry{Tenant: "t", Environment: "e", Version: "1", Release: "t-devops"})
	if _, ok := store.finish(entry.ID, deployQueueStatusSucceeded, ""); !ok {
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
	store := newTestDeployQueueStore(t)
	entry, _ := store.start(deployQueueEntry{Tenant: "t", Environment: "e", Version: "1", Release: "t-devops"})
	if store.dismiss(entry.ID) {
		t.Fatal("dismiss should refuse active entry")
	}
}

func TestDeployQueueLoadReconcilesStaleRunning(t *testing.T) {
	store := newDeployQueueStore(nil, nil, func() time.Time { return time.Date(2026, 5, 10, 13, 0, 0, 0, time.UTC) })
	stale := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	store.load([]*deployQueueEntry{{
		ID:          "stale",
		Tenant:      "t",
		Environment: "e",
		Version:     "1",
		Status:      deployQueueStatusRunning,
		StartedAt:   stale,
		LastUpdated: stale,
	}})
	all := store.list()
	if len(all) != 1 {
		t.Fatalf("list len = %d, want 1", len(all))
	}
	if all[0].Status != deployQueueStatusFailed {
		t.Fatalf("stale running status = %q, want failed", all[0].Status)
	}
	if all[0].Error == "" {
		t.Fatal("stale entry should carry an explanatory error")
	}
}

func TestDeployQueuePersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy_queue.json")
	store := newDeployQueueStore(
		func(entries []*deployQueueEntry) error {
			return writeDeployQueueStateAtomic(path, entries)
		},
		nil,
		func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) },
	)
	entry, _ := store.start(deployQueueEntry{Tenant: "team", Environment: "dev", Version: "1.0.0", Release: "team-devops"})
	store.updateContainers(entry.ID, []deployQueueContainerStatus{{Name: "erun-devops", Image: "img:1", Ready: true, Phase: "Running"}})
	if _, ok := store.finish(entry.ID, deployQueueStatusSucceeded, ""); !ok {
		t.Fatal("finish failed")
	}

	loaded, err := loadDeployQueueStateFromDisk(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded len = %d, want 1", len(loaded))
	}
	if loaded[0].ID != entry.ID {
		t.Fatalf("ID drift across persistence: in=%s out=%s", entry.ID, loaded[0].ID)
	}
	if loaded[0].Status != deployQueueStatusSucceeded {
		t.Fatalf("status = %q, want succeeded", loaded[0].Status)
	}
	if len(loaded[0].Containers) != 1 || loaded[0].Containers[0].Name != "erun-devops" {
		t.Fatalf("containers not preserved: %+v", loaded[0].Containers)
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
	statuses, err := parseDeployContainerStatuses(raw)
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
	if got := stripDeployTraceANSI("\x1b[31m==> Deployed team/dev\x1b[0m"); got != "==> Deployed team/dev" {
		t.Fatalf("unexpected: %q", got)
	}
	if got := stripDeployTraceANSI("plain text"); got != "plain text" {
		t.Fatalf("unexpected: %q", got)
	}
}
