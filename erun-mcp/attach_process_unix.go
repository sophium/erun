//go:build !windows

package erunmcp

import (
	"os/exec"
	"syscall"
)

// killAttachProcessGroup signals the whole process group the attach
// subprocess leads (creack/pty makes it a session/group leader), not just the
// immediate `sh` child. The script execs `dtach -A` as a child of `sh` rather
// than replacing it (it needs to inspect dtach's exit code afterward), so
// signalling `sh` alone would orphan the dtach client and leave the PTY slave
// held open -- the same failure shape erun-ui's terminal session Close() was
// written to avoid (erun-ui/session_unix.go).
func killAttachProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
