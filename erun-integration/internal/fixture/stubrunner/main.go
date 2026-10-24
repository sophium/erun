// Command erun-stub-runner forwards argv byte-exact to the sibling sh stub on
// Windows via a NUL-delimited file (ERUN_STUB_ARGV_FILE); a .bat would mangle it.
package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/sophium/erun/erun-integration/internal/harnessexec"
)

func main() {
	os.Exit(run())
}

// run returns the exit code so the deferred argv-file removal always runs.
func run() int {
	script := strings.TrimSuffix(os.Args[0], ".exe")

	argvFile, err := os.CreateTemp("", "erun-stub-argv-*")
	if err != nil {
		return 97
	}
	defer func() { _ = os.Remove(argvFile.Name()) }()
	for _, arg := range os.Args[1:] {
		if _, err := argvFile.WriteString(arg); err != nil {
			return 97
		}
		if _, err := argvFile.Write([]byte{0}); err != nil {
			return 97
		}
	}
	if err := argvFile.Close(); err != nil {
		return 97
	}

	// The scenario PATH has no shell; ERUN_STUB_SH supplies an absolute one.
	shell := os.Getenv("ERUN_STUB_SH")
	if strings.TrimSpace(shell) == "" {
		shell = "sh"
	}
	cmd := harnessexec.Command(shell, script)
	cmd.Env = append(os.Environ(), "ERUN_STUB_ARGV_FILE="+argvFile.Name())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		return 98
	}
	return 0
}
