package eruncommon

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Command wraps exec.Command, honoring an ERUN_<NAME>_BIN override so tests
// can redirect a named binary to a stub without a live toolchain or account.
// On Windows it also suppresses the stray console window that a console child
// of the windowless desktop app would otherwise flash (see HideConsoleWindow).
func Command(name string, args ...string) *exec.Cmd {
	cmd := newExecCommand(name, args...)
	HideConsoleWindow(cmd)
	return cmd
}

func newExecCommand(name string, args ...string) *exec.Cmd {
	if strings.ContainsAny(name, "/\\") {
		return exec.Command(name, args...)
	}
	// Derive the override var from the base tool name so a Windows .exe suffix
	// (erun-app.exe) resolves to the same ERUN_ERUN_APP_BIN seam as erun-app.
	base := strings.TrimSuffix(name, ".exe")
	envName := "ERUN_" + strings.ToUpper(strings.ReplaceAll(base, "-", "_")) + "_BIN"
	if override := strings.TrimSpace(os.Getenv(envName)); override != "" {
		return exec.Command(override, args...)
	}
	return exec.Command(name, args...)
}

type RawCommandRunnerFunc func(dir, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error

type RawCommandSpec struct {
	Dir  string   `json:"dir,omitempty"`
	Args []string `json:"args,omitempty"`
}

func RunRawCommand(ctx Context, spec RawCommandSpec, run RawCommandRunnerFunc) error {
	if len(spec.Args) == 0 || strings.TrimSpace(spec.Args[0]) == "" {
		return fmt.Errorf("raw command is required")
	}
	if run == nil {
		run = RawCommandRunner
	}

	name := spec.Args[0]
	args := append([]string(nil), spec.Args[1:]...)
	traceArgs := redactRawCommandArgs(spec.Args)
	ctx.TraceCommand(spec.Dir, traceArgs[0], traceArgs[1:]...)
	if ctx.DryRun {
		return nil
	}
	return run(spec.Dir, name, args, ctx.Stdin, ctx.Stdout, ctx.Stderr)
}

func RawCommandRunner(dir, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := Command(name, args...)
	cmd.Dir = dir
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func redactRawCommandArgs(args []string) []string {
	redacted := make([]string, 0, len(args))
	redactNext := false
	for _, arg := range args {
		if redactNext {
			redacted = append(redacted, "<redacted>")
			redactNext = false
			continue
		}
		if name, _, ok := strings.Cut(arg, "="); ok && isRawCommandSensitiveName(name) {
			redacted = append(redacted, name+"=<redacted>")
			continue
		}
		redacted = append(redacted, arg)
		if isRawCommandSensitiveName(arg) {
			redactNext = true
		}
	}
	return redacted
}

func isRawCommandSensitiveName(value string) bool {
	normalized := strings.ToLower(strings.TrimLeft(value, "-"))
	for _, token := range []string{"password", "passwd", "secret", "token", "apikey", "api-key", "access-key", "private-key"} {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}
