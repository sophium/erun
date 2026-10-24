package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

func postRestartControl(t *testing.T, port int, orchestratorID string) core.DesktopRestartResponse {
	t.Helper()
	body, err := json.Marshal(core.DesktopRestartRequest{OrchestratorID: orchestratorID})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d%s", port, core.DesktopControlPath), "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST restart control: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var decoded core.DesktopRestartResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return decoded
}

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

	// Closing the server leaves nothing that could fire the restart again.
	server.Close()
	if resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d%s", port, core.DesktopControlPath), "application/json", bytes.NewReader(nil)); err == nil {
		_ = resp.Body.Close()
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

func TestRestartControlServer_HandsThePaneToTheOrchestratorThatAsked(t *testing.T) {
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
	if state := readRestoreState(t, restoreDir, id); state.OrchestratorID != id {
		t.Fatalf("expected the hand-off to name %q, got %+v", id, state)
	}
	if target := app.ResolveOrchestratorToReopen(); target.OrchestratorID != id {
		t.Fatalf("expected %q to own the pane, got %+v", id, target)
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
	marker, err := core.ReadDesktopControlMarker(markerPath)
	if err != nil {
		t.Fatalf("expected a marker to be written: %v", err)
	}
	if marker.PID <= 0 || marker.ControlPort <= 0 {
		t.Fatalf("expected a live pid and control port, got %+v", marker)
	}
	if !core.DesktopProcessAlive(marker.PID) {
		t.Fatalf("marker names pid %d, which must read as alive (it is this test process)", marker.PID)
	}

	app.shutdown(context.Background())
	if _, err := core.ReadDesktopControlMarker(markerPath); err == nil {
		t.Fatal("expected shutdown to remove the restart control marker")
	}
}

func restartControlTestApp(t *testing.T, markerPath string) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	app := NewApp(erunUIDeps{
		store:                       newOrchestratorStubStore(t.TempDir()),
		orchestratorRestoreDir:      filepath.Join(home, "state", orchestratorRestoreDirName),
		orchestratorOpenPath:        filepath.Join(home, "orchestrator-open.json"),
		relaunchApp:                 func() error { return nil },
		quitApp:                     func() {},
		desktopControlMarkerPath:    markerPath,
		runOrchestratorLabelCommand: stubOrchestratorClaimLabel,
	})
	t.Cleanup(func() { app.shutdown(context.Background()) })
	return app
}

// The successor starts while the predecessor still owns the record, so it must wait rather than overwrite it.
// A real second process is used because liveness is the whole question.
func TestAppStartRestartControl_LeavesALiveOwnersRecordAndAdoptsItAfterwards(t *testing.T) {
	markerPath := filepath.Join(t.TempDir(), "state", "desktop-control.json")
	predecessor := startRestartControlHolder(t)
	predecessorMarker := core.DesktopControlMarker{PID: predecessor.pid(), ControlPort: 59775, StartedAtUnix: 100}
	if _, err := claimDesktopControlMarker(markerPath, predecessorMarker, neverAlive); err != nil {
		t.Fatalf("stage the predecessor's record: %v", err)
	}

	app := restartControlTestApp(t, markerPath)
	app.startRestartControl()
	if app.restartControl == nil {
		t.Fatal("expected the control server to have started")
	}

	got, err := core.ReadDesktopControlMarker(markerPath)
	if err != nil {
		t.Fatalf("the predecessor's record was removed by a process that is not it: %v", err)
	}
	if got != predecessorMarker {
		t.Fatalf("a second instance overwrote a live owner's record: got %+v, want %+v", got, predecessorMarker)
	}

	predecessor.stop()

	waitForRestartControlMarkerPID(t, markerPath, os.Getpid())
}

func neverAlive(int) bool { return false }

