//go:build windows

package cmd

import "os/exec"

func detachBackgroundProcess(*exec.Cmd) {}

func isPortForwardProcess(int) bool {
	return false
}
