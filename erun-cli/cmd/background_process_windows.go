//go:build windows

package cmd

import "os/exec"

func detachBackgroundProcess(*exec.Cmd) {}

func isMCPPortForwardProcess(int) bool {
	return false
}
