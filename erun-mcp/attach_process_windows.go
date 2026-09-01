//go:build windows

package erunmcp

import "os/exec"

// killAttachProcessGroup has no process-group equivalent wired here: `emcp`
// only ever ships inside the Linux runtime container
// (erun-devops/docker/erun-devops/Dockerfile), so this path never runs in
// production. It exists only so this module keeps building and testing on a
// Windows development machine.
func killAttachProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
