package integration

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
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

// restartControlPort is a fixed, reserved port for the real-run restart
// scenarios below (per erun-integration/AGENTS.md's "pin a high port range"),
// far from erun's default 17000 range and from the 26100/26200/26300 ports
// other real-run scenarios already reserve.
const restartControlPort = 26150

// stubDesktopControlServer stands in for erun-ui's own restart control server
// (erun-ui/restart_control.go) so a real (non-dry-run) `erun app restart` can
// be driven end-to-end from the compiled binary without a real desktop
// process. respond is the exact body the stub writes for every request.
func stubDesktopControlServer(t *testing.T, port int, respond string) {
	t.Helper()
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("listen on 127.0.0.1:%d: %v", port, err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respond))
	})}
	t.Cleanup(func() { _ = server.Close() })
	go func() { _ = server.Serve(listener) }()
}

// writeDesktopControlMarker stages the record erun-ui's restart control
// server writes at startup (see erun-common/desktop_restart.go), at the same
// path `erun app restart` resolves it from under the scenario's isolated
// config dir.
func writeDesktopControlMarker(t *testing.T, setup env.Setup, pid, controlPort int) {
	t.Helper()
	dir := filepath.Join(setup.ConfigHome, "ERun")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	data, err := json.Marshal(map[string]int{"pid": pid, "controlPort": controlPort, "startedAtUnix": 1700000000})
	if err != nil {
		t.Fatalf("marshal marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "desktop-control.json"), data, 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
}

func TestApp(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		result := erun.Run(t, []string{"app", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "app/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_traces_app_executable_without_launching", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		result := erun.Run(t, []string{"app", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "app/dry_run_traces_app_executable_without_launching", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_traces_headless_flags_for_app_executable", func(t *testing.T) {
		t.Parallel()
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
		t.Parallel()
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
		t.Parallel()
		// A missing erun-app must surface the friendly build-or-install
		// message rather than a raw exec error. The scenario's scrubbed PATH is
		// what makes erun-app absent, on every host.
		setup := env.New(t)
		result := erun.Run(t, []string{"app"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when erun-app is missing, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "app/real_run_errors_when_app_binary_missing", normalize.Apply(result.Combined))
	})

	t.Run("real_run_propagates_invalid_override_path_error", func(t *testing.T) {
		t.Parallel()
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

	t.Run("restart_help", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		result := erun.Run(t, []string{"app", "restart", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "app/restart_help", normalize.Apply(result.Combined))
	})

	t.Run("restart_dry_run_refused_when_no_desktop_running", func(t *testing.T) {
		t.Parallel()
		// No marker was ever staged, which is the ordinary "nothing running"
		// case: dry-run must refuse plainly rather than guess at a target.
		setup := env.New(t)
		result := erun.Run(t, []string{"app", "restart", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit refusing the restart, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "app/restart_dry_run_refused_when_no_desktop_running", normalize.Apply(result.Combined))
	})

	t.Run("restart_dry_run_refused_when_target_pid_does_not_resolve", func(t *testing.T) {
		t.Parallel()
		// A marker naming a pid nothing on this host will ever use: the
		// unsafe-target refusal root AGENTS.md requires (a relauncher armed
		// against a dead target kills nothing and a following relaunch just
		// re-activates what was already there).
		setup := env.New(t)
		writeDesktopControlMarker(t, setup, 999999999, 4242)
		result := erun.Run(t, []string{"app", "restart", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit refusing the restart, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "app/restart_dry_run_refused_when_target_pid_does_not_resolve", normalize.Apply(result.Combined))
	})

	t.Run("restart_dry_run_resolves_a_live_target", func(t *testing.T) {
		t.Parallel()
		// The marker names this test process's own pid, which is alive for the
		// whole scenario, so the dry-run plan resolves a real, verified target
		// and reports would-restart without contacting it.
		setup := env.New(t)
		writeDesktopControlMarker(t, setup, os.Getpid(), 4242)
		result := erun.Run(t, []string{"app", "restart", "--orchestrator", "my-orchestrator", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "app/restart_dry_run_resolves_a_live_target", normalize.Apply(result.Combined))
	})

	t.Run("restart_dry_run_orchestrator_defaults_from_env_var", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		writeDesktopControlMarker(t, setup, os.Getpid(), 4242)
		envVars := append(setup.Env(), "ERUN_ORCHESTRATOR_ID=env-orchestrator")
		result := erun.Run(t, []string{"app", "restart", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "app/restart_dry_run_orchestrator_defaults_from_env_var", normalize.Apply(result.Combined))
	})

	t.Run("restart_real_run_restarts_a_live_target", func(t *testing.T) {
		// A stub stands in for erun-ui's own control server, so this scenario
		// exercises the real HTTP round trip (postDesktopRestart) rather than
		// only the dry-run resolve-and-verify path above.
		skipIfPortsBusy(t, restartControlPort)
		setup := env.New(t)
		writeDesktopControlMarker(t, setup, os.Getpid(), restartControlPort)
		stubDesktopControlServer(t, restartControlPort, `{"ok":true}`)
		result := erun.Run(t, []string{"app", "restart", "--orchestrator", "my-orchestrator"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "app/restart_real_run_restarts_a_live_target", normalize.Apply(result.Combined))
	})

	t.Run("restart_real_run_reports_a_declined_restart", func(t *testing.T) {
		// The desktop is reachable but its own RestartApp failed; distinct from
		// every refusal above, which never reach the desktop at all.
		skipIfPortsBusy(t, restartControlPort)
		setup := env.New(t)
		writeDesktopControlMarker(t, setup, os.Getpid(), restartControlPort)
		stubDesktopControlServer(t, restartControlPort, `{"ok":false,"error":"persist restart target: disk full"}`)
		result := erun.Run(t, []string{"app", "restart"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for a declined restart, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "app/restart_real_run_reports_a_declined_restart", normalize.Apply(result.Combined))
	})
}
