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
	"strings"
	"testing"
	"time"
)

// descendantHold must outlast TestBareExecCommandLeavesTheWaitUnbounded's window
// yet be short enough that its blocked copier goroutine ends soon after.
const descendantHold = 10 * time.Second

// writePipeHolder writes a POSIX child that backgrounds a descendant holding its
// stdout, then exits: the state where Cmd.Wait never reaches EOF.
func writePipeHolder(t *testing.T) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "holder.sh")
	body := "#!/bin/sh\n" +
		// Outlives its parent, holding the inherited stdout.
		"sleep " + strconv.Itoa(int(descendantHold.Seconds())) + " &\n" +
		"echo fixture: descendant started\n" +
		"exit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return script
}

// awaitWait runs cmd.Wait on another goroutine and fails if it exceeds limit.
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

// TestCommandBoundsTheWaitOnADescendantsHeldPipe requires WaitDelay, specifically
// ErrWaitDelay, to end the wait on a descendant-held pipe.
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

// TestCommandBoundsTheWaitOnAChildThatNeverExits drives the context backstop
// that ends a child WaitDelay cannot reach.
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

// TestBareExecCommandLeavesTheWaitUnbounded shows a bare exec.Command still
// blocks on the same fixture; if os/exec gains a drain bound this fails.
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
	// Nothing ends this wait; the copier unblocks when the descendant exits.
	_ = cmd.Process.Kill()
}

// TestCommandArmsBothBounds pins the exported constructors to both bounds;
// exec.CommandContext sets Cancel where exec.Command leaves it nil.
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

// TestGitChildrenDetachNoMaintenance uses GIT_TRACE to require no detached
// maintenance from a Command push, and some from a plain one.
func TestGitChildrenDetachNoMaintenance(t *testing.T) {
	root := t.TempDir()
	run := func(cmd *exec.Cmd) string {
		t.Helper()
		cmd.Env = append(cmd.Env, "GIT_TRACE=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v: %s", cmd.Args, err, out)
		}
		return string(out)
	}
	git := func(dir string, args ...string) *exec.Cmd {
		cmd := Command("git", args...)
		cmd.Dir = dir
		return cmd
	}
	run(git(root, "init", "-q", "--bare", "origin.git"))
	run(git(root, "init", "-q", "-b", "main", "work"))
	work := filepath.Join(root, "work")
	run(git(work, "-c", "user.email=t@example", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "one"))
	run(git(work, "remote", "add", "origin", filepath.Join(root, "origin.git")))

	if trace := run(git(work, "push", "-q", "origin", "main")); strings.Contains(trace, "maintenance run") {
		t.Fatalf("a git child built by Command still detached maintenance:\n%s", trace)
	}

	run(git(work, "-c", "user.email=t@example", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "two"))
	plain := git(work, "push", "-q", "origin", "main")
	plain.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(root, "empty-gitconfig"))
	if trace := run(plain); !strings.Contains(trace, "maintenance run") {
		t.Fatalf("expected git's default to detach maintenance after a push, so this test reproduces the race:\n%s", trace)
	}
}
