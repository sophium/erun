package eruncommon

import (
	"bytes"
	"fmt"
	"io"
	"strings"
)

type Context struct {
	Logger                     Logger
	Verbosity                  int
	DryRun                     bool
	Stdin                      io.Reader
	Stdout                     io.Writer
	Stderr                     io.Writer
	KubernetesContextPreflight KubernetesContextPreflightFunc
}

type KubernetesContextPreflightFunc func(Context, string) error

// Trace is the audit-log channel for decisions made, inputs loaded, and
// outputs produced. These lines stay visible at default Info verbosity
// so a user can audit the plan a command would execute without passing
// any flags. Raw shell argv is logged separately via TraceCommand and is
// gated to the higher Trace verbosity (-vv) since at that level the user
// is asking to see the literal commands.
func (c Context) Trace(message string) {
	c.Logger.Info(message)
}

func (c Context) Info(message string) {
	c.Logger.Info(message)
}

func (c Context) TraceCommand(dir, name string, args ...string) {
	c.Logger.Trace(formatShellCommand(dir, name, args...))
}

// ToolCapture wires stdout/stderr for an external tool subprocess so the
// caller can replay captured output on error and so that, at Info verbosity,
// successful tool output stays out of the user's terminal.
//
// At VerbosityInfo the returned writers discard live output (only the buffer
// holds it) — a clean run is silent and the captured bytes feed back into the
// error via Apply on failure.
//
// At VerbosityDebug or higher the writers tee to ctx.Stdout/ctx.Stderr, so the
// user sees live tool output while the buffer still captures for error replay.
func (c Context) ToolCapture() *ToolCapture {
	capture := &ToolCapture{verbosity: c.Verbosity}
	if c.Verbosity >= VerbosityDebug {
		capture.stdout = teeWriter(c.Stdout, &capture.buf)
		capture.stderr = teeWriter(c.Stderr, &capture.buf)
		return capture
	}
	capture.stdout = &capture.buf
	capture.stderr = &capture.buf
	return capture
}

type ToolCapture struct {
	buf       bytes.Buffer
	stdout    io.Writer
	stderr    io.Writer
	verbosity int
}

func (c *ToolCapture) Stdout() io.Writer { return c.stdout }

func (c *ToolCapture) Stderr() io.Writer { return c.stderr }

func (c *ToolCapture) Output() string { return c.buf.String() }

// Apply returns err unchanged when nil. On error, if the tool output was
// suppressed (Info verbosity) and the buffer is non-empty, it folds the
// captured bytes into the error so failures stay debuggable. At Debug or
// higher the live stream already showed the user; the error is returned
// without duplication.
func (c *ToolCapture) Apply(err error) error {
	if err == nil {
		return nil
	}
	if c.verbosity >= VerbosityDebug {
		return err
	}
	output := strings.TrimSpace(c.buf.String())
	if output == "" {
		return err
	}
	return fmt.Errorf("%w\n%s", err, output)
}

func teeWriter(primary io.Writer, capture io.Writer) io.Writer {
	switch {
	case primary == nil && capture == nil:
		return io.Discard
	case primary == nil:
		return capture
	case capture == nil:
		return primary
	default:
		return io.MultiWriter(primary, capture)
	}
}

func (c Context) EnsureKubernetesContext(contextName string) error {
	contextName = strings.TrimSpace(contextName)
	if contextName == "" || c.KubernetesContextPreflight == nil {
		return nil
	}
	return c.KubernetesContextPreflight(c, contextName)
}

// RequireKubernetesContext is the variant of EnsureKubernetesContext for
// callers whose next action would target a real cluster — namespace
// create/delete, helm upgrade, anything that mutates remote state. It
// errors when contextName is empty instead of silently letting the next
// kubectl/helm invocation fall through to `kubectl config
// current-context`, which on a developer machine is usually a local
// orbstack/minikube cluster rather than the env's intended target.
//
// EnsureKubernetesContext stays advisory for callers (e.g. init's
// per-step namespace seeding) that legitimately have no context yet
// and treat it as a "preflight if you can" hint.
func (c Context) RequireKubernetesContext(contextName string) error {
	contextName = strings.TrimSpace(contextName)
	if contextName == "" {
		return fmt.Errorf("kubernetes context is required")
	}
	if c.KubernetesContextPreflight == nil {
		return nil
	}
	return c.KubernetesContextPreflight(c, contextName)
}

// TraceBlock logs a labeled multi-line block (file content being written,
// remote script body about to run, etc.). It is the "stdin" companion to
// TraceCommand's argv: shown when the user is auditing (`--dry-run`) or
// asked for max verbosity (`-vv`), and silent otherwise so real runs do
// not spam full script bodies. Single-line Trace/TraceCommand contracts
// are unchanged.
func (c Context) TraceBlock(label, body string) {
	if !c.DryRun && c.Verbosity < VerbosityTrace {
		return
	}
	label = strings.TrimSpace(label)
	body = strings.TrimRight(body, "\n")
	if label == "" || body == "" {
		return
	}

	c.Logger.Info(label + ":")
	for _, line := range strings.Split(body, "\n") {
		c.Logger.Info("  " + line)
	}
}

func formatShellCommand(dir, name string, args ...string) string {
	parts := make([]string, 0, len(args)+1)
	if strings.TrimSpace(name) != "" {
		parts = append(parts, traceShellQuote(name))
	}
	for _, arg := range args {
		parts = append(parts, traceShellQuote(arg))
	}

	command := strings.Join(parts, " ")
	if strings.TrimSpace(dir) == "" {
		return command
	}
	return fmt.Sprintf("cd %s && %s", traceShellQuote(dir), command)
}

func traceShellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if isShellSafe(value) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func isShellSafe(value string) bool {
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("/._:=+-", r):
		default:
			return false
		}
	}
	return true
}
