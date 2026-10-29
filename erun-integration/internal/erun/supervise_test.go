package erun

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/infra/activity/activitylease"
	"github.com/sophium/erun/erun-integration/internal/harnessexec"
)

// writePipeHolderFixture writes a child whose descendant holds its stdout open. surviveSignal makes
// the child ignore SIGQUIT, so it must be killed and Process.Wait never returns on its own.
func writePipeHolderFixture(t *testing.T, surviveSignal bool) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "holder.sh")
	pidFile := filepath.Join(dir, "holder.pid")
	body := "#!/bin/sh\n" +
		"echo fixture: started\n" +
		// The descendant records its pid because only the cleanup can reach it.
		"sleep 300 &\n" +
		"echo $! > \"" + pidFile + "\"\n" +
		"echo fixture: waiting\n"
	if surviveSignal {
		body += "trap '' QUIT\n"
	}
	body += "while :; do sleep 1; done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	// A leaked holder gets adopted by a subreaper and makes the job report itself abandoned.
	t.Cleanup(func() { killFixturePipeHolder(t, pidFile) })
	return script
}

// killFixturePipeHolder kills the recorded pid and waits until it is gone.
func killFixturePipeHolder(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	deadline := time.Now().Add(10 * time.Second)
	for activitylease.ProcessAlive(pid) {
		_ = proc.Kill()
		if !time.Now().Before(deadline) {
			t.Errorf("the pipe holder (pid %d) is still alive %s after being killed, so it outlives the run that started it", pid, 10*time.Second)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// driveSupervise runs supervise as Run does, observing it from another goroutine because the
// failure under test is that it never returns.
func driveSupervise(t *testing.T, script string, timeout, delay time.Duration) (*bytes.Buffer, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := harnessexec.CommandContext(ctx, "/bin/sh", script)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stdout
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fixture: %v", err)
	}

	killed := make(chan error, 1)
	go func() { killed <- supervise(cmd, cancel, timeout, delay) }()

	select {
	case err := <-killed:
		return &stdout, err
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("supervise never returned: it was waiting on a child whose descendant %q still held the inherited stdout pipe open; the wait is unbounded when the child does not exit", script)
		return &stdout, nil
	}
}

// TestSuperviseBoundsTheWaitOnADescendantsHeldPipe: a killed child's descendant holds the pipe, and the
// drain must still be bounded.
func TestSuperviseBoundsTheWaitOnADescendantsHeldPipe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture is a POSIX shell script; the pipe-inheritance defect it drives is POSIX-shaped")
	}

	script := writePipeHolderFixture(t, false)
	stdout, err := driveSupervise(t, script, 2*time.Second, 10*time.Second)

	if !errors.Is(err, errTimeout) {
		t.Fatalf("expected the timeout to end the run, got %v; stdout=%q", err, stdout.String())
	}
}

// TestSuperviseBoundsTheWaitOnAChildThatDoesNotExit: a child that never exits needs the context
// cancellation for WaitDelay to apply.
func TestSuperviseBoundsTheWaitOnAChildThatDoesNotExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture is a POSIX shell script; the pipe-inheritance defect it drives is POSIX-shaped")
	}

	script := writePipeHolderFixture(t, true)
	stdout, err := driveSupervise(t, script, 2*time.Second, 10*time.Second)

	if !errors.Is(err, errTimeout) {
		t.Fatalf("expected the timeout to end the run, got %v; stdout=%q", err, stdout.String())
	}
}
