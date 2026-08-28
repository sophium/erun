//go:build windows

package cmd

import (
	"os/exec"
	"syscall"
)

// createNewProcessGroup detaches a child from the console control events the
// caller's group receives — the closest Windows equivalent to a new session
// (see erun-common/job_process_windows.go, which uses the same flag for the
// same reason). Without it, closing the console that started `erun open`
// takes its port-forwards down with it.
const createNewProcessGroup = 0x00000200

func detachBackgroundProcess(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroup
}

func isPortForwardProcess(int) bool {
	return false
}

func isSSHDActivityProxyProcess(int) bool {
	return false
}
