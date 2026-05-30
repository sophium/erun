package main

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

// Tests in this file pin the respawn-on-early-exit contract that
// fixes #407 (kubectl port-forward died before the in-pod listener
// bound, so the contribute-app open flow gave up before the headless
// erun-app finished its CLI rebuild). They exercise
// waitForContributeAppReachable with a fake spawn that emits
// short-lived `sh -c` subprocesses, which keeps the exec.Cmd /
// ProcessState integration honest without needing a real kubectl.

func skipIfNoSh(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("respawn tests use /bin/sh; skipping on Windows")
	}
}

// freeTCPPort returns a port that was bindable a moment ago. Tests
// re-bind it on demand for the fake HTTP server; the race window
// between this call and the re-bind is acceptable for local/CI use.
func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// startTinyHTTPServerOn binds an HTTP server on the requested port
// that answers `200 OK` to every request. It models the in-pod
// `erun app --headless` server becoming reachable through the fake
// kubectl port-forward so the HTTP probe in
// canReachLocalContributeAppEndpoint succeeds.
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

// shrinkContributeAppTimings replaces the package's reach deadline +
// poll interval with test-fast values, restoring originals on cleanup.
func shrinkContributeAppTimings(t *testing.T, deadline, poll time.Duration) {
	t.Helper()
	origDeadline := contributeAppPortReachableTimeout
	origPoll := contributeAppPortReachablePollInterval
	contributeAppPortReachableTimeout = deadline
	contributeAppPortReachablePollInterval = poll
	t.Cleanup(func() {
		contributeAppPortReachableTimeout = origDeadline
		contributeAppPortReachablePollInterval = origPoll
	})
}

// spawnPlan is the per-call recipe a test supplies to the fake
// spawnContributeAppForwardCmd.
type spawnPlan struct {
	// script runs under `sh -c`. Use `exit 1` to simulate kubectl
	// dying after a connection-refused inside the pod, or `sleep N`
	// to simulate a live forward.
	script string
	// stderr is the buffer contents exitedWithError will surface for
	// this spawn. The real kubectl writes to its captured buffer
	// while running; the test pre-populates it because the fake
	// subprocess doesn't.
	stderr string
	// onSpawn, if non-nil, runs synchronously before the cmd starts.
	// Used to bring up the fake HTTP server on the respawn that
	// should make the wait succeed.
	onSpawn func()
}

// installFakeContributeSpawn replaces spawnContributeAppForwardCmd
// with a counter-aware stub driven by `plan`. The counter is bumped
// before each call so tests can assert how many times kubectl was
// spawned overall.
func installFakeContributeSpawn(t *testing.T, plan func(n int) spawnPlan, counter *int32) {
	t.Helper()
	orig := spawnContributeAppForwardCmd
	spawnContributeAppForwardCmd = func(ctx context.Context, _ []string) (*exec.Cmd, *bytes.Buffer, error) {
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
		// Reaping is owned by the forward (startReap), not the spawn
		// fake — match the production shape so the race detector sees
		// the same synchronisation discipline.
		return cmd, bytes.NewBufferString(p.stderr), nil
	}
	t.Cleanup(func() { spawnContributeAppForwardCmd = orig })
}

// initialContributeSpawn does the first spawn the same way
// startContributeAppForward would, returning a forward + canned args
// the wait loop can use to respawn.
func initialContributeSpawn(t *testing.T, port int) (*contributeAppForward, []string) {
	t.Helper()
	args := []string{"port-forward", "deployment/test", fmt.Sprintf("%d:%d", port, port)}
	cmd, stderr, err := spawnContributeAppForwardCmd(context.Background(), args)
	if err != nil {
		t.Fatalf("initial spawn: %v", err)
	}
	return newContributeAppForward(cmd, stderr, port), args
}

