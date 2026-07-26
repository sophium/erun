//go:build windows

package eruncommon

import (
	"os/exec"
	"strconv"
	"syscall"
)

// createNoWindow is the Win32 CREATE_NO_WINDOW process-creation flag. A console
// subsystem child (git, kubectl, the erun CLI, ...) spawned from the desktop app
// -- which is linked -H windowsgui and therefore owns no console -- otherwise
// gets a brand new console window allocated for it, which flashes up and
// vanishes. This flag runs the child without that console window.
const createNoWindow = 0x08000000

// HideConsoleWindow suppresses the transient console window Windows would
// otherwise allocate for a console child of the windowless GUI app. It is a
// no-op on other platforms (see exec_other.go). Inherited stdio handles still
// work, so redirected stdout/stderr are unaffected.
func HideConsoleWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}

// terminateProcessTree force-kills cmd's process and every descendant. On
// Windows cmd.Process.Kill() calls TerminateProcess on the direct child only,
// which orphans any grandchildren — helm's dind/kubectl children in production,
// or a stubbed shell's `sleep` in tests. An orphan keeps its inherited working
// directory and stdio pipes open, wedging the deploy's chart-dir cleanup (and, in
// the integration harness, the TempDir RemoveAll that then fails with "being used
// by another process"). taskkill /T walks the tree from the PID and kills every
// descendant; /F forces termination. Best-effort: a race against a process that
// already exited just yields a harmless non-zero taskkill exit.
func terminateProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	kill := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
	HideConsoleWindow(kill)
	_ = kill.Run()
}
