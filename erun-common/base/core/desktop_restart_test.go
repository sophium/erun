package core

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fakeRestartDeps(t *testing.T, marker DesktopControlMarker, markerErr error, alive bool, post func(context.Context, int, string) (DesktopRestartResponse, error)) DesktopRestartDeps {
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

// fakeDryRunDeps is a resolved, live target whose plan call is plan, with the
// restart call counted. Whatever the plan answers — a plan, a refusal, a
// transport error — a dry run must never reach the restart, so every case below
// reads that counter rather than trusting the branch it is exercising.
func fakeDryRunDeps(t *testing.T, plan func(context.Context, int, string) (DesktopRestartResponse, error)) (DesktopRestartDeps, *int) {
	t.Helper()
	restarts := 0
	deps := fakeRestartDeps(t, DesktopControlMarker{PID: 123, ControlPort: 4242}, nil, true,
		func(context.Context, int, string) (DesktopRestartResponse, error) {
			restarts++
			return DesktopRestartResponse{OK: true}, nil
		})
	deps.Plan = plan
	return deps, &restarts
}

// A desktop that answers but cannot produce the plan is the same report from
// the other side of the wire, and the same refusal to fall back on a restart.
func TestRestartDesktopApp_DryRunReportsAPlanTheDesktopCouldNotAnswer(t *testing.T) {
	deps, restarts := fakeDryRunDeps(t, func(context.Context, int, string) (DesktopRestartResponse, error) {
		return DesktopRestartResponse{OK: false, Error: "the open set could not be read"}, nil
	})
	outcome := RestartDesktopApp(context.Background(), deps, "orch-1", true)
	if outcome.Status != DesktopRestartWouldRestart {
		t.Fatalf("status = %q, want %q", outcome.Status, DesktopRestartWouldRestart)
	}
	if !strings.Contains(outcome.PreviewUnavailable, "the open set could not be read") {
		t.Fatalf("PreviewUnavailable = %q, want it to name the desktop's own reason", outcome.PreviewUnavailable)
	}
	if *restarts != 0 {
		t.Fatalf("the dry run asked the desktop to restart %d times", *restarts)
	}
}

func TestRestartDesktopApp_RestartedOnSuccess(t *testing.T) {
	var gotOrchestratorID string
	post := func(_ context.Context, port int, orchestratorID string) (DesktopRestartResponse, error) {
		gotOrchestratorID = orchestratorID
		if port != 4242 {
			t.Fatalf("port = %d, want 4242", port)
		}
		return DesktopRestartResponse{OK: true}, nil
	}
	deps := fakeRestartDeps(t, DesktopControlMarker{PID: 123, ControlPort: 4242}, nil, true, post)
	deps.Plan = func(context.Context, int, string) (DesktopRestartResponse, error) {
		t.Fatal("a real restart asked for the plan instead of the restart")
		return DesktopRestartResponse{}, nil
	}
	outcome := RestartDesktopApp(context.Background(), deps, "orch-1", false)
	if outcome.Status != DesktopRestartRestarted {
		t.Fatalf("status = %q, want %q", outcome.Status, DesktopRestartRestarted)
	}
	if gotOrchestratorID != "orch-1" {
		t.Fatalf("orchestratorID forwarded = %q, want %q", gotOrchestratorID, "orch-1")
	}
	if len(outcome.Preview) != 0 {
		t.Fatalf("outcome = %+v, want no plan on a real restart", outcome)
	}
}

func TestRestartDesktopApp_RefusedWhenPostTransportFails(t *testing.T) {
	post := func(context.Context, int, string) (DesktopRestartResponse, error) {
		return DesktopRestartResponse{}, net.ErrClosed
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

func TestDesktopControlMarker_ReadRejectsIncompleteTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop-control.json")
	if err := os.WriteFile(path, []byte(`{"pid":42}`), 0o600); err != nil {
		t.Fatalf("stage incomplete marker: %v", err)
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

	// Re-run this test binary with no test selected: it exits immediately, so
	// its pid is a real process that is really gone, without depending on a
	// host utility that not every platform ships (a skip here would have left
	// the exited-pid half of this check unrun wherever it was missing).
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatalf("spawn %s to get an exited pid: %v", os.Args[0], err)
	}
	if DesktopProcessAlive(cmd.Process.Pid) {
		t.Fatalf("pid %d exited and must not read as alive", cmd.Process.Pid)
	}
}

// TestPostDesktopRestart_RoundTrips exercises the real HTTP client against a
// real loopback server, since erun-ui's own control handler cannot be
// exercised from this module without an import cycle.
func TestPostDesktopRestart_RoundTrips(t *testing.T) {
	var gotBody DesktopRestartRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != DesktopControlPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, DesktopControlPath)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DesktopRestartResponse{OK: true})
	}))
	defer server.Close()

	port := testTCPPort(t, server.Listener.Addr())
	response, err := postDesktopRestart(context.Background(), port, "orch-9")
	if err != nil {
		t.Fatalf("postDesktopRestart: %v", err)
	}
	if !response.OK || response.Error != "" {
		t.Fatalf("response=%+v, want ok=true with no error", response)
	}
	if gotBody.OrchestratorID != "orch-9" {
		t.Fatalf("orchestratorId sent = %q, want %q", gotBody.OrchestratorID, "orch-9")
	}
}

// The path a plan is sent to is not the path a restart is performed by, and
// that is the whole defence: a desktop that serves only the restart path cannot
// be asked a question, so it must answer "not served" rather than act.
func TestPostDesktopRestartPlan_IsNotTheRestartPath(t *testing.T) {
	if DesktopControlPlanPath == DesktopControlPath {
		t.Fatal("the plan and the restart share a path, so an older desktop would read the question as the command")
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
	port := testTCPPort(t, listener.Addr())
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	if _, err := postDesktopRestart(context.Background(), port, "orch-9"); err == nil {
		t.Fatal("expected a transport error against a port nothing is listening on")
	}
}

func testTCPPort(t *testing.T, addr net.Addr) int {
	t.Helper()
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		t.Fatalf("want a TCP address, got %T", addr)
	}
	return tcp.Port
}
