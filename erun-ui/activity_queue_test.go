package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func newTestActivityQueueStore(t *testing.T) *activityQueueStore {
	t.Helper()
	return newActivityQueueStore(nil, func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) })
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

func TestDeployQueueFailureCapturesOutputDetail(t *testing.T) {
	store := newTestActivityQueueStore(t)
	entry, _ := store.start(activityQueueEntry{Tenant: "t", Environment: "e", Version: "1"})
	store.recordOutputLine("t", "e", "helm upgrade --install t-devops ./chart")
	store.recordOutputLine("t", "e", "Error: UPGRADE FAILED: timed out waiting for the condition")
	store.recordOutputLine("t", "e", "==> Deploy failed after 4s")
	final, ok := store.finish(entry.ID, activityQueueStatusFailed, "==> Deploy failed after 4s")
	if !ok {
		t.Fatal("finish returned ok=false")
	}
	for _, want := range []string{"helm upgrade --install", "UPGRADE FAILED", "==> Deploy failed after 4s"} {
		if !strings.Contains(final.Detail, want) {
			t.Fatalf("Detail missing %q, got %q", want, final.Detail)
		}
	}
}

func TestDeployQueueSuccessOmitsOutputDetail(t *testing.T) {
	store := newTestActivityQueueStore(t)
	entry, _ := store.start(activityQueueEntry{Tenant: "t", Environment: "e", Version: "1"})
	store.recordOutputLine("t", "e", "helm upgrade --install t-devops ./chart")
	final, _ := store.finish(entry.ID, activityQueueStatusSucceeded, "")
	if final.Detail != "" {
		t.Fatalf("succeeded entry must not carry Detail, got %q", final.Detail)
	}
}

func TestDeployQueueOutputBufferCapsToRecentLines(t *testing.T) {
	store := newTestActivityQueueStore(t)
	entry, _ := store.start(activityQueueEntry{Tenant: "t", Environment: "e", Version: "1"})
	total := activityQueueOutputBufferLines + 50
	for i := 0; i < total; i++ {
		store.recordOutputLine("t", "e", fmt.Sprintf("line-%d", i))
	}
	final, _ := store.finish(entry.ID, activityQueueStatusFailed, "fail")
	lines := strings.Split(final.Detail, "\n")
	if len(lines) != activityQueueOutputBufferLines {
		t.Fatalf("buffered %d lines, want cap %d", len(lines), activityQueueOutputBufferLines)
	}
	if lines[0] != fmt.Sprintf("line-%d", total-activityQueueOutputBufferLines) {
		t.Fatalf("oldest retained line = %q, want first line after the dropped prefix", lines[0])
	}
	if lines[len(lines)-1] != fmt.Sprintf("line-%d", total-1) {
		t.Fatalf("most recent line not retained, got %q", lines[len(lines)-1])
	}
}

func TestDeployQueueRecordOutputLineClipsLongLines(t *testing.T) {
	store := newTestActivityQueueStore(t)
	entry, _ := store.start(activityQueueEntry{Tenant: "t", Environment: "e", Version: "1"})
	store.recordOutputLine("t", "e", strings.Repeat("x", activityQueueOutputLineMaxChars+500))
	final, _ := store.finish(entry.ID, activityQueueStatusFailed, "fail")
	if len(final.Detail) != activityQueueOutputLineMaxChars {
		t.Fatalf("line not clipped: len=%d want %d", len(final.Detail), activityQueueOutputLineMaxChars)
	}
}

func TestDeployQueueRecordOutputLineIgnoresInactiveSelection(t *testing.T) {
	store := newTestActivityQueueStore(t)
	// No active entry for this selection: recording is a no-op and must not
	// panic or create an entry.
	store.recordOutputLine("ghost", "env", "orphan output")
	if len(store.list()) != 0 {
		t.Fatal("recording for an inactive selection should not create entries")
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
