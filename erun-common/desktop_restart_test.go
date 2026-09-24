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
	post := func(context.Context, int, string) (DesktopRestartResponse, error) {
		posted = true
		return DesktopRestartResponse{OK: true}, nil
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

// A dry run asks the desktop a question. It asks it on the plan path, and it
// never asks the restart path at all — so what it carries back is the plan, and
// what it leaves behind is a desktop that is still running.
func TestRestartDesktopApp_DryRunAsksForThePlanAndNotARestart(t *testing.T) {
	var gotOrchestratorID string
	deps, restarts := fakeDryRunDeps(t, func(_ context.Context, port int, orchestratorID string) (DesktopRestartResponse, error) {
		gotOrchestratorID = orchestratorID
		if port != 4242 {
			t.Fatalf("port = %d, want 4242", port)
		}
		return DesktopRestartResponse{OK: true, Preview: []DesktopRestartReopen{
			{OrchestratorID: "orch-2", ConversationID: "conv-anchor", Notice: "reopened on its anchor"},
		}}, nil
	})
	outcome := RestartDesktopApp(context.Background(), deps, "orch-1", true)
	if outcome.Status != DesktopRestartWouldRestart {
		t.Fatalf("status = %q, want %q", outcome.Status, DesktopRestartWouldRestart)
	}
	if outcome.PID != 123 || outcome.ControlPort != 4242 {
		t.Fatalf("outcome = %+v, want the resolved target named", outcome)
	}
	if gotOrchestratorID != "orch-1" {
		t.Fatalf("plan asked for %q, want %q", gotOrchestratorID, "orch-1")
	}
	if len(outcome.Preview) != 1 || outcome.Preview[0].OrchestratorID != "orch-2" {
		t.Fatalf("outcome = %+v, want the desktop's plan carried back", outcome)
	}
	if outcome.PreviewUnavailable != "" {
		t.Fatalf("outcome = %+v, want no unavailability reported when the plan came back", outcome)
	}
	if *restarts != 0 {
		t.Fatalf("the dry run asked the desktop to restart %d times", *restarts)
	}
}

// The dry run resolved and verified a live target, so it still would have
// restarted — and the half it could not read says so, because an absent plan
// silently rendered as "nothing would be stranded" is the very silence this
// command exists to end.
func TestRestartDesktopApp_DryRunReportsAPlanItCouldNotRead(t *testing.T) {
	deps, restarts := fakeDryRunDeps(t, func(context.Context, int, string) (DesktopRestartResponse, error) {
		return DesktopRestartResponse{}, net.ErrClosed
	})
	outcome := RestartDesktopApp(context.Background(), deps, "orch-1", true)
	if outcome.Status != DesktopRestartWouldRestart {
		t.Fatalf("status = %q, want %q — a plan failure is not a refusal to restart", outcome.Status, DesktopRestartWouldRestart)
	}
	if !strings.Contains(outcome.PreviewUnavailable, net.ErrClosed.Error()) {
		t.Fatalf("PreviewUnavailable = %q, want it to name the cause", outcome.PreviewUnavailable)
	}
	if *restarts != 0 {
		t.Fatalf("a plan that could not be read fell back on restarting the desktop %d times", *restarts)
	}
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

// The version-skew shape, and the whole reason the plan is a path of its own. A
// desktop built before this command serves the restart path and nothing else,
// so a `dryRun` flag sent to it would be an unknown JSON field it ignores: it
// would read the question as the command, restart itself, and answer exactly
// what a restart answers. Sent to a path it does not serve, the question is
// refused instead, and the operator is told which of the two they are talking
// to.
func TestRestartDesktopApp_DryRunAgainstADesktopThatPredatesThePlanRefusesRatherThanRestarting(t *testing.T) {
	restarts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != DesktopControlPath {
			http.NotFound(w, r)
			return
		}
		restarts++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DesktopRestartResponse{OK: true})
	}))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port

	outcome := RestartDesktopApp(context.Background(), DesktopRestartDeps{
		MarkerPath: "unused-in-fake",
		ReadMarker: func(string) (DesktopControlMarker, error) {
			return DesktopControlMarker{PID: 123, ControlPort: port}, nil
		},
		ProcessAlive: func(int) bool { return true },
		Post:         postDesktopRestart,
		Plan:         postDesktopRestartPlan,
	}, "orch-1", true)

	if restarts != 0 {
		t.Fatalf("the dry run restarted the desktop %d times", restarts)
	}
	if outcome.Status != DesktopRestartWouldRestart {
		t.Fatalf("status = %q, want %q", outcome.Status, DesktopRestartWouldRestart)
	}
	for _, want := range []string{DesktopControlPlanPath, "older than this command"} {
		if !strings.Contains(outcome.PreviewUnavailable, want) {
			t.Fatalf("PreviewUnavailable = %q, want it to name %q", outcome.PreviewUnavailable, want)
		}
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

func TestRestartDesktopApp_FailedWhenRemoteRefuses(t *testing.T) {
	post := func(context.Context, int, string) (DesktopRestartResponse, error) {
		return DesktopRestartResponse{OK: false, Error: "the running desktop declined the restart"}, nil
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

// exitedPID returns a pid that is certainly gone: this test binary re-executed
// with no test selected exits immediately, so its pid is a real process that is
// really finished (and this process has reaped it).
func exitedPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatalf("spawn %s for an exited pid: %v", os.Args[0], err)
	}
	return cmd.Process.Pid
}

// TestRestartDesktopApp_RefusesAStaleRecordAsStaleNotAbsent is the detection
// half of ownership: a desktop that died without shutting down leaves its
// record on disk naming the dead pid, and that record is the only thing that
// tells a trigger "a desktop ran here and is gone" instead of the
// indistinguishable "no desktop has ever run". Removing it on the way out, or
// reading it as absent, hides a desktop that died.
func TestRestartDesktopApp_RefusesAStaleRecordAsStaleNotAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), desktopControlMarkerFileName)
	deadPID := exitedPID(t)
	stageDesktopControlMarker(t, path, DesktopControlMarker{PID: deadPID, ControlPort: 59775, StartedAtUnix: 100})
	if DesktopProcessAlive(deadPID) {
		t.Fatalf("pid %d exited and must not read as alive", deadPID)
	}

	posted := false
	outcome := RestartDesktopApp(context.Background(), DesktopRestartDeps{
		MarkerPath:   path,
		ReadMarker:   ReadDesktopControlMarker,
		ProcessAlive: DesktopProcessAlive,
		Post: func(context.Context, int, string) (DesktopRestartResponse, error) {
			posted = true
			return DesktopRestartResponse{OK: true}, nil
		},
	}, "orch-1", false)
	if posted {
		t.Fatal("must not post a restart to a dead desktop's record")
	}
	if outcome.Status != DesktopRestartRefused {
		t.Fatalf("status = %q, want %q", outcome.Status, DesktopRestartRefused)
	}
	if !strings.Contains(outcome.Reason, "stale") {
		t.Fatalf("reason = %q, want it to name the record as stale rather than absent", outcome.Reason)
	}
}

