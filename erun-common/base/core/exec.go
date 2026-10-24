package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// commandWaitDelay bounds the post-exit pipe drain: a leaked write handle (common
// on Windows) would otherwise block Wait forever after the process died.
const commandWaitDelay = 10 * time.Second

// Command wraps exec.Command, honoring an ERUN_<NAME>_BIN override, and hides the
// console window on Windows.
func Command(name string, args ...string) *exec.Cmd {
	cmd := NewExecCommand(name, args...)
	HideConsoleWindow(cmd)
	// Interactive tab shells use newExecCommand directly and skip this bound.
	cmd.WaitDelay = commandWaitDelay
	return cmd
}

// BoundCommandWait applies commandWaitDelay to a cmd built elsewhere.
func BoundCommandWait(cmd *exec.Cmd) {
	if cmd != nil {
		cmd.WaitDelay = commandWaitDelay
	}
}

// DesktopAppName is the desktop binary name that `pkill -f` matches; nothing erun
// writes into a command line may contain it (see RemoteAppSessionSocketDir).
const DesktopAppName = "erun-app"

// DesktopAppCommand builds the desktop app launch; macOS bundles go through
// `open -n <bundle> --args`. Shared by `erun app` and the desktop's self-restart.
func DesktopAppCommand(goos, executable string, args []string) *exec.Cmd {
	if goos == "darwin" && filepath.Ext(executable) == ".app" {
		openArgs := []string{"-n", executable}
		if len(args) > 0 {
			openArgs = append(openArgs, "--args")
			openArgs = append(openArgs, args...)
		}
		return Command("open", openArgs...)
	}
	cmd := Command(executable, args...)
	if goos == "darwin" {
		cmd.Args[0] = "ERun"
	}
	return cmd
}

// CommandContext is Command bound to ctx.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := newExecCommandContext(ctx, name, args...)
	HideConsoleWindow(cmd)
	cmd.WaitDelay = commandWaitDelay
	return cmd
}

func newExecCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	if strings.ContainsAny(name, "/\\") {
		return exec.CommandContext(ctx, name, args...)
	}
	base := strings.TrimSuffix(name, ".exe")
	envName := "ERUN_" + strings.ToUpper(strings.ReplaceAll(base, "-", "_")) + "_BIN"
	if override := strings.TrimSpace(os.Getenv(envName)); override != "" {
		return exec.CommandContext(ctx, override, args...)
	}
	return exec.CommandContext(ctx, name, args...)
}

func NewExecCommand(name string, args ...string) *exec.Cmd {
	if strings.ContainsAny(name, "/\\") {
		return exec.Command(name, args...)
	}
	// Strip .exe so erun-app.exe resolves to the same ERUN_ERUN_APP_BIN override.
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
	traceArgs := RedactRawCommandArgs(spec.Args)
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

func RedactRawCommandArgs(args []string) []string {
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
