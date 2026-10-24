//go:build linux

package podruntime

import (
	"os"
	"os/exec"
	"syscall"
)

// execve makes the real binary what idle stop finds in the process table.
func defaultClaudeHost() (claudeHost, error) {
	return claudeHost{
		getenv:     os.Getenv,
		getwd:      os.Getwd,
		isDir:      isDirectory,
		readFile:   os.ReadFile,
		lookPath:   exec.LookPath,
		executable: os.Executable,
		exec:       syscall.Exec,
		environ:    os.Environ,
	}, nil
}
