//go:build !windows

package shared

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/sophium/erun/erun-common/base/core"
)

// DetachBackgroundProcess starts a port-forward in its own session so it survives
// `erun open` exiting; Setpgid alone would not.
func DetachBackgroundProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func isPortForwardProcess(pid int) bool {
	output, err := core.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return false
	}
	command := string(output)
	return strings.Contains(command, "kubectl") && strings.Contains(command, "port-forward")
}

func isSSHDActivityProxyProcess(pid int) bool {
	output, err := core.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return false
	}
	command := string(output)
	return strings.Contains(command, "activity") && strings.Contains(command, "ssh-proxy")
}

// kubectlPortForwardProcessIDs lists live kubectl port-forward PIDs from one ps call,
// so only exact PIDs ever reach kill.
func kubectlPortForwardProcessIDs() []int {
	output, err := core.Command("ps", "-e", "-ww", "-o", "pid=", "-o", "command=").Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "kubectl") || !strings.Contains(line, "port-forward") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}
