//go:build !windows

package cmd

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	eruncommon "github.com/sophium/erun/erun-common"
)

// detachBackgroundProcess puts a port-forward in its own session, mirroring
// detachEnvironmentJobSupervisor (erun-common/job_process_unix.go): a session
// leader has no controlling terminal to be hung up with and is not in the
// caller's process group, which is what lets it survive `erun open` exiting
// (a doomed non-interactive shell, or a normal --no-shell return) without the
// caller wrapping anything in setsid itself. Setpgid alone still shares that
// session and does not give the same guarantee.
func detachBackgroundProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
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

// kubectlPortForwardProcessIDs enumerates every live kubectl port-forward
// process on the host, for the argv-identity sweep that catches a forward
// whose state-file entry was overwritten by a losing race between two
// overlapping opens (see sweepDeadPortForwardsMatching). A single ps call,
// filtered here rather than by a shell pattern, so nothing but an exact PID
// ever reaches a kill call.
func kubectlPortForwardProcessIDs() []int {
	output, err := eruncommon.Command("ps", "-e", "-ww", "-o", "pid=", "-o", "command=").Output()
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