func waitForRestartControlMarkerPID(t *testing.T, path string, pid int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		marker, err := core.ReadDesktopControlMarker(path)
		if err == nil && marker.PID == pid {
			return
		}
		if time.Now().After(deadline) {
			got := "none"
			if err == nil {
				got = fmt.Sprintf("%+v", marker)
			}
			t.Fatalf("the successor never took over the control record: got %s (err %v), want pid %d", got, err, pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

const restartControlHolderEnv = "ERUN_TEST_RESTART_CONTROL_HOLDER"

// TestRestartControlHolderProcess is a re-exec helper, not a test: it exits when stdin closes.
func TestRestartControlHolderProcess(t *testing.T) {
	if os.Getenv(restartControlHolderEnv) != "1" {
		return
	}
	_, _ = fmt.Fprintln(os.Stdout, "ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

type restartControlHolder struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stopped bool
}

func (h *restartControlHolder) pid() int { return h.cmd.Process.Pid }

func (h *restartControlHolder) stop() {
	if h.stopped {
		return
	}
	h.stopped = true
	_ = h.stdin.Close()
	_ = h.cmd.Wait()
}

func startRestartControlHolder(t *testing.T) *restartControlHolder {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRestartControlHolderProcess$")
	cmd.Env = append(os.Environ(), restartControlHolderEnv+"=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe for the holder process: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe for the holder process: %v", err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the holder process: %v", err)
	}
	holder := &restartControlHolder{cmd: cmd, stdin: stdin}
	t.Cleanup(holder.stop)
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("holder process did not come up: line %q, err %v", line, err)
	}
	if !core.DesktopProcessAlive(holder.pid()) {
		t.Fatalf("holder process pid %d does not read as alive", holder.pid())
	}
	return holder
}

func TestAdoptRestartControl_TakesOverOnceTheHolderExits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop-control.json")
	predecessor := core.DesktopControlMarker{PID: 50193, ControlPort: 59775, StartedAtUnix: 100}
	successor := core.DesktopControlMarker{PID: os.Getpid(), ControlPort: 56614, StartedAtUnix: 200}
	alive := map[int]bool{predecessor.PID: true, successor.PID: true}
	processAlive := func(pid int) bool { return alive[pid] }
	if _, err := claimDesktopControlMarker(path, predecessor, processAlive); err != nil {
		t.Fatalf("stage the predecessor's record: %v", err)
	}

	attempts := 0
	sleeps := 0
	remaining, err := adoptRestartControl(func() (restartControlClaim, error) {
		attempts++
		if attempts == 2 {
			delete(alive, predecessor.PID)
			if err := removeDesktopControlMarker(path, predecessor.PID); err != nil {
				return restartControlClaim{}, fmt.Errorf("predecessor shutdown: %w", err)
			}
		}
		return claimDesktopControlMarker(path, successor, processAlive)
	}, func(time.Duration) { sleeps++ })
	if err != nil {
		t.Fatalf("adoptRestartControl: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("remaining holder = %d, want 0 once the successor recorded itself", remaining)
	}
	if attempts != 2 {
		t.Fatalf("claimed %d times, want the refused claim then the successful one", attempts)
	}
	if sleeps != 1 {
		t.Fatalf("slept %d times, want the one wait between the refused and the successful claim", sleeps)
	}
	if got, err := core.ReadDesktopControlMarker(path); err != nil || got != successor {
		t.Fatalf("got %+v, err %v; want the successor %+v", got, err, successor)
	}
}

func TestAdoptRestartControl_AbandonsWhenThisProcessIsShuttingDown(t *testing.T) {
	attempts := 0
	remaining, err := adoptRestartControl(func() (restartControlClaim, error) {
		attempts++
		if attempts < 3 {
			return restartControlClaim{HolderPID: 50193}, nil
		}
		return restartControlClaim{}, errRestartControlReleased
	}, func(time.Duration) {})
	if !errors.Is(err, errRestartControlReleased) {
		t.Fatalf("err = %v, want errRestartControlReleased", err)
	}
	if remaining != 0 {
		t.Fatalf("remaining holder = %d, want 0 when the wait was abandoned", remaining)
	}
	if attempts != 3 {
		t.Fatalf("claimed %d times, want the wait abandoned on the claim that found the process released", attempts)
	}
}

func TestAdoptRestartControl_ReportsAHolderThatOutlivesTheWait(t *testing.T) {
	attempts := 0
	remaining, err := adoptRestartControl(func() (restartControlClaim, error) {
		attempts++
		return restartControlClaim{HolderPID: 50193}, nil
	}, func(time.Duration) {})
	if err != nil {
		t.Fatalf("adoptRestartControl: %v", err)
	}
	if remaining != 50193 {
		t.Fatalf("remaining holder = %d, want the surviving holder 50193", remaining)
	}
	if attempts != restartControlClaimAttempts {
		t.Fatalf("claimed %d times, want the wait bounded at %d", attempts, restartControlClaimAttempts)
	}
}

func TestAdoptRestartControlSteadily_TakesOverAHolderThatOutlivesTheHandoffWindow(t *testing.T) {
	stage := stageStrandedRestartControl(t)

	remaining, err := adoptRestartControl(stage.claim, func(time.Duration) {})
	if err != nil {
		t.Fatalf("adoptRestartControl: %v", err)
	}
	if remaining != stage.predecessor.PID {
		t.Fatalf("remaining holder = %d, want the predecessor %d outliving the window", remaining, stage.predecessor.PID)
	}
	if got := stage.recorded(t); got != stage.predecessor {
		t.Fatalf("record is %+v after the window; want it still the predecessor's", got)
	}

	if err := stage.predecessorExits(); err != nil {
		t.Fatalf("predecessor shutdown: %v", err)
	}

	sleeps := 0
	if err := adoptRestartControlSteadily(stage.claim, func(time.Duration) { sleeps++ }, restartControlRetryInterval); err != nil {
		t.Fatalf("adoptRestartControlSteadily: %v", err)
	}
	if sleeps != 1 {
		t.Fatalf("re-checked after %d waits, want the one wait before the record came free", sleeps)
	}
	if got := stage.recorded(t); got != stage.successor {
		t.Fatalf("record is %+v; want the successor %+v to have taken it over", got, stage.successor)
	}
}

// strandedRestartControl fakes the holder: the wait's shape is under test, not process control.
type strandedRestartControl struct {
	path        string
	predecessor core.DesktopControlMarker
	successor   core.DesktopControlMarker
	alive       map[int]bool
}

func stageStrandedRestartControl(t *testing.T) *strandedRestartControl {
	t.Helper()
	stage := &strandedRestartControl{
		path:        filepath.Join(t.TempDir(), "desktop-control.json"),
		predecessor: core.DesktopControlMarker{PID: 50193, ControlPort: 59775, StartedAtUnix: 100},
		successor:   core.DesktopControlMarker{PID: os.Getpid(), ControlPort: 56614, StartedAtUnix: 200},
	}
	stage.alive = map[int]bool{stage.predecessor.PID: true, stage.successor.PID: true}
	if _, err := claimDesktopControlMarker(stage.path, stage.predecessor, stage.processAlive); err != nil {
		t.Fatalf("stage the predecessor's record: %v", err)
	}
	return stage
}

func (s *strandedRestartControl) processAlive(pid int) bool { return s.alive[pid] }

func (s *strandedRestartControl) claim() (restartControlClaim, error) {
	return claimDesktopControlMarker(s.path, s.successor, s.processAlive)
}

func (s *strandedRestartControl) predecessorExits() error {
	delete(s.alive, s.predecessor.PID)
	return removeDesktopControlMarker(s.path, s.predecessor.PID)
}

func (s *strandedRestartControl) recorded(t *testing.T) core.DesktopControlMarker {
	t.Helper()
	got, err := core.ReadDesktopControlMarker(s.path)
	if err != nil {
		t.Fatalf("read the control record: %v", err)
	}
	return got
}

func TestAdoptRestartControlSteadily_EndsWhenThisProcessShutsDown(t *testing.T) {
	attempts := 0
	err := adoptRestartControlSteadily(func() (restartControlClaim, error) {
		attempts++
		if attempts < 3 {
			return restartControlClaim{HolderPID: 50193}, nil
		}
		return restartControlClaim{}, errRestartControlReleased
	}, func(time.Duration) {}, restartControlRetryInterval)
	if !errors.Is(err, errRestartControlReleased) {
		t.Fatalf("err = %v, want errRestartControlReleased", err)
	}
	if attempts != 3 {
		t.Fatalf("claimed %d times, want the re-check to end on the claim that found this process released", attempts)
	}
}
