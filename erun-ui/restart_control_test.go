package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

func postRestartControl(t *testing.T, port int, orchestratorID string) restartControlResponse {
	t.Helper()
	body, err := json.Marshal(restartControlRequest{OrchestratorID: orchestratorID})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d%s", port, eruncommon.DesktopControlPath), "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST restart control: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var decoded restartControlResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return decoded
}

// TestRestartControlServer_InvokesRestartAppExactlyOnce is the red-then-green
// case root AGENTS.md's "no dead ends" and #1341's "run exactly once" both
// require: one trigger call must produce exactly one relaunch and one quit,
// nothing left listening or armed to fire again once the server is closed.
func TestRestartControlServer_InvokesRestartAppExactlyOnce(t *testing.T) {
	app, _ := restartTestApp(t)
	relaunchCount := 0
	app.deps.relaunchApp = func() error { relaunchCount++; return nil }
	quitCount := 0
	app.deps.quitApp = func() { quitCount++ }

	server, port := startRestartControlServer(app)
	if server == nil {
		t.Fatal("expected the control server to bind a loopback listener")
	}
	defer server.Close()

	resp := postRestartControl(t, port, "agent-1")
	if !resp.OK || resp.Error != "" {
		t.Fatalf("expected ok=true, got %+v", resp)
	}
	if relaunchCount != 1 {
		t.Fatalf("relaunch called %d times, want exactly 1", relaunchCount)
	}
	if quitCount != 1 {
		t.Fatalf("quit called %d times, want exactly 1", quitCount)
	}

	// Absence of a supervisor: closing the server leaves nothing listening, so
	// there is no process left that could fire the restart again on its own —
	// the entire mechanism was this one request/response, not a standing job.
	server.Close()
	if _, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d%s", port, eruncommon.DesktopControlPath), "application/json", bytes.NewReader(nil)); err == nil {
		t.Fatal("expected the control server to refuse connections once closed, proving nothing supervises it")
	}
}

func TestRestartControlServer_ReportsRestartAppFailure(t *testing.T) {
	app, _ := restartTestApp(t)
	app.deps.relaunchApp = func() error { return fmt.Errorf("boom") }

	server, port := startRestartControlServer(app)
	if server == nil {
		t.Fatal("expected the control server to bind a loopback listener")
	}
	defer server.Close()

	resp := postRestartControl(t, port, "agent-1")
	if resp.OK {
		t.Fatal("expected ok=false when RestartApp fails")
	}
	if resp.Error == "" {
		t.Fatal("expected a non-empty error naming why the restart failed")
	}
}

// TestRestartControlServer_ResumesTheHandoffsExactConversation ties the
// trigger to the same resume path the button uses: a restart triggered
// through the control server must hand the next launch the exact conversation
// the live orchestrator session was on, not a re-derived one.
func TestRestartControlServer_ResumesTheHandoffsExactConversation(t *testing.T) {
	app, restoreDir := restartTestApp(t)
	id := createAndStartOrchestrator(t, app)

	server, port := startRestartControlServer(app)
	if server == nil {
		t.Fatal("expected the control server to bind a loopback listener")
	}
	defer server.Close()

	resp := postRestartControl(t, port, id)
	if !resp.OK {
		t.Fatalf("expected ok=true, got %+v", resp)
	}

	state := readRestoreState(t, restoreDir, id)
	if state.ConversationID != orchestratorSessionID(id) {
		t.Fatalf("expected the live conversation to be recorded, got %+v", state)
	}
	stageOrchestratorConversation(t, state.ConversationID)

	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != id || target.ConversationID != state.ConversationID {
		t.Fatalf("expected the hand-off's exact conversation to be resumed, got %+v", target)
	}
}

func TestAppStartRestartControl_WritesAndRemovesMarker(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	markerPath := filepath.Join(home, "state", "desktop-control.json")

	app := NewApp(erunUIDeps{
		store:                    newOrchestratorStubStore(t.TempDir()),
		orchestratorRestoreDir:   filepath.Join(home, "state", orchestratorRestoreDirName),
		orchestratorOpenPath:     filepath.Join(home, "orchestrator-open.json"),
		relaunchApp:              func() error { return nil },
		quitApp:                  func() {},
		desktopControlMarkerPath: markerPath,
	})

	app.startRestartControl()
	if app.restartControl == nil {
		t.Fatal("expected the control server to have started")
	}
	marker, err := eruncommon.ReadDesktopControlMarker(markerPath)
	if err != nil {
		t.Fatalf("expected a marker to be written: %v", err)
	}
	if marker.PID <= 0 || marker.ControlPort <= 0 {
		t.Fatalf("expected a live pid and control port, got %+v", marker)
	}
	if !eruncommon.DesktopProcessAlive(marker.PID) {
		t.Fatalf("marker names pid %d, which must read as alive (it is this test process)", marker.PID)
	}

	app.shutdown(context.Background())
	if _, err := eruncommon.ReadDesktopControlMarker(markerPath); err == nil {
		t.Fatal("expected shutdown to remove the restart control marker")
	}
}
