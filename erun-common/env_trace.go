package eruncommon

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// envTraceLogMaxBytes bounds the per-env trace log: when the current file
// exceeds it, the file rotates to trace.log.1 (replacing any prior one) so
// the pair never grows past ~2× the cap.
const envTraceLogMaxBytes = 5 * 1024 * 1024

// EnvTraceLogPath is the per-env trace log (issues #466/#508): the full
// VerbosityTrace stream of every env-scoped erun invocation, appended by
// the runtime itself so the desktop's Diagnostics console can show what
// happened at any time — including for commands that ran before the console
// was opened. Host-side invocations write the host file; in-pod invocations
// write the pod's (both resolve $HOME).
func EnvTraceLogPath(tenant, environment string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return "", fmt.Errorf("tenant and environment are required")
	}
	return filepath.Join(home, ".erun", tenant, environment, "trace.log"), nil
}

// ActivateEnvTrace turns on the per-env trace tee for this invocation.
// Capture is always on (issue #508): diagnostics must exist for the first
// failure, not only for failures after an operator opted in. Returns the
// (possibly) tee'd context and a closer the caller defers.
//
// The tee is diagnostics, never a failure source: an unopenable file or a
// failed rotation degrades to the un-tee'd context with a trace line. In
// dry-run mode nothing is written; the trace names the resolved log path so
// the plan stays auditable (the integration-golden contract).
func ActivateEnvTrace(ctx Context, tenant, environment string) (Context, func()) {
	noop := func() {}
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return ctx, noop
	}
	path, err := EnvTraceLogPath(tenant, environment)
	if err != nil {
		ctx.Trace("trace-log: cannot resolve trace log path: " + err.Error())
		return ctx, noop
	}
	if ctx.DryRun {
		ctx.Trace("trace-log: would append the full trace to " + path)
		return ctx, noop
	}
	file, err := openEnvTraceLog(path)
	if err != nil {
		ctx.Trace("trace-log: cannot open " + path + ": " + err.Error())
		return ctx, noop
	}
	ctx.Logger = ctx.Logger.WithTraceSink(&stampedLineWriter{out: file, now: time.Now})
	ctx.Trace("trace-log: appending the full trace to " + path)
	return ctx, func() { _ = file.Close() }
}

// openEnvTraceLog opens the append-mode log, rotating the current file to
// trace.log.1 once it exceeds the cap.
func openEnvTraceLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if info, err := os.Stat(path); err == nil && info.Size() > envTraceLogMaxBytes {
		if err := os.Rename(path, path+".1"); err != nil {
			return nil, err
		}
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
}

// stampedLineWriter prefixes each written line with an RFC3339 timestamp so
// interleaved invocations stay attributable in the shared per-env log.
type stampedLineWriter struct {
	out io.Writer
	now func() time.Time
}

func (w *stampedLineWriter) Write(p []byte) (int, error) {
	stamp := w.now().UTC().Format(time.RFC3339)
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if _, err := fmt.Fprintf(w.out, "%s %s\n", stamp, line); err != nil {
			return len(p), nil // diagnostics only: never fail the command
		}
	}
	return len(p), nil
}
