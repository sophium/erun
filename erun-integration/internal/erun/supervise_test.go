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

	eruncommon "github.com/sophium/erun/erun-common"

	"github.com/sophium/erun/erun-integration/internal/harnessexec"
)

// writePipeHolderFixture writes a shell child that starts a descendant
// inheriting its stdout and outliving it, then keeps running itself. Whatever
// ends the child, the descendant still holds the pipe, so the output copier
// goroutine inside os/exec never reaches EOF and cmd.Wait cannot conclude on
// its own.
//
// surviveSignal additionally makes the child ignore SIGQUIT, which is the
// signal supervise sends first. The pair of fixtures exists because the two
// states reach different code in os/exec: a child that dies to that signal is
// reaped, while one that ignores it has to be killed outright -- and it is the
// second state in which Process.Wait itself never returns.
func writePipeHolderFixture(t *testing.T, surviveSignal bool) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "holder.sh")
	pidFile := filepath.Join(dir, "holder.pid")
	body := "#!/bin/sh\n" +
		"echo fixture: started\n" +
		// Outlives its parent by design: this is the process that keeps the
		// inherited stdout open after the child it came from is gone. It
		// records its own pid on the way, because nothing else can reach it
		// afterwards: the child it came from is gone, and it is not the
		// process the test started, so the cleanup below is the only thing
		// that can end it.
		"sleep 300 &\n" +
		"echo $! > \"" + pidFile + "\"\n" +
		"echo fixture: waiting\n"
	if surviveSignal {
		body += "trap '' QUIT\n"
	}
	// Outlasts the timeout the test passes, so supervise has to end this child
	// rather than see it exit on its own.
	body += "while :; do sleep 1; done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	// Ending it here is not tidiness. The pipe holder outlives the run by
	// design, so left alive it also outlives the package and the command that
	// ran it -- and a supervisor adopts it, as a child subreaper, once the
	// process it came from is gone. The job that ran this suite then reports
	// itself abandoned for work it started and never waited for, which is a
	// clean gate recorded as anything but.
	t.Cleanup(func() { killFixturePipeHolder(t, pidFile) })
	return script
}

// killFixturePipeHolder ends the process path records the pid of, and waits
// for it to actually go rather than only for the kill to be sent. A file that
// is missing or unreadable means the fixture never reached that line, which
// the assertions in the test above it have already reported.
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
	for eruncommon.ProcessAlive(pid) {
		_ = proc.Kill()
		if !time.Now().Before(deadline) {
			t.Errorf("the pipe holder (pid %d) is still alive %s after being killed, so it outlives the run that started it", pid, 10*time.Second)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// driveSupervise starts fixture and runs supervise against it exactly the way
// Run does: a cancellable context (so WaitDelay's cancellation clause can arm)
// and short caps, so a regression fails this test in seconds rather than
// hanging the package. The wait is observed from another goroutine because the
// failure being reproduced is precisely "it never returns", which cannot be
// asserted from the goroutine that would have to observe the return.
//
// It returns the observed stdout alongside supervise's result so a failing
// case can report what the fixture actually managed to write before the wait
// was cut short.
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
		return nil, nil
	}
}

// A child that exits leaves its stdout pipe open for as long as a descendant
// keeps it, so the copier goroutine os/exec runs against that pipe never sees
// EOF and cmd.Wait waits on the pipe rather than the child. That is the state
// this reproduces: the child is killed, the descendant holds the pipe, and
// without a bound on the *drain* the harness blocks forever.
func TestSuperviseBoundsTheWaitOnADescendantsHeldPipe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture is a POSIX shell script; the pipe-inheritance defect it drives is POSIX-shaped")
	}

	// Long enough that the fixture is still running when the timeout fires.
	script := writePipeHolderFixture(t, false)
	stdout, err := driveSupervise(t, script, 2*time.Second, 10*time.Second)

	if !errors.Is(err, errTimeout) {
		t.Fatalf("expected the timeout to end the run, got %v; stdout=%q", err, stdout.String())
	}
}

// The state above is not the one that wedged the suite. There the child did
// not exit at all, and that is a strictly harder case: a child that never
// exits means Process.Wait never returns, so the post-exit path that consults
// WaitDelay -- the one that bounds the drain above -- is never reached, and
// WaitDelay does not arm on its own because os/exec only runs the goroutine
// enforcing it for a Cmd carrying a cancellable context. The deadline has to
// come from the cancellation on the timeout path instead.
//
// This fixture ignores the signal supervise sends first, so the child is
// genuinely still running when the timeout fires and only the kill behind the
// cancellation can end it. The wait still has to conclude.
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
