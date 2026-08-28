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
