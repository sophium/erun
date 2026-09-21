package erun

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// When a child the harness runs exits, the harness still has to learn that it
// exited. os/exec ordinarily learns it from the child's stdout/stderr pipes
// reaching EOF, and those pipes reach EOF only once *every* process holding
// them has closed them -- including one the child started and left behind. So
// a child that exits with an inherited-pipe descendant still running leaves
// cmd.Wait blocked on a pipe nothing is going to close, and no kill rescues
// it: the child is already gone, so Process.Kill is a no-op, and the copier
// goroutines blocked reading those pipes never look at signals at all.
//
// That is the state this reproduces. A child starts a descendant that
// inherits its stdout and outlives it, fails to finish inside the timeout, and
// is killed -- exactly the sequence the harness runs when a command is slow
// under the gate's CPU contention. Killing the child cannot end the wait,
// because the descendant still holds the pipe; the harness used to block
// there forever, and one scenario's abandoned descendant then stopped being a
// failed scenario and became a package-wide `panic: test timed out`, failing
// every other test in erun-integration with it.
func TestSuperviseBoundsTheWaitOnADescendantsHeldPipe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture is a POSIX shell script; the pipe-inheritance defect it drives is POSIX-shaped")
	}

	script := filepath.Join(t.TempDir(), "holder.sh")
	body := "#!/bin/sh\n" +
		"echo fixture: started\n" +
		// Outlives its parent by design: this is the process that keeps the
		// inherited stdout open after the child it came from is gone.
		"sleep 120 &\n" +
		"echo fixture: waiting\n" +
		// Outlasts the timeout below, so supervise has to kill this child
		// rather than see it exit on its own.
		"sleep 120\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cmd := exec.Command("/bin/sh", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fixture: %v", err)
	}
	// The wait is observed from another goroutine so a regression fails this
	// test in seconds instead of hanging it: the failure being reproduced is
	// precisely "it never returns", which cannot be asserted from the
	// goroutine that would have to observe the return. Both caps are short
	// enough to keep that bounded and long enough that the fixture is still
	// running when the timeout below fires.
	killed := make(chan error, 1)
	go func() { killed <- supervise(cmd, 2*time.Second, 10*time.Second) }()

	select {
	case err := <-killed:
		if !errors.Is(err, errTimeout) {
			t.Fatalf("expected the timeout to end the run, got %v; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("supervise never returned: it was waiting on a child that had already exited, blocked on a stdout pipe its own descendant %q still held open", script)
	}
}
