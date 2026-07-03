package eruncommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestActivateEnvTrace owns the real-write branches, which the dry-run
// integration harness cannot reach by design; the dry-run trace contract
// itself is locked by the open goldens.
func TestActivateEnvTrace(t *testing.T) {
	t.Run("tee captures suppressed trace lines, stamped, with no opt-in", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, os.Stderr, os.Stderr)}
		teeCtx, closeTee := ActivateEnvTrace(ctx, "team", "dev")
		// TraceCommand is gated to -vv on the terminal; the sink must see it
		// anyway — that is the "full trace without -vv" contract.
		teeCtx.TraceCommand("", "kubectl", "get", "pods")
		teeCtx.Info("==> Deploying team/dev 1.0.0")
		closeTee()

		raw, err := os.ReadFile(filepath.Join(home, ".erun", "team", "dev", "trace.log"))
		if err != nil {
			t.Fatalf("trace log not written: %v", err)
		}
		content := string(raw)
		assertTraceLogContains(t, content, "kubectl get pods", "==> Deploying team/dev 1.0.0", "appending the full trace")
		assertTraceLogLinesStamped(t, content)
	})

	t.Run("dry-run names the path and writes nothing", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		ctx := Context{
			Logger: NewLoggerWithWriters(VerbosityInfo, os.Stderr, os.Stderr),
			DryRun: true,
		}
		_, closeTee := ActivateEnvTrace(ctx, "team", "dev")
		closeTee()
		if _, err := os.Stat(filepath.Join(home, ".erun", "team", "dev", "trace.log")); !os.IsNotExist(err) {
			t.Fatalf("dry-run must not create the trace log, stat err = %v", err)
		}
	})

	t.Run("a blank tenant or environment deactivates the tee", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, os.Stderr, os.Stderr)}
		teeCtx, closeTee := ActivateEnvTrace(ctx, " ", "dev")
		teeCtx.Trace("invisible")
		closeTee()
		entries, err := os.ReadDir(filepath.Join(home, ".erun"))
		if !os.IsNotExist(err) && len(entries) > 0 {
			t.Fatalf("unscoped invocation must not create a trace log, got %v (err=%v)", entries, err)
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
		teeCtx, closeTee := ActivateEnvTrace(ctx, "team", "dev")
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

func assertTraceLogContains(t *testing.T, content string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(content, want) {
			t.Fatalf("trace log missing %q:\n%s", want, content)
		}
	}
}

func assertTraceLogLinesStamped(t *testing.T, content string) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		stamp, _, found := strings.Cut(line, " ")
		if !found {
			t.Fatalf("unstamped line %q", line)
		}
		if _, err := time.Parse(time.RFC3339, stamp); err != nil {
			t.Fatalf("line %q does not start with an RFC3339 stamp: %v", line, err)
		}
	}
}
