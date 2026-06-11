package eruncommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestActivateEnvDebugTee pins the per-env debug-output capture (issue
// #466): the tee mirrors the full trace stream — including lines the
// terminal verbosity suppresses — into ~/.erun/<tenant>/<env>/trace.log,
// stamped per line; the flag persists the env setting; dry-run only names
// the path. The real-write branches are unreachable from the dry-run
// integration harness by design, so this white-box test owns them; the
// dry-run trace contract is locked by the open goldens.
func TestActivateEnvDebugTee(t *testing.T) {
	t.Run("tee captures suppressed trace lines, stamped, and persists the flag", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		var saved *EnvConfig
		save := func(_ string, config EnvConfig) error {
			saved = &config
			return nil
		}
		ctx := Context{
			Logger:      NewLoggerWithWriters(VerbosityInfo, os.Stderr, os.Stderr),
			DebugOutput: true,
		}
		teeCtx, closeTee := ActivateEnvDebugTee(ctx, EnvConfig{Name: "dev"}, save, "team", "dev")
		// TraceCommand is gated to -vv on the terminal; the sink must see it
		// anyway — that is the "full trace without -vv" contract.
		teeCtx.TraceCommand("", "kubectl", "get", "pods")
		teeCtx.Info("==> Deploying team/dev 1.0.0")
		closeTee()

		if saved == nil || !saved.DebugOutput {
			t.Fatalf("expected debugoutput=true persisted, got %+v", saved)
		}
		raw, err := os.ReadFile(filepath.Join(home, ".erun", "team", "dev", "trace.log"))
		if err != nil {
			t.Fatalf("trace log not written: %v", err)
		}
		content := string(raw)
		for _, want := range []string{"kubectl get pods", "==> Deploying team/dev 1.0.0", "appending the full trace"} {
			if !strings.Contains(content, want) {
				t.Fatalf("trace log missing %q:\n%s", want, content)
			}
		}
		for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
			stamp, _, found := strings.Cut(line, " ")
			if !found {
				t.Fatalf("unstamped line %q", line)
			}
			if _, err := time.Parse(time.RFC3339, stamp); err != nil {
				t.Fatalf("line %q does not start with an RFC3339 stamp: %v", line, err)
			}
		}
	})

	t.Run("dry-run names the path and writes nothing", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		ctx := Context{
			Logger:      NewLoggerWithWriters(VerbosityInfo, os.Stderr, os.Stderr),
			DebugOutput: true,
			DryRun:      true,
		}
		_, closeTee := ActivateEnvDebugTee(ctx, EnvConfig{Name: "dev"}, func(string, EnvConfig) error {
			t.Fatal("dry-run must not persist")
			return nil
		}, "team", "dev")
		closeTee()
		if _, err := os.Stat(filepath.Join(home, ".erun", "team", "dev", "trace.log")); !os.IsNotExist(err) {
			t.Fatalf("dry-run must not create the trace log, stat err = %v", err)
		}
	})

	t.Run("neither the flag nor the setting means no tee", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, os.Stderr, os.Stderr)}
		teeCtx, closeTee := ActivateEnvDebugTee(ctx, EnvConfig{Name: "dev"}, nil, "team", "dev")
		teeCtx.Trace("invisible")
		closeTee()
		if _, err := os.Stat(filepath.Join(home, ".erun", "team", "dev", "trace.log")); !os.IsNotExist(err) {
			t.Fatalf("opted-out env must not create the trace log, stat err = %v", err)
		}
	})

	t.Run("the persisted setting alone activates the tee", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, os.Stderr, os.Stderr)}
		teeCtx, closeTee := ActivateEnvDebugTee(ctx, EnvConfig{Name: "dev", DebugOutput: true}, nil, "team", "dev")
		teeCtx.Trace("captured without any flag")
		closeTee()
		raw, err := os.ReadFile(filepath.Join(home, ".erun", "team", "dev", "trace.log"))
		if err != nil || !strings.Contains(string(raw), "captured without any flag") {
			t.Fatalf("persisted setting must capture (err=%v):\n%s", err, raw)
		}
	})

	t.Run("an oversized log rotates to .1", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		path := filepath.Join(home, ".erun", "team", "dev", "trace.log")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, make([]byte, envTraceLogMaxBytes+1), 0o600); err != nil {
			t.Fatal(err)
		}
		ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, os.Stderr, os.Stderr)}
		teeCtx, closeTee := ActivateEnvDebugTee(ctx, EnvConfig{Name: "dev", DebugOutput: true}, nil, "team", "dev")
		teeCtx.Trace("fresh after rotation")
		closeTee()
		if _, err := os.Stat(path + ".1"); err != nil {
			t.Fatalf("expected rotation to trace.log.1: %v", err)
		}
		raw, _ := os.ReadFile(path)
		if int64(len(raw)) > envTraceLogMaxBytes || !strings.Contains(string(raw), "fresh after rotation") {
			t.Fatalf("expected a fresh capped log, got %d bytes", len(raw))
		}
	})
}
