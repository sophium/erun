package eruncommon

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// commandWaitDelay bounds how long Wait()/Output()/Run() will wait for a
// finished process's stdout/stderr to reach EOF before force-closing the pipes
// and returning. Without it, a leaked pipe write-end — an endpoint-security
// agent or a grandchild that inherited the handle, common on Windows — keeps
// Wait blocked forever even though the process is already dead, hanging deploys,
// reconnects, and the pod-watch drain. It only ever triggers after the process
// exits, so a legitimately long-running command is unaffected.
const commandWaitDelay = 10 * time.Second

// Command wraps exec.Command, honoring an ERUN_<NAME>_BIN override so tests
// can redirect a named binary to a stub without a live toolchain or account.
// On Windows it also suppresses the stray console window that a console child
// of the windowless desktop app would otherwise flash (see HideConsoleWindow).
func Command(name string, args ...string) *exec.Cmd {
	cmd := newExecCommand(name, args...)
	HideConsoleWindow(cmd)
	// Bound the post-exit I/O drain so a leaked pipe handle can't wedge Wait
	// forever (see commandWaitDelay). Interactive tab shells build their command
	// via newExecCommand directly and deliberately skip this.
	cmd.WaitDelay = commandWaitDelay
	return cmd
}

// BoundCommandWait applies the same post-exit I/O-drain bound as Command to a
// cmd built elsewhere (e.g. the desktop's exec.CommandContext calls that stream
// a child's stdout/stderr). Call it right after constructing such a cmd so a
// leaked pipe handle can't wedge Wait()/CombinedOutput() forever on Windows.
func BoundCommandWait(cmd *exec.Cmd) {
	if cmd != nil {
		cmd.WaitDelay = commandWaitDelay
	}
}

// DesktopAppName is the installed file name of the desktop binary, and so the
// string a `pkill -f` aimed at that binary carries. Nothing erun writes into a
// process's command line may contain it — see RemoteAppSessionSocketDir.
const DesktopAppName = "erun-app"

// DesktopAppCommand builds the command that launches the ERun desktop app
// (erun-app). On macOS an `.app` bundle must launch via `open -n <bundle>` (a
// fresh instance; forwarded args go after `--args`); elsewhere the binary is
// spawned directly, with argv[0] set to "ERun" on macOS for a stable process
// name. It lives here, transport-neutral, so both `erun app` (CLI) and the
// desktop app's own self-restart build the launch identically without the
// desktop importing erun-cli.
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
