package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
)

// RegistryForwarder owns the port-forwards to an in-cluster registry.
type RegistryForwarder interface {
	Start(ctx Context, kubeContext, namespace, service string, remotePort int) (int, error)
	Close()
}

type Context struct {
	Logger    Logger
	Verbosity int
	DryRun    bool
	// Output selects the result format; JSON goes to Stdout, logging stays on Stderr.
	Output model.OutputMode
	// BuildJobs caps concurrent image builds; zero means resolve it, one is sequential.
	// It lives here because policy must not leak into the env-agnostic resolvers.
	BuildJobs int
	// PlatformAlias is the operator's explicit erun cloud alias choice; empty means none.
	PlatformAlias string
	Stdin         io.Reader
	Stdout        io.Writer
	Stderr        io.Writer
	// Command is the operator-facing invocation (e.g. "erun usage") that recovery
	// messages name; empty outside the CLI.
	Command string
	// CommandScopesTenantByFlag reports whether Command's --tenant scopes resolution,
	// so a recovery never suggests a flag that does something else.
	CommandScopesTenantByFlag  bool
	KubernetesContextPreflight KubernetesContextPreflightFunc
	// RegistryForwards spans registry resolution and the build/deploy using it. Nil
	// means forwards are opened on demand.
	RegistryForwards RegistryForwarder
	// Timing is the step-timing root of a long command; nil elsewhere.
	Timing *stepTiming
	// Progress is the active build run's heartbeat; nil elsewhere.
	Progress *BuildHeartbeat
	// MCPTool names the initiating MCP tool so platform calls are audited as MCP.
	// Empty means not an MCP call.
	MCPTool string
}

// WriteResult emits v as the command's structured result.
func (c Context) WriteResult(v any) error {
	if c.Output != model.OutputJSON {
		return nil
	}
	out := c.Stdout
	if out == nil {
		return nil
	}
	encoded, err := json.MarshalIndent(normalizeResultSlices(v), "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(encoded))
	return err
}

// normalizeResultSlices turns a top-level nil slice into [], because null would
// mean "not determined". Nested fields keep nil: there absence is meaningful.
func normalizeResultSlices(v any) any {
	if v == nil {
		return v
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice || !rv.IsNil() {
		return v
	}
	return reflect.MakeSlice(rv.Type(), 0, 0).Interface()
}

type KubernetesContextPreflightFunc func(Context, string) error

// StderrOnlyContext is a Context with stdout pointed at stderr, for internal
// checks that trace without a caller Context.
func StderrOnlyContext() Context {
	return Context{Logger: NewLoggerWithWriters(VerbosityInfo, os.Stderr, os.Stderr)}
}

// Trace logs decisions at default verbosity; raw argv goes through TraceCommand (-vv).
func (c Context) Trace(message string) {
	c.Logger.Info(message)
}

func (c Context) Info(message string) {
	c.Logger.Info(message)
}

func (c Context) TraceCommand(dir, name string, args ...string) {
	c.Logger.Trace(FormatShellCommand(dir, name, args...))
}

// ToolCapture silences a tool's output but replays it on error, streaming live at
// Debug. Separate buffers per stream: os/exec writes them from separate goroutines.
func (c Context) ToolCapture() *ToolCapture {
	capture := &ToolCapture{verbosity: c.Verbosity}
	if c.Verbosity >= VerbosityDebug {
		capture.stdout = TeeWriter(c.Stdout, &capture.stdoutBuf)
		capture.stderr = TeeWriter(c.Stderr, &capture.stderrBuf)
		return capture
	}
	capture.stdout = &capture.stdoutBuf
	capture.stderr = &capture.stderrBuf
	return capture
}

type ToolCapture struct {
	stdoutBuf bytes.Buffer
	stderrBuf bytes.Buffer
	stdout    io.Writer
	stderr    io.Writer
	verbosity int
}

func (c *ToolCapture) Stdout() io.Writer { return c.stdout }

func (c *ToolCapture) Stderr() io.Writer { return c.stderr }

func (c *ToolCapture) Output() string { return c.stdoutBuf.String() + c.stderrBuf.String() }

// Apply folds captured output into err unless Debug already streamed it.
func (c *ToolCapture) Apply(err error) error {
	if err == nil {
		return nil
	}
	if c.verbosity >= VerbosityDebug {
		return err
	}
	output := strings.TrimSpace(c.Output())
	if output == "" {
		return err
	}
	return fmt.Errorf("%w\n%s", err, output)
}

func TeeWriter(primary io.Writer, capture io.Writer) io.Writer {
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

// CommandOutputCapture keeps stdout and stderr in separate buffers because os/exec
// copies each stream on its own goroutine. Read combined() only after the command exits.
type CommandOutputCapture struct {
	Stdout bytes.Buffer
	Stderr bytes.Buffer
}

func (c *CommandOutputCapture) Combined() string {
	return c.Stdout.String() + c.Stderr.String()
}

func (c Context) EnsureKubernetesContext(contextName string) error {
	contextName = strings.TrimSpace(contextName)
	if contextName == "" || c.KubernetesContextPreflight == nil {
		return nil
	}
	return c.KubernetesContextPreflight(c, contextName)
}

// RequireKubernetesContext errors on an empty context so mutating actions never
// fall through to the machine's current-context.
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

// TraceBlock logs a multi-line block only on --dry-run or -vv.
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

func FormatShellCommand(dir, name string, args ...string) string {
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
