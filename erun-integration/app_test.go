package integration

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestApp(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"app", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "app/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_traces_app_executable_without_launching", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"app", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "app/dry_run_traces_app_executable_without_launching", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_traces_headless_flags_for_app_executable", func(t *testing.T) {
		// --headless / --port let a headless browser harness drive the
		// same frontend the desktop app renders.
		setup := env.New(t)
		result := erun.Run(t, []string{"app", "--headless", "--port", "34123", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "app/dry_run_traces_headless_flags_for_app_executable", normalize.Apply(result.Combined))
	})

	t.Run("real_run_detaches_app_stub_with_headless_args", func(t *testing.T) {
		// The launcher detaches the desktop process immediately, so the only
		// proof the headless argv was delivered is the marker file the stub
		// writes — the golden cannot observe the detached child.
		setup := env.New(t)
		stubs := setup.Cwd + "/stubs"
		marker := filepath.Join(setup.Cwd, "app-launch-marker")
		fixture.StubBinaryWithScript(t, stubs, "erun-app", `printf '%s\n' "$*" > '`+marker+`'
exit 0`)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "erun-app")...)
		result := erun.Run(t, []string{"-vv", "app", "--headless", "--port", "34123"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "app/real_run_detaches_app_stub_with_headless_args", normalize.Apply(result.Combined))
		argv := strings.TrimSpace(waitForFile(t, marker, 5*time.Second))
		if argv != "--headless --port 34123" {
			t.Errorf("expected detached erun-app to receive headless argv, got %q", argv)
		}
	})

	t.Run("real_run_errors_when_app_binary_missing", func(t *testing.T) {
		// A missing erun-app must surface the friendly build-or-install
		// message rather than a raw exec error.
		setup := env.New(t)
		envVars := append(setup.Env(), emptyPathDir(t, setup.Cwd))
		result := erun.Run(t, []string{"app"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when erun-app is missing, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "app/real_run_errors_when_app_binary_missing", normalize.Apply(result.Combined))
	})

	t.Run("real_run_propagates_invalid_override_path_error", func(t *testing.T) {
		// A bad executable override must propagate the raw fork/exec error,
		// not the friendly not-found message, so the broken path stays visible.
		setup := env.New(t)
		envVars := append(setup.Env(), "ERUN_ERUN_APP_BIN="+filepath.Join(setup.Cwd, "missing", "erun-app"))
		result := erun.Run(t, []string{"app"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for invalid override path, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "app/real_run_propagates_invalid_override_path_error", normalize.Apply(result.Combined))
	})
}