// TestWaitForContributeAppReachableSucceedsWhenAlreadyServing covers
// the reuse path: something is already listening on the local port
// so the wait just polls HTTP without owning any kubectl process.
func TestWaitForContributeAppReachableSucceedsWhenAlreadyServing(t *testing.T) {
	shrinkContributeAppTimings(t, 2*time.Second, 25*time.Millisecond)
	port := freeTCPPort(t)
	closeFn := startTinyHTTPServerOn(t, port)
	t.Cleanup(closeFn)

	if err := waitForContributeAppReachable(context.Background(), port, nil, nil); err != nil {
		t.Fatalf("expected reachable, got %v", err)
	}
}

// TestWaitForContributeAppReachableRespawnsKubectlOnEarlyExit locks
// the bug from #407. The first kubectl spawn dies immediately
// (simulating kubectl's "lost connection to pod" when the in-pod
// listener has not yet bound during the CLI rebuild window). The
// loop must respawn and succeed once the in-pod side is up.
func TestWaitForContributeAppReachableRespawnsKubectlOnEarlyExit(t *testing.T) {
	skipIfNoSh(t)
	shrinkContributeAppTimings(t, 5*time.Second, 25*time.Millisecond)
	port := freeTCPPort(t)

	var spawnCount int32
	var srvCloseFn func()
	installFakeContributeSpawn(t, func(n int) spawnPlan {
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

	forward, args := initialContributeSpawn(t, port)
	t.Cleanup(forward.stop)

	if err := waitForContributeAppReachable(context.Background(), port, forward, args); err != nil {
		t.Fatalf("expected reachable after one respawn, got %v", err)
	}
	if n := atomic.LoadInt32(&spawnCount); n != 2 {
		t.Errorf("expected exactly 2 spawn calls (initial + 1 respawn), got %d", n)
	}
}

// TestWaitForContributeAppReachableTimesOutPreservesLastExit covers
// the deadline path. kubectl keeps dying (a real failure mode: wrong
// context, deployment missing, host port busy on the pod side). The
// deadline error must include the last captured kubectl stderr so
// the user sees the cause, not just a 5-minute timeout banner.
func TestWaitForContributeAppReachableTimesOutPreservesLastExit(t *testing.T) {
	skipIfNoSh(t)
	shrinkContributeAppTimings(t, 250*time.Millisecond, 25*time.Millisecond)
	port := freeTCPPort(t)

	const exitStderr = "Error from server (NotFound): deployments.apps \"erun\" not found"
	var spawnCount int32
	installFakeContributeSpawn(t, func(_ int) spawnPlan {
		return spawnPlan{script: "exit 1", stderr: exitStderr}
	}, &spawnCount)

	forward, args := initialContributeSpawn(t, port)
	t.Cleanup(forward.stop)

	err := waitForContributeAppReachable(context.Background(), port, forward, args)
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

// TestStopAbortsRespawn covers the race where the user toggles
// contribute mode off (or the env closes) while the wait is mid-
// respawn. forward.adopt must refuse to install the new cmd, the
// freshly-spawned kubectl must be killed, and the wait must return a
// "stopped" error rather than burning the full deadline.
func TestStopAbortsRespawn(t *testing.T) {
	skipIfNoSh(t)
	shrinkContributeAppTimings(t, 5*time.Second, 25*time.Millisecond)
	port := freeTCPPort(t)

	var spawnCount int32
	installFakeContributeSpawn(t, func(_ int) spawnPlan {
		return spawnPlan{script: "exit 1", stderr: "error: lost connection to pod"}
	}, &spawnCount)

	forward, args := initialContributeSpawn(t, port)

	done := make(chan error, 1)
	go func() {
		done <- waitForContributeAppReachable(context.Background(), port, forward, args)
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

// TestAdoptRefusesAfterStop is the unit-level invariant behind
// TestStopAbortsRespawn: once stop() has set the stopped flag,
// subsequent adopt calls must return false so the caller can kill
// the orphaned kubectl.
func TestAdoptRefusesAfterStop(t *testing.T) {
	forward := &contributeAppForward{}
	forward.stop()
	if forward.adopt(&exec.Cmd{}, &bytes.Buffer{}) {
		t.Fatalf("adopt should return false after stop()")
	}
}
