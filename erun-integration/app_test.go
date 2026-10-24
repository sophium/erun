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

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// restartControlPort is reserved for the real-run restart scenarios
// (erun-integration/AGENTS.md's "pin a high port range").
const restartControlPort = 26150

type stubDesktopControlRequest struct {
	Path string
	Body string
}

// stubDesktopControl records requests by path: the path, not the response,
// distinguishes a dry run from a restart.
type stubDesktopControl struct {
	mu       sync.Mutex
	requests []stubDesktopControlRequest
}

func (s *stubDesktopControl) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.requests))
	for _, request := range s.requests {
		out = append(out, request.Path)
	}
	return out
}

func (s *stubDesktopControl) bodies() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.requests))
	for _, request := range s.requests {
		out = append(out, request.Body)
	}
	return strings.Join(out, "\n")
}

// stubDesktopControlServer stands in for erun-ui's restart control server.
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

// stubPredatingDesktopControlServer models a desktop without the plan path:
// it 404s everything but restart, and records a restart request as unanswered.
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
		if r.URL.Path != core.DesktopControlPath {
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

func assertAskedExactly(t *testing.T, stub *stubDesktopControl, path, scenario string) {
	t.Helper()
	if paths := stub.paths(); len(paths) != 1 || paths[0] != path {
		t.Fatalf("expected %s to send exactly one request to %s, got %v", scenario, path, paths)
	}
}

// writeDesktopControlMarker stages the record erun-ui's control server writes.
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
	t.Run("help", testAppHelp)

	t.Run("dry_run_traces_app_executable_without_launching", testAppDryRunTracesAppExecutableWithoutLaunching)

	t.Run("dry_run_traces_headless_flags_for_app_executable", testAppDryRunTracesHeadlessFlagsForAppExecutable)

	t.Run("real_run_detaches_app_stub_with_headless_args", testAppRealRunDetachesAppStubWithHeadlessArgs)

	t.Run("real_run_errors_when_app_binary_missing", testAppRealRunErrorsWhenAppBinaryMissing)

	t.Run("real_run_propagates_invalid_override_path_error", testAppRealRunPropagatesInvalidOverridePathError)

	t.Run("restart_help", testAppRestartHelp)

	t.Run("restart_dry_run_refused_when_no_desktop_running", testAppRestartDryRunRefusedWhenNoDesktopRunning)

	t.Run("restart_dry_run_refused_when_target_pid_does_not_resolve", testAppRestartDryRunRefusedWhenTargetPIDDoesNotResolve)

	t.Run("restart_dry_run_resolves_a_live_target", testAppRestartDryRunResolvesALiveTarget)

	t.Run("restart_dry_run_orchestrator_defaults_from_env_var", testAppRestartDryRunOrchestratorDefaultsFromEnvVar)

	t.Run("restart_dry_run_names_the_orchestrators_it_would_not_reopen", testAppRestartDryRunNamesTheOrchestratorsItWouldNotReopen)

	t.Run("restart_dry_run_carries_the_plan_in_json_output", testAppRestartDryRunCarriesThePlanInJSONOutput)

	t.Run("restart_dry_run_reports_a_plan_it_could_not_read", testAppRestartDryRunReportsAPlanItCouldNotRead)

	t.Run("restart_dry_run_refuses_the_plan_path_an_older_desktop_does_not_serve", testAppRestartDryRunRefusesThePlanPathAnOlderDesktopDoesNotServe)

	t.Run("restart_real_run_restarts_a_live_target", testAppRestartRealRunRestartsALiveTarget)

	t.Run("restart_real_run_reports_a_declined_restart", testAppRestartRealRunReportsADeclinedRestart)
}

