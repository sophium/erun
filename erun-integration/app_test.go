package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
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

// stubDesktopControlRequest is one request a stub control server received.
type stubDesktopControlRequest struct {
	Path string
	Body string
}

// stubDesktopControl is a stand-in control server's record of what it was
// asked, by path. WHICH question a trigger put to the desktop is carried by the
// path, not by the response, so a scenario that only inspected the response
// could not tell a dry run from a restart that happened to answer the same way.
type stubDesktopControl struct {
	mu       sync.Mutex
	requests []stubDesktopControlRequest
}

// paths returns the paths the stub was asked on, in order.
func (s *stubDesktopControl) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.requests))
	for _, request := range s.requests {
		out = append(out, request.Path)
	}
	return out
}

// bodies returns every request body the stub received, joined.
func (s *stubDesktopControl) bodies() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.requests))
	for _, request := range s.requests {
		out = append(out, request.Body)
	}
	return strings.Join(out, "\n")
}

// stubDesktopControlServer stands in for erun-ui's own restart control server
// (erun-ui/restart_control.go) so a real (non-dry-run) `erun app restart` can
// be driven end-to-end from the compiled binary without a real desktop
// process. respond is the exact body the stub writes for every request.
func stubDesktopControlServer(t *testing.T, port int, respond string) *stubDesktopControl {
	t.Helper()
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("listen on 127.0.0.1:%d: %v", port, err)
	}
	stub := &stubDesktopControl{}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		stub.mu.Lock()
		stub.requests = append(stub.requests, stubDesktopControlRequest{Path: r.URL.Path, Body: string(body)})
		stub.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respond))
	})}
	t.Cleanup(func() { _ = server.Close() })
	go func() { _ = server.Serve(listener) }()
	return stub
}

// stubPredatingDesktopControlServer stands in for a desktop built before the
// plan path existed: it serves the restart path and 404s everything else, which
// is what the compiled binary meets whenever the desktop running on this
// machine is older than the CLI asking it a question. The restart path is
// deliberately NOT recorded as answered — any request that lands there is the
// restart the dry run must not perform, and the assertions read the record.
func stubPredatingDesktopControlServer(t *testing.T, port int) *stubDesktopControl {
	t.Helper()
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("listen on 127.0.0.1:%d: %v", port, err)
	}
	stub := &stubDesktopControl{}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.mu.Lock()
		stub.requests = append(stub.requests, stubDesktopControlRequest{Path: r.URL.Path})
		stub.mu.Unlock()
		if r.URL.Path != eruncommon.DesktopControlPath {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})}
	t.Cleanup(func() { _ = server.Close() })
	go func() { _ = server.Serve(listener) }()
	return stub
}

// assertAskedExactly checks the trigger put exactly one request to path. Both
// halves matter: a dry run that reached the restart path would have restarted
// the desktop it was asked not to touch.
func assertAskedExactly(t *testing.T, stub *stubDesktopControl, path, scenario string) {
	t.Helper()
	if paths := stub.paths(); len(paths) != 1 || paths[0] != path {
		t.Fatalf("expected %s to send exactly one request to %s, got %v", scenario, path, paths)
	}
}

