//go:build windows

package attach

import (
	"os/exec"
)

// killAttachProcessGroup is a stub; emcp only ships in the Linux runtime image.
func killAttachProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