// stageDesktopControlMarker writes the record exactly as the desktop records
// it, for the read-side tests here that need one without a desktop to write it.
func stageDesktopControlMarker(t *testing.T, path string, marker DesktopControlMarker) {
	t.Helper()
	data, err := json.Marshal(marker)
	if err != nil {
		t.Fatalf("marshal marker: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("stage marker %s: %v", path, err)
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

	port := server.Listener.Addr().(*net.TCPAddr).Port
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

// The plan is asked for on its own path, and the plan it comes back with is the
// whole point of asking.
func TestPostDesktopRestartPlan_CarriesThePlanBack(t *testing.T) {
	var gotPath string
	var gotBody DesktopRestartRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DesktopRestartResponse{OK: true, Preview: []DesktopRestartReopen{
			{OrchestratorID: "petios-ops", ConversationID: "anchor-1", Notice: "left 3bde477f behind"},
		}})
	}))
	defer server.Close()

	port := server.Listener.Addr().(*net.TCPAddr).Port
	response, err := postDesktopRestartPlan(context.Background(), port, "orch-9")
	if err != nil {
		t.Fatalf("postDesktopRestartPlan: %v", err)
	}
	if gotPath != DesktopControlPlanPath {
		t.Fatalf("plan posted to %q, want %q", gotPath, DesktopControlPlanPath)
	}
	if gotBody.OrchestratorID != "orch-9" {
		t.Fatalf("orchestratorId sent = %q, want %q", gotBody.OrchestratorID, "orch-9")
	}
	if len(response.Preview) != 1 || response.Preview[0].ConversationID != "anchor-1" {
		t.Fatalf("response = %+v, want the plan decoded off the wire", response)
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

func TestPostDesktopRestart_ReportsRemoteRefusal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DesktopRestartResponse{OK: false, Error: "restart handoff refused"})
	}))
	defer server.Close()

	port := server.Listener.Addr().(*net.TCPAddr).Port
	response, err := postDesktopRestart(context.Background(), port, "orch-9")
	if err != nil {
		t.Fatalf("postDesktopRestart: %v", err)
	}
	if response.OK {
		t.Fatal("expected ok=false for a remote refusal")
	}
	if response.Error != "restart handoff refused" {
		t.Fatalf("error = %q", response.Error)
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
	if _, err := postDesktopRestart(context.Background(), port, "orch-9"); err == nil {
		t.Fatal("expected a transport error against a port nothing is listening on")
	}
}
