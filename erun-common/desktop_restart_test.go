package eruncommon

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func fakeRestartDeps(t *testing.T, marker DesktopControlMarker, markerErr error, alive bool, post func(context.Context, int, string) (bool, string, error)) DesktopRestartDeps {
	t.Helper()
	return DesktopRestartDeps{
		MarkerPath: "unused-in-fake",
		ReadMarker: func(string) (DesktopControlMarker, error) {
			if markerErr != nil {
				return DesktopControlMarker{}, markerErr
			}
			return marker, nil
		},
		ProcessAlive: func(int) bool { return alive },
		Post:         post,
	}
}

func TestRestartDesktopApp_RefusedWhenMarkerMissing(t *testing.T) {
	deps := fakeRestartDeps(t, DesktopControlMarker{}, os.ErrNotExist, true, nil)
	outcome := RestartDesktopApp(context.Background(), deps, "orch-1", false)
	if outcome.Status != DesktopRestartRefused {
		t.Fatalf("status = %q, want %q", outcome.Status, DesktopRestartRefused)
	}
	if outcome.Reason == "" {
		t.Fatal("expected a reason naming why no desktop app was found")
	}
}

// TestRestartDesktopApp_RefusedWhenPidNotAlive is the pid-does-not-resolve
// refusal root AGENTS.md calls out: a marker naming a dead pid must be refused
// outright, never treated as reachable.
func TestRestartDesktopApp_RefusedWhenPidNotAlive(t *testing.T) {
	posted := false
	post := func(context.Context, int, string) (bool, string, error) {
		posted = true
		return true, "", nil
	}
	deps := fakeRestartDeps(t, DesktopControlMarker{PID: 999999, ControlPort: 4242}, nil, false, post)
	outcome := RestartDesktopApp(context.Background(), deps, "orch-1", false)
	if outcome.Status != DesktopRestartRefused {
		t.Fatalf("status = %q, want %q", outcome.Status, DesktopRestartRefused)
	}
	if outcome.PID != 999999 || outcome.ControlPort != 4242 {
		t.Fatalf("outcome = %+v, want the resolved (stale) target named", outcome)
	}
	if posted {
		t.Fatal("must not attempt the restart post against a target that failed liveness verification")
	}
}

func TestRestartDesktopApp_DryRunReportsWouldRestartWithoutPosting(t *testing.T) {
	posted := false
	post := func(context.Context, int, string) (bool, string, error) {
		posted = true
		return true, "", nil
	}
	deps := fakeRestartDeps(t, DesktopControlMarker{PID: 123, ControlPort: 4242}, nil, true, post)
	outcome := RestartDesktopApp(context.Background(), deps, "orch-1", true)
	if outcome.Status != DesktopRestartWouldRestart {
		t.Fatalf("status = %q, want %q", outcome.Status, DesktopRestartWouldRestart)
	}
	if outcome.PID != 123 || outcome.ControlPort != 4242 {
		t.Fatalf("outcome = %+v, want the resolved target named", outcome)
	}
	if posted {
		t.Fatal("dry-run must never call Post")
	}
}

func TestRestartDesktopApp_RestartedOnSuccess(t *testing.T) {
	var gotOrchestratorID string
	post := func(_ context.Context, port int, orchestratorID string) (bool, string, error) {
		gotOrchestratorID = orchestratorID
		if port != 4242 {
			t.Fatalf("port = %d, want 4242", port)
		}
		return true, "", nil
	}
	deps := fakeRestartDeps(t, DesktopControlMarker{PID: 123, ControlPort: 4242}, nil, true, post)
	outcome := RestartDesktopApp(context.Background(), deps, "orch-1", false)
	if outcome.Status != DesktopRestartRestarted {
		t.Fatalf("status = %q, want %q", outcome.Status, DesktopRestartRestarted)
	}
	if gotOrchestratorID != "orch-1" {
		t.Fatalf("orchestratorID forwarded = %q, want %q", gotOrchestratorID, "orch-1")
	}
}

func TestRestartDesktopApp_FailedWhenRemoteRefuses(t *testing.T) {
	post := func(context.Context, int, string) (bool, string, error) {
		return false, "the running desktop declined the restart", nil
	}
	deps := fakeRestartDeps(t, DesktopControlMarker{PID: 123, ControlPort: 4242}, nil, true, post)
	outcome := RestartDesktopApp(context.Background(), deps, "orch-1", false)
	if outcome.Status != DesktopRestartFailed {
		t.Fatalf("status = %q, want %q", outcome.Status, DesktopRestartFailed)
	}
	if outcome.Reason != "the running desktop declined the restart" {
		t.Fatalf("reason = %q", outcome.Reason)
	}
}

