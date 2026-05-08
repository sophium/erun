package eruncommon

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Command returns *exec.Cmd for the named external binary, honoring an
// optional ERUN_<NAME>_BIN environment override. Tests use the override to
// route real subprocess execution to a stub script so non-dry-run code paths
// can be exercised without depending on the developer's local toolchain or a
// live cloud account. Names containing hyphens are normalized to underscores
// for the env var lookup (e.g., golangci-lint -> ERUN_GOLANGCI_LINT_BIN).
//
// Absolute paths and names that already point at a specific binary (anything
// containing a path separator) pass through untouched so existing call sites
// that compute their own paths still work.
func Command(name string, args ...string) *exec.Cmd {
	if strings.ContainsAny(name, "/\\") {
		return exec.Command(name, args...)
	}
	envName := "ERUN_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_BIN"
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
