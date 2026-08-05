//go:build !windows

package main

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

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