func TestRestartDesktopApp_RefusedWhenPostTransportFails(t *testing.T) {
	post := func(context.Context, int, string) (bool, string, error) {
		return false, "", net.ErrClosed
	}
	deps := fakeRestartDeps(t, DesktopControlMarker{PID: 123, ControlPort: 4242}, nil, true, post)
	outcome := RestartDesktopApp(context.Background(), deps, "orch-1", false)
	if outcome.Status != DesktopRestartRefused {
		t.Fatalf("status = %q, want %q", outcome.Status, DesktopRestartRefused)
	}
	if outcome.Reason == "" {
		t.Fatal("expected a reason naming the unreachable control endpoint")
	}
}

func TestDesktopControlMarker_WriteReadRemoveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "desktop-control.json")
	if _, err := ReadDesktopControlMarker(path); err == nil {
		t.Fatal("expected an error reading a marker that was never written")
	}
	want := DesktopControlMarker{PID: 42, ControlPort: 4242, StartedAtUnix: 100}
	if err := WriteDesktopControlMarker(path, want); err != nil {
		t.Fatalf("WriteDesktopControlMarker: %v", err)
	}
	got, err := ReadDesktopControlMarker(path)
	if err != nil {
		t.Fatalf("ReadDesktopControlMarker: %v", err)
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if err := RemoveDesktopControlMarker(path); err != nil {
		t.Fatalf("RemoveDesktopControlMarker: %v", err)
	}
	if _, err := ReadDesktopControlMarker(path); err == nil {
		t.Fatal("expected an error reading the marker after removal")
	}
	// Removing an already-removed marker is not an error: shutdown may run
	// more than once, and the marker may never have been written at all.
	if err := RemoveDesktopControlMarker(path); err != nil {
		t.Fatalf("RemoveDesktopControlMarker (already gone): %v", err)
	}
}

func TestDesktopControlMarker_ReadRejectsIncompleteTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop-control.json")
	if err := WriteDesktopControlMarker(path, DesktopControlMarker{PID: 42}); err != nil {
		t.Fatalf("WriteDesktopControlMarker: %v", err)
	}
	if _, err := ReadDesktopControlMarker(path); err == nil {
		t.Fatal("expected an error for a marker with no control port")
	}
}

func TestDesktopProcessAlive(t *testing.T) {
	if !DesktopProcessAlive(os.Getpid()) {
		t.Fatal("the running test process must read as alive")
	}
	if DesktopProcessAlive(0) || DesktopProcessAlive(-1) {
		t.Fatal("a non-positive pid must never read as alive")
	}

	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Skipf("no usable host binary to spawn a short-lived process: %v", err)
	}
	if DesktopProcessAlive(cmd.Process.Pid) {
		t.Fatalf("pid %d exited and must not read as alive", cmd.Process.Pid)
	}
}

// TestPostDesktopRestart_RoundTrips exercises the real HTTP client against a
// real loopback server, since erun-ui's own control handler cannot be
// exercised from this module without an import cycle.
func TestPostDesktopRestart_RoundTrips(t *testing.T) {
	var gotBody desktopRestartRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != DesktopControlPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, DesktopControlPath)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(desktopRestartResponse{OK: true})
	}))
	defer server.Close()

	port := server.Listener.Addr().(*net.TCPAddr).Port
	ok, reason, err := postDesktopRestart(context.Background(), port, "orch-9")
	if err != nil {
		t.Fatalf("postDesktopRestart: %v", err)
	}
	if !ok || reason != "" {
		t.Fatalf("ok=%v reason=%q, want ok=true reason=\"\"", ok, reason)
	}
	if gotBody.OrchestratorID != "orch-9" {
		t.Fatalf("orchestratorId sent = %q, want %q", gotBody.OrchestratorID, "orch-9")
	}
}

func TestPostDesktopRestart_ReportsRemoteRefusal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(desktopRestartResponse{OK: false, Error: "restart handoff refused"})
	}))
	defer server.Close()

	port := server.Listener.Addr().(*net.TCPAddr).Port
	ok, reason, err := postDesktopRestart(context.Background(), port, "orch-9")
	if err != nil {
		t.Fatalf("postDesktopRestart: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for a remote refusal")
	}
	if reason != "restart handoff refused" {
		t.Fatalf("reason = %q", reason)
	}
}

func TestPostDesktopRestart_TransportErrorWhenUnreachable(t *testing.T) {
	// Bind and immediately close a listener to obtain a port nothing is
	// listening on, so the POST fails at the transport layer rather than
	// receiving any response.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	if _, _, err := postDesktopRestart(context.Background(), port, "orch-9"); err == nil {
		t.Fatal("expected a transport error against a port nothing is listening on")
	}
}
