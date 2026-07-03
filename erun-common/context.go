package eruncommon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type Context struct {
	Logger    Logger
	Verbosity int
	DryRun    bool
	// Output selects how a command renders its machine-readable result. JSON mode
	// puts the structured result on Stdout while logging stays on Stderr, so an
	// orchestrator captures an uncorrupted payload.
	Output                     OutputMode
	Stdin                      io.Reader
	Stdout                     io.Writer
	Stderr                     io.Writer
	KubernetesContextPreflight KubernetesContextPreflightFunc
}

// WriteResult emits v as the command's structured result; callers invoke it on
// success, after the human trace has streamed to Stderr.
func (c Context) WriteResult(v any) error {
	if c.Output != OutputJSON {
		return nil
	}
	out := c.Stdout
	if out == nil {
		return nil
	}
	encoded, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(encoded))
	return err
}

type KubernetesContextPreflightFunc func(Context, string) error

// Trace is the audit channel for decisions, inputs, and outputs; it stays
// visible at default verbosity so a user can audit a command's plan without
// flags. Raw argv goes through TraceCommand, gated to -vv.
func (c Context) Trace(message string) {
	c.Logger.Info(message)
}

func (c Context) Info(message string) {
	c.Logger.Info(message)
}

func (c Context) TraceCommand(dir, name string, args ...string) {
	c.Logger.Trace(formatShellCommand(dir, name, args...))
}

// ToolCapture wires stdout/stderr for an external tool subprocess so a clean run
// stays silent while captured output can replay on error; at Debug verbosity or
// higher the output also streams live to the user's terminal.
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

// Apply folds captured tool output into a returned error so a silenced run's
// failure stays debuggable, and avoids duplicating output Debug already streamed
// live.
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

// RequireKubernetesContext is the mutating-action variant of
// EnsureKubernetesContext: it errors on an empty context instead of letting
// kubectl/helm fall through to the dev machine's current-context (usually a
// local orbstack/minikube cluster, not the env's target). EnsureKubernetesContext
// stays advisory for callers that legitimately have no context yet.
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

// TraceBlock logs a labeled multi-line block (a file body, a remote script) for
// audit; it shows on --dry-run or -vv and stays silent otherwise so real runs do
// not spam full script bodies.
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
