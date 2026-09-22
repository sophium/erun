package eruncommon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
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
	BuildJobs int
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	// Command is the operator-facing invocation of the command that is running
	// (for example "erun usage", "erun outputs list"), set once at command
	// entry. Resolution failures name the command's own recovery from it, so a
	// failure names the operation the operator actually ran rather than a fixed
	// one. Empty for callers that are not a CLI command (the desktop app, MCP),
	// which get the command-free wording instead of a borrowed name.
	Command string
	// CommandScopesTenantByFlag reports whether Command takes a --tenant flag
	// that scopes resolution (rather than, say, `list`'s --tenant, which selects
	// version-drift reporting). It gates whether the recovery may name that flag
	// as the fix: offering a flag that does not do what the operator wants is the
	// same defect as naming the wrong command, so a command with no such flag
	// (build and push take the tenant positionally) gets a recovery that names
	// the command without asserting a flag it does not have.
	CommandScopesTenantByFlag  bool
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
	// progress is the live heartbeat for the command's active build run, set by
	// RunDockerBuilds (or RunDockerBuild for a lone image) and nil everywhere
	// else. Every image the run builds registers with it, so one ticker names
	// whatever is still building however the run is scheduled — see
	// build_heartbeat.go.
	progress *buildHeartbeat
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
	encoded, err := json.MarshalIndent(normalizeResultSlices(v), "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(encoded))
	return err
}

// normalizeResultSlices replaces a top-level nil slice with an empty one, so a
// result that resolved to no rows marshals as [] rather than null. The two are
// not interchangeable for the orchestrators --output json exists for: null
// conflates "we queried and nothing matched" with "this was not determined",
// while [] can only mean the former. A consumer that iterates the result or
// reads .length otherwise has to null-guard every command whose cardinality it
// cannot predict from the surface.
//
// Only the top level is rewritten. A nil field inside a struct is left alone:
// there, absence is part of the declared shape (omitempty marks a value that is
// genuinely not part of this result), and filling it in would turn a reported
// absence into a claim -- "refreshFailures": [] asserts the refresh ran and
// nothing failed, which is false when it was never attempted.
//
// Maps are deliberately out of scope: the defect this normalises is a list
// shape, and widening the rewrite to every container kind is a larger change
// than the reported evidence supports.
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
//
// stdout and stderr each get their own buffer rather than sharing one: os/exec
// runs one copier goroutine per stream, and a single shared *bytes.Buffer
// reachable from both would be written by both goroutines with no
// synchronization (the same shape deploy.go's helmOutputCapture exists to
// avoid for helm deploy). Output()/Apply() only read the buffers after the
// command has finished, which is the only time every caller reads them.
func (c Context) ToolCapture() *ToolCapture {
	capture := &ToolCapture{verbosity: c.Verbosity}
	if c.Verbosity >= VerbosityDebug {
		capture.stdout = teeWriter(c.Stdout, &capture.stdoutBuf)
		capture.stderr = teeWriter(c.Stderr, &capture.stderrBuf)
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
	output := strings.TrimSpace(c.Output())
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

// commandOutputCapture keeps stdout and stderr in separate buffers rather than
// one shared *bytes.Buffer. os/exec runs one copier goroutine per stream, so a
// single buffer reachable from both cmd.Stdout and cmd.Stderr — built via two
// separate teeWriter/io.MultiWriter calls, which defeats os/exec's
// same-writer dedup (interfaceEqual) even when the two calls share the same
// underlying capture — is written by both goroutines with no synchronization.
// This is the same shape deploy.go's helmOutputCapture exists to avoid for
// helm deploy; build_docker_commands.go, helm_chart_publish.go, and
// published_artifact_verify.go all had the unguarded version of it.
// combined() must only be read after the command has finished, which is the
// only time any caller here reads it.
type commandOutputCapture struct {
	stdout bytes.Buffer
	stderr bytes.Buffer
}

func (c *commandOutputCapture) combined() string {
	return c.stdout.String() + c.stderr.String()
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
