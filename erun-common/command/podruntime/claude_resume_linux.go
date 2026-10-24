//go:build linux

package podruntime

import (
	"os"
	"os/exec"
	"syscall"
)

// defaultClaudeHost hands the process over to Claude Code with execve, so the
// real binary replaces erun and is what idle stop finds in the process table.
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

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
