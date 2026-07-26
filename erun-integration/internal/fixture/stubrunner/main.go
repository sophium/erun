// Command erun-stub-runner faithfully forwards argv to a sibling POSIX-shell
// stub script on Windows.
//
// A .bat launcher can't do this: cmd.exe's %* forwards a command-line string
// that sh then re-parses with POSIX rules, mangling complex args (notably the
// multi-line `sh -lc '<script>'` erun passes to kubectl). Go's exec, by
// contrast, hands this .exe its argv faithfully via CommandLineToArgvW. This
// program then passes that argv to sh through a NUL-delimited file
// (ERUN_STUB_ARGV_FILE) — never a command line — so the stub script sees each
// argument exactly, byte for byte.
//
// The stub script beside this executable is its own path minus the .exe suffix;
// a writeStub-injected preamble rebuilds "$@" from the argv file.
package main

import (
	"os"
	"os/exec"
	"strings"
)

func main() {
	script := strings.TrimSuffix(os.Args[0], ".exe")

	argvFile, err := os.CreateTemp("", "erun-stub-argv-*")
	if err != nil {
		os.Exit(97)
	}
	defer func() { _ = os.Remove(argvFile.Name()) }()
	for _, arg := range os.Args[1:] {
		if _, err := argvFile.WriteString(arg); err != nil {
			os.Exit(97)
		}
		if _, err := argvFile.Write([]byte{0}); err != nil {
			os.Exit(97)
		}
	}
	if err := argvFile.Close(); err != nil {
		os.Exit(97)
	}

	cmd := exec.Command("sh", script)
	cmd.Env = append(os.Environ(), "ERUN_STUB_ARGV_FILE="+argvFile.Name())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		os.Exit(98)
	}
}
