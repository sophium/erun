//go:build !windows

package cmd

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

func detachBackgroundProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func isMCPPortForwardProcess(pid int) bool {
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return false
	}
	command := string(output)
	return strings.Contains(command, "kubectl") && strings.Contains(command, "port-forward")
}
