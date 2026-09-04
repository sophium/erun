package eruncommon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

type Context struct {
	Logger    Logger
	Verbosity int
	DryRun    bool
	// Output selects how a command renders its machine-readable result. JSON mode
	// puts the structured result on Stdout while logging stays on Stderr, so an
	// orchestrator captures an uncorrupted payload.
	Output OutputMode
	// BuildJobs caps how many images build at once. Concurrency is execution
	// policy, so it rides the context rather than the docker command target,
	// which flows into the env-agnostic resolvers where policy must not leak.
	// Zero means "resolve it" (see resolveBuildJobs); one is strictly sequential.
	BuildJobs                  int
	Stdin                      io.Reader
	Stdout                     io.Writer
	Stderr                     io.Writer
	KubernetesContextPreflight KubernetesContextPreflightFunc
	// RegistryForwards owns any kubectl port-forwards a cluster registry needs.
	// It is set once at command entry so the forward's lifetime spans registry
	// resolution and the build/deploy that uses it; the entry defers its Close.
	// Nil when no command-level forward lifecycle has been established (tests,
	// pure resolution) — concretization then forwards on demand into a throwaway.
	RegistryForwards *ClusterRegistryForwards
	// timing is the active step-timing root for a long command (build, release,
	// push, deploy), set by that command's umbrella and nil everywhere else —
	// see timing.go. Unexported: only erun-common's own umbrellas start one.
	timing *stepTiming
	// MCPTool names the MCP tool that initiated this call, set only by
	// erun-mcp's tool handlers before they call into shared execution.
	// newPlatformClientForAlias forwards it to erun-backend-api as an audit
	// caller hint (see PlatformClient.WithMCPTool), so a platform-backed call
	// an MCP tool triggers is audited as type MCP with this tool name instead
	// of the generic API classification every other bearer-token caller gets.
	// Empty means "not an MCP call" (CLI, tests, pure resolution) and changes
	// nothing about the request. erun-cli does not populate this field yet.
	MCPTool string
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

// stderrOnlyContext is for internal status checks that have no caller-supplied
// Context to thread through (e.g. a background token-verify probe) but still
// call into code that traces via Context.Trace. A zero-value Context leaves
// Logger's stdout writer unset, which falls back to the real os.Stdout -- so a
// diagnostic line lands ahead of a command's own stdout result instead of
// beside its other diagnostics on stderr. This mirrors how a real transport
// context is wired (Logger's stdout pointed at stderr) without requiring a
// caller-supplied Context to exist yet.
func stderrOnlyContext() Context {
	return Context{Logger: NewLoggerWithWriters(VerbosityInfo, os.Stderr, os.Stderr)}
}

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
		case strings.ContainsRune(`/._:=+-\`, r):
		default:
			return false
		}
	}
	return true
}
