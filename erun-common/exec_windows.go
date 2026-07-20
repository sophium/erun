//go:build windows

package eruncommon

import (
	"os/exec"
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
