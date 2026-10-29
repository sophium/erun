//go:build windows

package core

import (
	"os/exec"
	"strconv"
	"syscall"
)

// createNoWindow is Win32 CREATE_NO_WINDOW; without it a console child of the
// windowsgui desktop flashes a new console window.
const createNoWindow = 0x08000000

// HideConsoleWindow suppresses that window; inherited stdio handles still work.
func HideConsoleWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}

// TerminateProcessTree kills cmd's process tree with taskkill /T /F: Kill alone
// orphans grandchildren that keep the working directory and pipes open.
func TerminateProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	kill := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
	HideConsoleWindow(kill)
	_ = kill.Run()
}
