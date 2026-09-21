package harnessexec

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// descendantHold is how long the fixture's descendant holds the child's
// inherited stdout open after the child itself is gone. It has to outlast the
// window TestBareExecCommandLeavesTheWaitUnbounded asserts in, and it is short
// enough that the copier goroutine that window leaves blocked resolves on its
// own shortly after the test returns rather than living out the run.
const descendantHold = 10 * time.Second

// writePipeHolder writes the fixture both tests drive: a child that forks a
// descendant inheriting its stdout, announces it, and then exits on its own.
//
// The shape is the defect exactly. The child concludes, so os/exec reaps it
// and Process.Wait returns; the descendant still holds the write end of the
// stdout pipe, so the copier goroutine os/exec runs against that pipe never
// reaches EOF and Cmd.Wait has nothing left to conclude from. Nothing is slow
// here and nothing has to be killed: the fixture is a child that has already
// exited, which is what makes the wait unbounded rather than merely long.
//
// It is a POSIX shell script, so the two tests that drive it are POSIX-only --
// the pipe-inheritance defect they reproduce is POSIX-shaped, the same
// limitation the supervisor's own regression carries.
func writePipeHolder(t *testing.T) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "holder.sh")
	body := "#!/bin/sh\n" +
		// Outlives its parent by design: this is the process that keeps the
		// inherited stdout open after the child it came from is gone.
		"sleep " + strconv.Itoa(int(descendantHold.Seconds())) + " &\n" +
		"echo fixture: descendant started\n" +
		"exit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return script
}

// awaitWait observes cmd.Wait from its own goroutine and returns what it
// returned, failing if it has not returned within limit. The return has to be
// observed from another goroutine because the failure being reproduced is
// precisely "it never returns", which the goroutine that would have to notice
// it cannot assert.
func awaitWait(t *testing.T, cmd *exec.Cmd, limit time.Duration, stdout *bytes.Buffer) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		_ = cmd.Process.Kill()
		t.Fatalf("the wait never returned within %s; stdout=%q", limit, stdout.String())
		return nil
	}
}

// TestCommandBoundsTheWaitOnADescendantsHeldPipe is the regression for the
// harness hang: the fixture's child exits, a descendant of it holds the
// inherited stdout open, and the harness's wait still concludes.
//
// The bound it concludes on is Cmd.WaitDelay's, which os/exec applies to a
// child that has exited whether or not the Cmd carries a context. Asserting
// ErrWaitDelay specifically, rather than merely "it returned", is what pins
// that: a wait that returned for some other reason would mean the fixture
// stopped reproducing the state this test exists for.
func TestCommandBoundsTheWaitOnADescendantsHeldPipe(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the fixture is a POSIX shell script; the pipe-inheritance defect it drives is POSIX-shaped")
	}

	cmd := command(context.Background(), 1*time.Second, HangNet, "/bin/sh", writePipeHolder(t))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stdout
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fixture: %v", err)
	}

	started := time.Now()
	err := awaitWait(t, cmd, 30*time.Second, &stdout)
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("expected the drain bound to end the wait, got %v; stdout=%q", err, stdout.String())
	}
	if elapsed := time.Since(started); elapsed > descendantHold {
		t.Fatalf("the wait concluded after %s, which is the descendant releasing the pipe rather than the bound ending it", elapsed)
	}
}

// TestCommandBoundsTheWaitOnAChildThatNeverExits covers the shape Cmd.WaitDelay
// cannot reach: the child itself never exits, so Process.Wait never returns and
// the post-exit path that consults the delay is never reached. The harness's
// own backstop is a context cancellation, and this test drives it at test speed
// rather than waiting one out.
//
// Without the backstop the wait is unbounded, so the failure this asserts is
// "the wait returned at all"; the guard against the backstop being dropped is
// the watchdog in awaitWait, which fails the test rather than hanging it.
func TestCommandBoundsTheWaitOnAChildThatNeverExits(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the fixture is a POSIX shell script; the wait it drives is POSIX-shaped")
	}

	cmd := command(context.Background(), 1*time.Second, 2*time.Second, "/bin/sh", "-c", "while :; do sleep 1; done")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stdout
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fixture: %v", err)
	}

	started := time.Now()
	err := awaitWait(t, cmd, 30*time.Second, &stdout)
	if err == nil {
		t.Fatalf("a child that never exited reported a clean run; stdout=%q", stdout.String())
	}
	if elapsed := time.Since(started); elapsed > 20*time.Second {
		t.Fatalf("the backstop took %s to end a child that never exited", elapsed)
	}
}

// TestBareExecCommandLeavesTheWaitUnbounded drives the same fixture through
// exec.Command, the constructor this package exists to replace, and requires
// the wait to still be blocked when the bound has already ended the run above.
//
// It is the reproduction the reported failure described, kept as a test rather
// than as prose: it is what makes exec_bound_test.go's refusal to accept a bare
// exec.Command anywhere in the harness a statement about a real wedge instead
// of a style preference. If os/exec ever grows a default drain bound this fails,
// and the failure means the constructor below no longer needs its bound -- not
// that the fixture drifted.
func TestBareExecCommandLeavesTheWaitUnbounded(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the fixture is a POSIX shell script; the pipe-inheritance defect it drives is POSIX-shaped")
	}

	cmd := exec.Command("/bin/sh", writePipeHolder(t))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stdout
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fixture: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		t.Fatalf("a bare exec.Command concluded after %s (%v); the fixture no longer reproduces the unbounded wait, so this test no longer shows why Command arms a bound", descendantHold, err)
	case <-time.After(2 * time.Second):
	}
	// The child has already exited and the descendant still holds the pipe, so
	// there is nothing left to end this wait; the copier goroutine unblocks
	// when the descendant exits, which is why the fixture keeps that short.
	_ = cmd.Process.Kill()
}

// TestCommandArmsBothBounds pins the exported constructors to the two arms the
// package comment promises, so the tests above cannot keep passing against the
// inner function while the ones the harness actually calls lose an arm.
//
// A Cmd carrying a cancellable context is what os/exec keys the backstop on,
// and it is observable without starting anything: exec.CommandContext sets
// Cancel where exec.Command leaves it nil.
func TestCommandArmsBothBounds(t *testing.T) {
	t.Parallel()
	cmd := Command("true")
	if cmd.WaitDelay != WaitDelay {
		t.Errorf("Command left WaitDelay at %s, want %s", cmd.WaitDelay, WaitDelay)
	}
	if cmd.Cancel == nil {
		t.Error("Command built a Cmd with no cancellable context, so the HangNet backstop has nothing to cancel")
	}
	cmd = CommandContext(context.Background(), "true")
	if cmd.WaitDelay != WaitDelay {
		t.Errorf("CommandContext left WaitDelay at %s, want %s", cmd.WaitDelay, WaitDelay)
	}
	if cmd.Cancel == nil {
		t.Error("CommandContext built a Cmd with no cancellable context, so the HangNet backstop has nothing to cancel")
	}
}
