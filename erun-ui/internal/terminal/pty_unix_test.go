//go:build !windows

package terminal

import (
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func unixSessionProcess(t *testing.T, session PTY) *os.Process {
	t.Helper()
	unixSession, ok := session.(*unixTerminalSession)
	if !ok {
		t.Fatalf("session is %T, not a unix terminal session", session)
	}
	return unixSession.cmd.Process
}

func startSleepingSession(t *testing.T) PTY {
	t.Helper()
	session, err := StartPTY(StartParams{
		Executable: "/bin/sh",
		Args:       []string{"-c", "sleep 300"},
		Cols:       80,
		Rows:       24,
	})
	if err != nil {
		t.Fatalf("StartPTY: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// After Wait, Process.Signal returns os.ErrProcessDone rather than ESRCH.
func TestAliveIsFalseForAKilledAndReapedProcess(t *testing.T) {
	session := startSleepingSession(t)
	pid := session.Pid()

	if !session.Alive() {
		t.Fatalf("a running process must read alive")
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := syscall.Kill(pid, syscall.Signal(0)); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("expected pid %d to be gone after Close, kill(pid,0) = %v", pid, err)
	}

	if session.Alive() {
		probe := unixSessionProcess(t, session).Signal(syscall.Signal(0))
		t.Fatalf(
			"Alive() must be false for a killed-and-reaped process (pid %d); Process.Signal(0) = %v, errors.Is(err, syscall.ESRCH) = %v",
			pid, probe, errors.Is(probe, syscall.ESRCH),
		)
	}
}

func TestAliveIsTrueForAProcessThatIsNotYetReaped(t *testing.T) {
	session := startSleepingSession(t)
	pid := session.Pid()

	// Kill without waiting, leaving a zombie.
	if err := unixSessionProcess(t, session).Kill(); err != nil {
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

func TestCloseReapsWholeProcessGroup(t *testing.T) {
	session, err := StartPTY(StartParams{
		Executable: "/bin/sh",
		Args:       []string{"-c", `sleep 300 & echo "CHILD_PID=$!"; wait`},
		Cols:       80,
		Rows:       24,
	})
	if err != nil {
		t.Fatalf("StartPTY: %v", err)
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

// A zombie still accepts signal 0 where PID 1 never reaps, so read the process state.
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

func readChildPid(t *testing.T, session PTY) int {
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

func TestCloseIsIdempotentAfterTheProcessExits(t *testing.T) {
	session, err := StartPTY(StartParams{
		Executable: "/bin/sh",
		Args:       []string{"-c", "exit 0"},
		Cols:       80,
		Rows:       24,
	})
	if err != nil {
		t.Fatalf("StartPTY: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("closing an already-closed session must succeed, got: %v", err)
	}
}
