//go:build !windows

package main

import (
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func startSleepingSession(t *testing.T) terminalSession {
	t.Helper()
	session, err := startTerminalSession(startTerminalSessionParams{
		Executable: "/bin/sh",
		Args:       []string{"-c", "sleep 300"},
		Cols:       80,
		Rows:       24,
	})
	if err != nil {
		t.Fatalf("startTerminalSession: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// TestAliveIsFalseForAKilledAndReapedProcess reproduces the stale-liveness
// defect. Close() kills the session's process and reaps it through Wait, and
// once Wait has run Go's Process.Signal answers os.ErrProcessDone rather than
// ESRCH for a process that is demonstrably gone. A probe that recognizes only
// ESRCH therefore reports a dead process as alive, which is what every caller
// gating cleanup on Alive() — a "did the kill land" check, the stale-shell
// detector — reads.
func TestAliveIsFalseForAKilledAndReapedProcess(t *testing.T) {
	session := startSleepingSession(t)
	pid := session.Pid()

	// Control: the same probe on the same session answers true while the process
	// really is running, so a false answer below is the reaping and not the probe.
	if !session.Alive() {
		t.Fatalf("a running process must read alive")
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Assert the process is gone independently of Alive(), so a red below is
	// about the liveness probe and not about a kill that never landed.
	if err := syscall.Kill(pid, syscall.Signal(0)); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("expected pid %d to be gone after Close, kill(pid,0) = %v", pid, err)
	}

	if session.Alive() {
		probe := session.(*unixTerminalSession).cmd.Process.Signal(syscall.Signal(0))
		t.Fatalf(
			"Alive() must be false for a killed-and-reaped process (pid %d); Process.Signal(0) = %v, errors.Is(err, syscall.ESRCH) = %v",
			pid, probe, errors.Is(probe, syscall.ESRCH),
		)
	}
}

// TestAliveIsTrueForAProcessThatIsNotYetReaped is the other half of the probe's
// contract, and a control: signal 0 answers nil while the pid entry still
// exists, so a process this session killed but has not waited on still reads
// alive. The fix must narrow the answer for a reaped process only — collapsing
// the probe to "did signal 0 fail" would report a kill that has not been
// collected yet as an exit.
func TestAliveIsTrueForAProcessThatIsNotYetReaped(t *testing.T) {
	session := startSleepingSession(t)
	pid := session.Pid()

	// Kill the direct child without waiting on it, leaving it as a zombie.
	if err := session.(*unixTerminalSession).cmd.Process.Kill(); err != nil {
		t.Fatalf("kill pid %d: %v", pid, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !processIsZombie(pid) {

		if time.Now().After(deadline) {
			t.Fatalf("pid %d never became a zombie", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !session.Alive() {
		t.Fatalf("a killed but not-yet-reaped process must still read alive (pid %d)", pid)
	}
}

// processIsZombie reports whether the pid names a process that has exited but
// has not been reaped, so the pid entry still exists.
func processIsZombie(pid int) bool {
	if pid <= 0 {
		return false
	}
	out, err := exec.Command("ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
}

// TestCloseReapsWholeProcessGroup pins the orphan fix: Close() must reap the
// session's whole process group, so the kubectl exec grandchild that `erun open`
// spawns cannot outlive it holding a remote exec stream open. Only the session's
// own group is signalled, leaving other tabs and app instances untouched.
func TestCloseReapsWholeProcessGroup(t *testing.T) {
	session, err := startTerminalSession(startTerminalSessionParams{
		Executable: "/bin/sh",
		Args:       []string{"-c", `sleep 300 & echo "CHILD_PID=$!"; wait`},
		Cols:       80,
		Rows:       24,
	})
	if err != nil {
		t.Fatalf("startTerminalSession: %v", err)
	}

	childPid := readChildPid(t, session)
	if !processIsRunning(childPid) {
		t.Fatalf("grandchild %d not alive before Close()", childPid)
	}

	_ = session.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !processIsRunning(childPid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(childPid, syscall.SIGKILL)
	t.Fatalf("grandchild %d survived Close(); the kubectl exec orphan leak is back", childPid)
}

// processIsRunning reports whether the pid names a process that is still
// executing. A signal-0 probe cannot answer that on its own: the grandchild is
// reparented on kill, and where the new parent never reaps (a container whose
// PID 1 is an application, not an init) the pid lingers as a zombie that
// signal-0 still accepts. Reading the process state distinguishes "killed but
// unreaped" from "still running", which is what the test is asserting.
func processIsRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	out, err := exec.Command("ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(out))
	return state != "" && !strings.HasPrefix(state, "Z")
}

var childPidPattern = regexp.MustCompile(`CHILD_PID=(\d+)`)

func readChildPid(t *testing.T, session terminalSession) int {
	t.Helper()
	var output []byte
	buffer := make([]byte, 4096)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		n, err := session.Read(buffer)
		if n > 0 {
			output = append(output, buffer[:n]...)
			if match := childPidPattern.FindSubmatch(output); match != nil {
				pid, convErr := strconv.Atoi(string(match[1]))
				if convErr != nil {
					t.Fatalf("parse grandchild pid from %q: %v", match[1], convErr)
				}
				return pid
			}
		}
		if err != nil {
			break
		}
	}
	_ = session.Close()
	t.Fatalf("grandchild pid never appeared in session output: %q", output)
	return 0
}

// TestCloseIsIdempotentAfterTheProcessExits pins the teardown contract: closing
// a session whose PTY the streaming goroutine already closed must succeed. It is
// the desired end state, and reporting it as a failure made "close environment"
// abort its unwind, leaving the sidebar rendering a torn-down env as open.
func TestCloseIsIdempotentAfterTheProcessExits(t *testing.T) {
	session, err := startTerminalSession(startTerminalSessionParams{
		Executable: "/bin/sh",
		Args:       []string{"-c", "exit 0"},
		Cols:       80,
		Rows:       24,
	})
	if err != nil {
		t.Fatalf("startTerminalSession: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("closing an already-closed session must succeed, got: %v", err)
	}
}
