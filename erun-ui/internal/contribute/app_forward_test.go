package contribute

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func skipIfNoSh(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("respawn tests use /bin/sh; skipping on Windows")
	}
}

// freeTCPPort returns a port that was bindable a moment ago.
func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %v is not TCP", ln.Addr())
	}
	_ = ln.Close()
	return addr.Port
}

func startTinyHTTPServerOn(t *testing.T, port int) func() {
	t.Helper()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("bind %d: %v", port, err)
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}
	go func() { _ = srv.Serve(ln) }()
	return func() { _ = srv.Close() }
}

func shrinkAppTimings(t *testing.T, deadline, poll time.Duration) {
	t.Helper()
	origDeadline := appReachableTimeout
	origPoll := appReachablePollInterval
	appReachableTimeout = deadline
	appReachablePollInterval = poll
	t.Cleanup(func() {
		appReachableTimeout = origDeadline
		appReachablePollInterval = origPoll
	})
}

type spawnPlan struct {
	script string
	// The fake does not stream stderr, so it is pre-populated.
	stderr  string
	onSpawn func()
}

func installFakeSpawn(t *testing.T, plan func(n int) spawnPlan, counter *int32) {
	t.Helper()
	orig := spawnForwardCmd
	spawnForwardCmd = func(ctx context.Context, _ []string) (*exec.Cmd, *bytes.Buffer, error) {
		n := atomic.AddInt32(counter, 1)
		p := plan(int(n))
		if p.onSpawn != nil {
			p.onSpawn()
		}
		cmd := exec.CommandContext(ctx, "sh", "-c", p.script)
		cmd.Stdout = io.Discard
		if err := cmd.Start(); err != nil {
			return nil, nil, err
		}
		// The forward reaps (startReap), as in production.
		return cmd, bytes.NewBufferString(p.stderr), nil
	}
	t.Cleanup(func() { spawnForwardCmd = orig })
}

func initialSpawn(t *testing.T, port int) (*appForward, []string) {
	t.Helper()
	args := []string{"port-forward", "deployment/test", fmt.Sprintf("%d:%d", port, port)}
	cmd, stderr, err := spawnForwardCmd(context.Background(), args)
	if err != nil {
		t.Fatalf("initial spawn: %v", err)
	}
	return newAppForward(cmd, stderr, port), args
}

func TestWaitForContributeAppReachableSucceedsWhenAlreadyServing(t *testing.T) {
	shrinkAppTimings(t, 2*time.Second, 25*time.Millisecond)
	port := freeTCPPort(t)
	closeFn := startTinyHTTPServerOn(t, port)
	t.Cleanup(closeFn)

	if err := waitForAppReachable(context.Background(), port, nil, nil); err != nil {
		t.Fatalf("expected reachable, got %v", err)
	}
}

// The first kubectl dies immediately, as when the in-pod listener has not bound yet.
func TestWaitForContributeAppReachableRespawnsKubectlOnEarlyExit(t *testing.T) {
	skipIfNoSh(t)
	shrinkAppTimings(t, 5*time.Second, 25*time.Millisecond)
	port := freeTCPPort(t)

	var spawnCount int32
	var srvCloseFn func()
	installFakeSpawn(t, func(n int) spawnPlan {
		if n == 1 {
			return spawnPlan{script: "exit 1", stderr: "error: lost connection to pod"}
		}
		return spawnPlan{
			script: "sleep 60",
			onSpawn: func() {
				if srvCloseFn == nil {
					srvCloseFn = startTinyHTTPServerOn(t, port)
				}
			},
		}
	}, &spawnCount)
	t.Cleanup(func() {
		if srvCloseFn != nil {
			srvCloseFn()
		}
	})

	forward, args := initialSpawn(t, port)
	t.Cleanup(forward.stop)

	if err := waitForAppReachable(context.Background(), port, forward, args); err != nil {
		t.Fatalf("expected reachable after one respawn, got %v", err)
	}
	if n := atomic.LoadInt32(&spawnCount); n != 2 {
		t.Errorf("expected exactly 2 spawn calls (initial + 1 respawn), got %d", n)
	}
}

// The deadline error must include the last kubectl stderr.
func TestWaitForContributeAppReachableTimesOutPreservesLastExit(t *testing.T) {
	skipIfNoSh(t)
	shrinkAppTimings(t, 250*time.Millisecond, 25*time.Millisecond)
	port := freeTCPPort(t)

	const exitStderr = "Error from server (NotFound): deployments.apps \"erun\" not found"
	var spawnCount int32
	installFakeSpawn(t, func(_ int) spawnPlan {
		return spawnPlan{script: "exit 1", stderr: exitStderr}
	}, &spawnCount)

	forward, args := initialSpawn(t, port)
	t.Cleanup(forward.stop)

	err := waitForAppReachable(context.Background(), port, forward, args)
	if err == nil {
		t.Fatalf("expected deadline error, got nil")
	}
	if !strings.Contains(err.Error(), "did not become reachable") {
		t.Errorf("expected deadline message, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), exitStderr) {
		t.Errorf("expected last kubectl exit (%q) in error, got %q", exitStderr, err.Error())
	}
	if n := atomic.LoadInt32(&spawnCount); n < 2 {
		t.Errorf("expected at least 2 spawn calls (initial + ≥1 respawn) before timeout, got %d", n)
	}
}

func TestStopAbortsRespawn(t *testing.T) {
	skipIfNoSh(t)
	shrinkAppTimings(t, 5*time.Second, 25*time.Millisecond)
	port := freeTCPPort(t)

	var spawnCount int32
	installFakeSpawn(t, func(_ int) spawnPlan {
		return spawnPlan{script: "exit 1", stderr: "error: lost connection to pod"}
	}, &spawnCount)

	forward, args := initialSpawn(t, port)

	done := make(chan error, 1)
	go func() {
		done <- waitForAppReachable(context.Background(), port, forward, args)
	}()
	time.Sleep(100 * time.Millisecond)
	forward.stop()

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected stopped error, got nil")
		}
		if !strings.Contains(err.Error(), "stopped before becoming reachable") {
			t.Errorf("expected stopped-before-reachable error, got %q", err.Error())
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("wait did not return after stop()")
	}
}

func TestAdoptRefusesAfterStop(t *testing.T) {
	forward := &appForward{}
	forward.stop()
	if forward.adopt(&exec.Cmd{}, &bytes.Buffer{}) {
		t.Fatalf("adopt should return false after stop()")
	}
}

func TestContributeAppLaunchLeavesTheDesktopRebuildToTheWrapper(t *testing.T) {
	got := appLaunchCommand(34567)
	want := "erun app --headless --port 34567\n"
	if got != want {
		t.Fatalf("appLaunchCommand(34567) = %q, want %q", got, want)
	}
}
