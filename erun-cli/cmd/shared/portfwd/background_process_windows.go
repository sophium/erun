//go:build windows

package portfwd

import (
	"os/exec"
	"syscall"
)

// createNewProcessGroup keeps port-forwards alive when the console that started `erun open` closes.
const createNewProcessGroup = 0x00000200

func DetachBackgroundProcess(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroup
}

func isPortForwardProcess(int) bool {
	return false
}

func IsSSHDActivityProxyProcess(int) bool {
	return false
}

// kubectlPortForwardProcessIDs is a no-op: the argv sweep is unix-only.
func kubectlPortForwardProcessIDs() []int {
	return nil
}