// writeDesktopControlMarker stages the record erun-ui's restart control
// server writes at startup (see erun-common/desktop_restart.go), at the same
// path `erun app restart` resolves it from under the scenario's isolated
// config dir.
func writeDesktopControlMarker(t *testing.T, setup env.Setup, pid, controlPort int) {
	t.Helper()
	dir := filepath.Join(setup.ConfigHome, "ERun")
	if runtime.GOOS == "darwin" {
		dir = filepath.Join(setup.Home, "Library", "Application Support", "ERun")
	}
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
		// whole scenario, so the dry run resolves a real, verified target and
		// reports would-restart. The control port is one nothing is listening on,
		// which is why the plan it cannot read is reported as missing here — the
		// dedicated scenario below stages the deskop's own answer.
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
		// Same shape as the scenario above: a live pid and a control port nothing
		// answers on, so the plan is reported as unreadable alongside the resume
		// the environment variable supplied.
		setup := env.New(t)
		writeDesktopControlMarker(t, setup, os.Getpid(), 4242)
		envVars := append(setup.Env(), "ERUN_ORCHESTRATOR_ID=env-orchestrator")
		result := erun.Run(t, []string{"app", "restart", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "app/restart_dry_run_orchestrator_defaults_from_env_var", normalize.Apply(result.Combined))
	})

	t.Run("restart_dry_run_names_the_orchestrators_it_would_not_resume", func(t *testing.T) {
		// The reported flow, one step earlier than it was reported: a restart is
		// about to strand an orchestrator that has no conversation attached, and
		// the dry run is the last moment attaching it can still change the
		// outcome. The stub answers with the plan the desktop computes, so this
		// pins what a plan turns into on the operator's terminal -- including the
		// orchestrator that asks for the restart, which must NOT be reported as
		// stranded because the restart hands it back its own live conversation.
		skipIfPortsBusy(t, restartControlPort)
		setup := env.New(t)
		writeDesktopControlMarker(t, setup, os.Getpid(), restartControlPort)
		stub := stubDesktopControlServer(t, restartControlPort,
			`{"ok":true,"preview":[`+
				`{"orchestratorId":"erun","conversationId":"3bde477f-6a1c-4a2e-9f0d-2c5b8e7a1d34"},`+
				`{"orchestratorId":"petios-ops","conversationId":"1cbe8921-0f4a-4c5e-9d21-8b6f3a2e5c70","notice":"Reopened petios-ops on the conversation derived from its id (1cbe8921-0f4a-4c5e-9d21-8b6f3a2e5c70), not the one its last session was working in (6942fcd4-2f8b-4c6d-8e10-7a3d9b5c4e21). That conversation is still there; manage the orchestrator to attach it."}]}`)
		result := erun.Run(t, []string{"app", "restart", "--orchestrator", "erun", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "app/restart_dry_run_names_the_orchestrators_it_would_not_resume", normalize.Apply(result.Combined))
		assertAskedExactly(t, stub, eruncommon.DesktopControlPlanPath, "a dry run")
		if bodies := stub.bodies(); !strings.Contains(bodies, `"orchestratorId":"erun"`) {
			t.Fatalf("expected the dry run to name the orchestrator to resume, sent %q", bodies)
		}
	})

	t.Run("restart_dry_run_carries_the_plan_in_json_output", func(t *testing.T) {
		// The plan is what an orchestrator consumes, so the structured result has
		// to carry it rather than leaving it only in the human stream.
		skipIfPortsBusy(t, restartControlPort)
		setup := env.New(t)
		writeDesktopControlMarker(t, setup, os.Getpid(), restartControlPort)
		stub := stubDesktopControlServer(t, restartControlPort,
			`{"ok":true,"preview":[{"orchestratorId":"erun-ideas","conversationId":"9f90b94f-3c2a-4b71-8d05-6e4f1a9c2b83","notice":"Reopened erun-ideas on the conversation derived from its id (9f90b94f-3c2a-4b71-8d05-6e4f1a9c2b83), not the one its last session was working in (6942fcd4-2f8b-4c6d-8e10-7a3d9b5c4e21). That conversation is still there; manage the orchestrator to attach it."}]}`)
		result := erun.Run(t, []string{"app", "restart", "--dry-run", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "app/restart_dry_run_carries_the_plan_in_json_output", normalize.Apply(result.Combined))
		assertAskedExactly(t, stub, eruncommon.DesktopControlPlanPath, "a dry run")
	})

	t.Run("restart_dry_run_reports_a_plan_it_could_not_read", func(t *testing.T) {
		// A live, verified target that does not answer the plan's question. The
		// dry run still resolves a target and still would restart -- the marker
		// and the liveness probe are what established that -- but the missing half
		// is named rather than dropped, because an empty plan is exactly what
		// "nothing would be stranded" must not be mistaken for.
		skipIfPortsBusy(t, restartControlPort)
		setup := env.New(t)
		writeDesktopControlMarker(t, setup, os.Getpid(), restartControlPort)
		result := erun.Run(t, []string{"app", "restart", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "app/restart_dry_run_reports_a_plan_it_could_not_read", normalize.Apply(result.Combined))
	})

	t.Run("restart_dry_run_refuses_the_plan_path_an_older_desktop_does_not_serve", func(t *testing.T) {
		// The version skew, and the reason the plan is a path of its own. A
		// desktop older than this command ignores unknown JSON fields, so a flag
		// asking for the plan would reach it as an ordinary restart: it would
		// restart itself and answer what a restart answers. The path it does not
		// serve refuses the question instead, and the desktop is left running.
		skipIfPortsBusy(t, restartControlPort)
		setup := env.New(t)
		writeDesktopControlMarker(t, setup, os.Getpid(), restartControlPort)
		stub := stubPredatingDesktopControlServer(t, restartControlPort)
		result := erun.Run(t, []string{"app", "restart", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "app/restart_dry_run_refuses_the_plan_path_an_older_desktop_does_not_serve", normalize.Apply(result.Combined))
		assertAskedExactly(t, stub, eruncommon.DesktopControlPlanPath, "a dry run against an older desktop")
	})

	t.Run("restart_real_run_restarts_a_live_target", func(t *testing.T) {
		// A stub stands in for erun-ui's own control server, so this scenario
		// exercises the real HTTP round trip (postDesktopRestart) rather than
		// only the dry-run resolve-and-verify path above.
		skipIfPortsBusy(t, restartControlPort)
		setup := env.New(t)
		writeDesktopControlMarker(t, setup, os.Getpid(), restartControlPort)
		stub := stubDesktopControlServer(t, restartControlPort, `{"ok":true}`)
		result := erun.Run(t, []string{"app", "restart", "--orchestrator", "my-orchestrator"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "app/restart_real_run_restarts_a_live_target", normalize.Apply(result.Combined))
		// The restart is a restart: it goes to the restart path, never to the one
		// reserved for the question, which is the one way an operator could be
		// shown a plan and no restart.
		assertAskedExactly(t, stub, eruncommon.DesktopControlPath, "a real restart")
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