func testAppHelp(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	result := erun.Run(t, []string{"app", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "app/help", normalize.Apply(result.Combined))
}

func testAppDryRunTracesAppExecutableWithoutLaunching(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	result := erun.Run(t, []string{"app", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "app/dry_run_traces_app_executable_without_launching", normalize.Apply(result.Combined))
}

func testAppDryRunTracesHeadlessFlagsForAppExecutable(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	result := erun.Run(t, []string{"app", "--headless", "--port", "34123", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "app/dry_run_traces_headless_flags_for_app_executable", normalize.Apply(result.Combined))
}

func testAppRealRunDetachesAppStubWithHeadlessArgs(t *testing.T) {
	t.Parallel()
	// The child detaches, so the stub's marker file is the only proof of argv.
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
}

func testAppRealRunErrorsWhenAppBinaryMissing(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	result := erun.Run(t, []string{"app"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when erun-app is missing, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "app/real_run_errors_when_app_binary_missing", normalize.Apply(result.Combined))
}

func testAppRealRunPropagatesInvalidOverridePathError(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	envVars := append(setup.Env(), "ERUN_ERUN_APP_BIN="+filepath.Join(setup.Cwd, "missing", "erun-app"))
	result := erun.Run(t, []string{"app"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for invalid override path, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "app/real_run_propagates_invalid_override_path_error", normalize.Apply(result.Combined))
}

func testAppRestartHelp(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	result := erun.Run(t, []string{"app", "restart", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "app/restart_help", normalize.Apply(result.Combined))
}

func testAppRestartDryRunRefusedWhenNoDesktopRunning(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	result := erun.Run(t, []string{"app", "restart", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit refusing the restart, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "app/restart_dry_run_refused_when_no_desktop_running", normalize.Apply(result.Combined))
}

func testAppRestartDryRunRefusedWhenTargetPIDDoesNotResolve(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	writeDesktopControlMarker(t, setup, 999999999, 4242)
	result := erun.Run(t, []string{"app", "restart", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit refusing the restart, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "app/restart_dry_run_refused_when_target_pid_does_not_resolve", normalize.Apply(result.Combined))
}

func testAppRestartDryRunResolvesALiveTarget(t *testing.T) {
	t.Parallel()
	// Our own pid is a live target; nothing listens on the port, so the plan
	// is reported missing.
	setup := env.New(t)
	writeDesktopControlMarker(t, setup, os.Getpid(), 4242)
	result := erun.Run(t, []string{"app", "restart", "--orchestrator", "my-orchestrator", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "app/restart_dry_run_resolves_a_live_target", normalize.Apply(result.Combined))
}

func testAppRestartDryRunOrchestratorDefaultsFromEnvVar(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	writeDesktopControlMarker(t, setup, os.Getpid(), 4242)
	envVars := append(setup.Env(), "ERUN_ORCHESTRATOR_ID=env-orchestrator")
	result := erun.Run(t, []string{"app", "restart", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "app/restart_dry_run_orchestrator_defaults_from_env_var", normalize.Apply(result.Combined))
}

func testAppRestartDryRunNamesTheOrchestratorsItWouldNotReopen(t *testing.T) {
	skipIfPortsBusy(t, restartControlPort)
	setup := env.New(t)
	writeDesktopControlMarker(t, setup, os.Getpid(), restartControlPort)
	stub := stubDesktopControlServer(t, restartControlPort,
		`{"ok":true,"preview":[`+
			`{"orchestratorId":"erun"},`+
			`{"orchestratorId":"petios-ops","notice":"The restart will not reopen petios-ops: the gateway is turned on but no gateway catalog is configured. Its background agent keeps running; fix that and open it again."}]}`)
	result := erun.Run(t, []string{"app", "restart", "--orchestrator", "erun", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "app/restart_dry_run_names_the_orchestrators_it_would_not_reopen", normalize.Apply(result.Combined))
	assertAskedExactly(t, stub, core.DesktopControlPlanPath, "a dry run")
	if bodies := stub.bodies(); !strings.Contains(bodies, `"orchestratorId":"erun"`) {
		t.Fatalf("expected the dry run to name the orchestrator to resume, sent %q", bodies)
	}
}

func testAppRestartDryRunCarriesThePlanInJSONOutput(t *testing.T) {
	skipIfPortsBusy(t, restartControlPort)
	setup := env.New(t)
	writeDesktopControlMarker(t, setup, os.Getpid(), restartControlPort)
	stub := stubDesktopControlServer(t, restartControlPort,
		`{"ok":true,"preview":[{"orchestratorId":"erun-ideas","notice":"The restart will not reopen erun-ideas: the gateway is turned on but no gateway catalog is configured. Its background agent keeps running; fix that and open it again."}]}`)
	result := erun.Run(t, []string{"app", "restart", "--dry-run", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "app/restart_dry_run_carries_the_plan_in_json_output", normalize.Apply(result.Combined))
	assertAskedExactly(t, stub, core.DesktopControlPlanPath, "a dry run")
}

func testAppRestartDryRunReportsAPlanItCouldNotRead(t *testing.T) {
	// An empty plan must not read as "nothing would be stranded".
	skipIfPortsBusy(t, restartControlPort)
	setup := env.New(t)
	writeDesktopControlMarker(t, setup, os.Getpid(), restartControlPort)
	result := erun.Run(t, []string{"app", "restart", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "app/restart_dry_run_reports_a_plan_it_could_not_read", normalize.Apply(result.Combined))
}

func testAppRestartDryRunRefusesThePlanPathAnOlderDesktopDoesNotServe(t *testing.T) {
	// An older desktop ignores unknown JSON fields, so the plan needs its own
	// path; otherwise asking would restart it.
	skipIfPortsBusy(t, restartControlPort)
	setup := env.New(t)
	writeDesktopControlMarker(t, setup, os.Getpid(), restartControlPort)
	stub := stubPredatingDesktopControlServer(t, restartControlPort)
	result := erun.Run(t, []string{"app", "restart", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "app/restart_dry_run_refuses_the_plan_path_an_older_desktop_does_not_serve", normalize.Apply(result.Combined))
	assertAskedExactly(t, stub, core.DesktopControlPlanPath, "a dry run against an older desktop")
}

func testAppRestartRealRunRestartsALiveTarget(t *testing.T) {
	skipIfPortsBusy(t, restartControlPort)
	setup := env.New(t)
	writeDesktopControlMarker(t, setup, os.Getpid(), restartControlPort)
	stub := stubDesktopControlServer(t, restartControlPort, `{"ok":true}`)
	result := erun.Run(t, []string{"app", "restart", "--orchestrator", "my-orchestrator"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "app/restart_real_run_restarts_a_live_target", normalize.Apply(result.Combined))
	assertAskedExactly(t, stub, core.DesktopControlPath, "a real restart")
}

func testAppRestartRealRunReportsADeclinedRestart(t *testing.T) {
	skipIfPortsBusy(t, restartControlPort)
	setup := env.New(t)
	writeDesktopControlMarker(t, setup, os.Getpid(), restartControlPort)
	stubDesktopControlServer(t, restartControlPort, `{"ok":false,"error":"persist restart target: disk full"}`)
	result := erun.Run(t, []string{"app", "restart"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for a declined restart, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "app/restart_real_run_reports_a_declined_restart", normalize.Apply(result.Combined))
}
