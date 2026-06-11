package eruncommon

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// envTraceLogMaxBytes bounds the per-env debug-output log: when the current
// file exceeds it, the file rotates to trace.log.1 (replacing any prior one)
// so the pair never grows past ~2× the cap.
const envTraceLogMaxBytes = 5 * 1024 * 1024

// EnvTraceLogPath is the per-env debug-output log (issue #466): the full
// VerbosityTrace stream of every erun invocation for the env, appended by
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

// EnvDebugTeeStore is the minimal store the debug-output activation needs:
// read the env's persisted setting, and persist the flag-driven enable.
type EnvDebugTeeStore interface {
	LoadEnvConfig(string, string) (EnvConfig, string, error)
	SaveEnvConfig(string, EnvConfig) error
}

// ActivateEnvDebugTeeFromStore is ActivateEnvDebugTee for callers that hold
// a store rather than an already-loaded env config (the MCP tools).
func ActivateEnvDebugTeeFromStore(ctx Context, store EnvDebugTeeStore, tenant, environment string) (Context, func()) {
	noop := func() {}
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if store == nil || tenant == "" || environment == "" {
		return ctx, noop
	}
	config, _, err := store.LoadEnvConfig(tenant, environment)
	if err != nil {
		if ctx.DebugOutput {
			ctx.Trace("debug-output: cannot load env config for " + tenant + "/" + environment + ": " + err.Error())
		}
		return ctx, noop
	}
	return ActivateEnvDebugTee(ctx, config, store.SaveEnvConfig, tenant, environment)
}

// ActivateEnvDebugTee turns on the per-env debug-output tee for this
// invocation when the env opts in — via the persisted `debugoutput` setting
// or the --debug-output flag (ctx.DebugOutput), which also persists the
// setting (through save, when provided) so every later invocation for the
// env keeps capturing. Returns the (possibly) tee'd context and a closer
// the caller defers.
//
// The tee is diagnostics, never a failure source: an unopenable file or a
// failed rotation degrades to the un-tee'd context with a trace line. In
// dry-run mode nothing is written or persisted; the trace names the
// resolved log path so the plan stays auditable (the integration-golden
// contract).
func ActivateEnvDebugTee(ctx Context, config EnvConfig, save func(string, EnvConfig) error, tenant, environment string) (Context, func()) {
	noop := func() {}
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return ctx, noop
	}
	if !ctx.DebugOutput && !config.DebugOutput {
		return ctx, noop
	}
	path, err := EnvTraceLogPath(tenant, environment)
	if err != nil {
		ctx.Trace("debug-output: cannot resolve trace log path: " + err.Error())
		return ctx, noop
	}
	if ctx.DryRun {
		ctx.Trace("debug-output: would append the full trace to " + path)
		if ctx.DebugOutput && !config.DebugOutput {
			ctx.Trace("debug-output: would persist debugoutput=true for " + tenant + "/" + environment)
		}
		return ctx, noop
	}
	file, err := openEnvTraceLog(path)
	if err != nil {
		ctx.Trace("debug-output: cannot open " + path + ": " + err.Error())
		return ctx, noop
	}
	if ctx.DebugOutput && !config.DebugOutput {
		if save == nil {
			ctx.Trace("debug-output: capture is on for this run only (no config writer to persist debugoutput=true)")
		} else {
			config.DebugOutput = true
			if err := save(tenant, config); err != nil {
				ctx.Trace("debug-output: persisting debugoutput=true failed: " + err.Error())
			} else {
				ctx.Trace("debug-output: persisted debugoutput=true for " + tenant + "/" + environment)
			}
		}
	}
	ctx.Logger = ctx.Logger.WithTraceSink(&stampedLineWriter{out: file, now: time.Now})
	ctx.Trace("debug-output: appending the full trace to " + path)
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
