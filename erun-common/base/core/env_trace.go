package core

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// envTraceLogMaxBytes caps each trace file so the rotated pair stays under ~2× this.
const envTraceLogMaxBytes = 5 * 1024 * 1024

// EnvTraceLogPath resolves the per-env trace log every env-scoped invocation appends to.
func EnvTraceLogPath(tenant, environment string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := ErrMissingTenantOrEnvironment("resolve env trace log path", tenant, environment); err != nil {
		return "", err
	}
	return filepath.Join(home, ".erun", tenant, environment, "trace.log"), nil
}

// ActivateEnvTrace tees this invocation's trace into the per-env log. Setup
// errors degrade to the un-tee'd context; dry-run writes nothing but still traces the path.
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

func openEnvTraceLog(path string) (*os.File, error) {
	return OpenBoundedAppendLog(path, envTraceLogMaxBytes, 0o600)
}

// stampedLineWriter stamps each line so concurrent invocations sharing the log stay attributable.
type stampedLineWriter struct {
	out io.Writer
	now func() time.Time
}

func (w *stampedLineWriter) Write(p []byte) (int, error) {
	stamp := w.now().UTC().Format(time.RFC3339)
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if !writeStampedLine(w.out, stamp, line) {
			break // diagnostics only: never fail the command
		}
	}
	return len(p), nil
}

func writeStampedLine(out io.Writer, stamp, line string) bool {
	_, err := fmt.Fprintf(out, "%s %s\n", stamp, line)
	return err == nil
}
