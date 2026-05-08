//go:build !windows

package cmd

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	eruncommon "github.com/sophium/erun/erun-common"
)

func detachBackgroundProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func isPortForwardProcess(pid int) bool {
	output, err := eruncommon.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return false
	}
	command := string(output)
	return strings.Contains(command, "kubectl") && strings.Contains(command, "port-forward")
}

func isSSHDActivityProxyProcess(pid int) bool {
	output, err := eruncommon.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return false
	}
	command := string(output)
	return strings.Contains(command, "activity") && strings.Contains(command, "ssh-proxy")
}
