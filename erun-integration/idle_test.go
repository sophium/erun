package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestIdle(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"idle", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "idle/help", normalize.Apply(result.Combined))
	})

	t.Run("status_for_seeded_env", func(t *testing.T) {
		// Exercises eruncommon.ResolveStoredEnvironmentIdleStatus and
		// erun-cli/cmd/idle.go's writeIdleStatus / idleMarkerValue. The
		// working-hours marker is wall-clock-dependent so we assert the
		// stable lines exactly and the variable lines structurally.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"idle", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		stableLines := []string{
			"timeout: 300 seconds",
			"seconds until stop: 0",
			"stop eligible: off",
			"stop blocked: environment is not cloud-managed",
			"ssh: idle",
			"api: idle",
			"mcp: idle",
			"cli: idle",
			"codex: idle",
		}
		for _, line := range stableLines {
			if !strings.Contains(result.Stdout, line) {
				t.Errorf("expected stdout to contain %q, got:\n%s", line, result.Stdout)
			}
		}
		// working-hours: idle (when wall clock is outside 08:00-20:00) OR
		// active (12345s) (when inside). Either form is acceptable.
		workingHours := regexp.MustCompile(`(?m)^\s*working-hours: (idle|active \(\d+s\))\s*$`)
		if !workingHours.MatchString(result.Stdout) {
			t.Errorf("expected working-hours marker line, got:\n%s", result.Stdout)
		}
	})

	t.Run("status_json_output", func(t *testing.T) {
		// Exercises idle.go --json branch: instead of writeIdleStatus's
		// labeled-value output, the status should be emitted as JSON via
		// json.NewEncoder. The structured fields (timeout, markers) must
		// parse cleanly back into common.EnvironmentIdleStatus.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"idle", "team", "dev", "--json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		stdout := strings.TrimSpace(result.Stdout)
		if !strings.HasPrefix(stdout, "{") || !strings.Contains(stdout, "\"markers\"") {
			t.Errorf("expected JSON object with markers field, got:\n%s", stdout)
		}
	})

	t.Run("missing_env_errors", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"idle", "missing", "missing"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "idle/missing_env_errors", normalize.Apply(result.Combined))
	})

	t.Run("json_overlays_pending_grace_window", func(t *testing.T) {
		// eruncommon.overlayStopPending: with a stop-pending.json on disk
		// for a cloud-managed, stop-eligible env, the resolved status must
		// surface stopPendingSince / gracePeriodSeconds /
		// secondsUntilForcedStop. A Since in the far future clamps elapsed
		// to 0, so secondsUntilForcedStop deterministically equals the full
		// grace (600) for decades without sleeping. Whole-stream golden is
		// impossible: the working-hours marker varies by wall clock, so the
		// overlay fields are asserted directly on the JSON stream.
		setup := env.New(t)
		seedManagedCloudTenantEnv(t, setup, "team", "dev")
		seedStopPending(t, setup.Home, "team", "dev", `{"since": "2099-01-01T00:00:00Z", "graceSeconds": 600}
`)
		result := erun.Run(t, []string{"idle", "team", "dev", "--json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		for _, want := range []string{
			`"managedCloud": true`,
			`"stopEligible": true`,
			`"stopPendingSince": "2099-01-01T00:00:00Z"`,
			`"gracePeriodSeconds": 600`,
			`"secondsUntilForcedStop": 600`,
		} {
			if !strings.Contains(result.Stdout, want) {
				t.Errorf("expected JSON status to contain %s, got:\n%s", want, result.Stdout)
			}
		}
	})

	t.Run("reports_stop_error_tail_from_env_log", func(t *testing.T) {
		// eruncommon.loadEnvironmentIdleStopError: the per-env
		// ~/.erun/<tenant>/<env>/idle-stop.log surfaces as `stop error:` and
		// only its last 4000 bytes are kept, so a repeatedly-failing stop
		// loop cannot flood the status output. Whole-stream golden is
		// impossible (working-hours line varies by wall clock), so the
		// truncation contract is asserted via head/tail markers.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		logDir := filepath.Join(setup.Home, ".erun", "team", "dev")
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", logDir, err)
		}
		content := "HEAD-MARKER " + strings.Repeat("x", 4100) + " TAIL-MARKER"
		if err := os.WriteFile(filepath.Join(logDir, "idle-stop.log"), []byte(content), 0o644); err != nil {
			t.Fatalf("write idle-stop.log: %v", err)
		}
		result := erun.Run(t, []string{"idle", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Stdout, "stop error: ") {
			t.Errorf("expected stop error line, got:\n%s", result.Stdout)
		}
		if !strings.Contains(result.Stdout, "TAIL-MARKER") {
			t.Errorf("expected log tail retained, got:\n%s", result.Stdout)
		}
		if strings.Contains(result.Stdout, "HEAD-MARKER") {
			t.Errorf("expected log head truncated past 4000 bytes, got:\n%s", result.Stdout)
		}
	})

	t.Run("reports_stop_error_from_legacy_log_location", func(t *testing.T) {
		// loadEnvironmentIdleStopError's legacy fallback: when the per-env
		// log is absent, the shared ~/.erun/idle-stop.log written by older
		// runtime images is read instead.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		legacyDir := filepath.Join(setup.Home, ".erun")
		if err := os.MkdirAll(legacyDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", legacyDir, err)
		}
		if err := os.WriteFile(filepath.Join(legacyDir, "idle-stop.log"), []byte("legacy stop failure: AccessDenied"), 0o644); err != nil {
			t.Fatalf("write legacy idle-stop.log: %v", err)
		}
		result := erun.Run(t, []string{"idle", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Stdout, "stop error: legacy stop failure: AccessDenied") {
			t.Errorf("expected legacy stop error surfaced, got:\n%s", result.Stdout)
		}
	})
}
