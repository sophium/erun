package core

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
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

func TestRestartDesktopApp_RefusedWhenPostTransportFails(t *testing.T) {
	post := func(context.Context, int, string) (DesktopRestartResponse, error) {
		return DesktopRestartResponse{}, net.ErrClosed
	}
	deps := fakeRestartDeps(t, DesktopControlMarker{PID: 123, ControlPort: 4242}, nil, true, post)
	outcome := RestartDesktopApp(context.Background(), deps, "orch-1", false)
	if outcome.Status != model.DesktopRestartRefused {
		t.Fatalf("status = %q, want %q", outcome.Status, model.DesktopRestartRefused)
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

	// Re-running this binary with no tests yields a real exited pid on every platform.
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatalf("spawn %s to get an exited pid: %v", os.Args[0], err)
	}
	if DesktopProcessAlive(cmd.Process.Pid) {
		t.Fatalf("pid %d exited and must not read as alive", cmd.Process.Pid)
	}
}

func TestPostDesktopRestart_TransportErrorWhenUnreachable(t *testing.T) {
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
