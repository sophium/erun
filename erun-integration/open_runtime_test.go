package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testOpenDeployFlagDryRunDeploysRuntimeBeforeOpening(t *testing.T) {
	t.Parallel()
	// --deploy deploys the runtime before opening; the NotFound stub selects the deploy branch.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlNotFound(t, setup)
	// Stands in for the registry read confirming the chart is published.
	envVars = append(envVars, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	result := erun.Run(t, []string{"open", "team", "dev", "--deploy", "--no-shell", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/deploy_flag_dry_run_deploys_runtime_before_opening", normalize.Apply(result.Combined))
}

func testOpenDryRunWakesStoppedRuntimeBeforeForwarding(t *testing.T) {
	t.Parallel()
	// port-forward cannot attach to zero replicas, so a stopped runtime is scaled
	// up and waited for before forwarding.
	setup := env.New(t)
	fixture.SeedStoppedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlRunState(t, setup, 0, 0)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/dry_run_wakes_stopped_runtime_before_forwarding", normalize.Apply(result.Combined))
}

func testOpenDryRunUnaffectedByKubectlDeploymentWaitLibraryExecutionMode(t *testing.T) {
	t.Parallel()
	// Library mode must not change the dry-run trace.
	setup := env.New(t)
	fixture.SeedStoppedTenantEnv(t, setup, "team", "dev")
	seedExecutionMode(t, setup, "kubectl-deployment-wait", "library")
	envVars := stubKubectlRunState(t, setup, 0, 0)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/dry_run_wakes_stopped_runtime_before_forwarding", normalize.Apply(result.Combined))
}

func testOpenReconnectDryRunRefusesToStartAStoppedRuntime(t *testing.T) {
	t.Parallel()
	// A supervisor reattach must not wake a stopped environment.
	setup := env.New(t)
	fixture.SeedStoppedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlRunState(t, setup, 0, 0)
	result := erun.Run(t, []string{"open", "team", "dev", "--reconnect", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit reconnecting to a stopped environment:\n%s", result.Combined)
	}
	golden.Equal(t, "open/reconnect_dry_run_refuses_to_start_a_stopped_runtime", normalize.Apply(result.Combined))
}

func testOpenRealRunKubectlDeploymentWaitLibraryExecutionModeWakesAStoppedRuntime(t *testing.T) {
	// The stub fails only the wait argv, so a fallback to the subprocess path is
	// visible. Ports are real because --no-shell needs live forwards.
	skipIfPortsBusy(t, 26100, 26133)
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	if err := os.WriteFile(envConfigPath, append(existing, []byte("stopped: true\n")...), 0o644); err != nil {
		t.Fatalf("write stopped env config: %v", err)
	}
	seedExecutionMode(t, setup, "kubectl-deployment-wait", "library")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubKubectlRunStateWithFailingWait(t, stubs, 0, 0)
	stubLsofNoHolder(t, stubs)

	var apiHits atomic.Int64
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiHits.Add(1)
		if r.URL.Path != "/apis/apps/v1/namespaces/team-dev/deployments/team-devops" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"kind":"Deployment","apiVersion":"apps/v1","metadata":{"name":"team-devops","namespace":"team-dev"},`+
			`"status":{"conditions":[{"type":"Available","status":"True"}]}}`)
	}))
	defer apiServer.Close()
	kubeDir := filepath.Join(setup.Home, ".kube")
	if err := os.MkdirAll(kubeDir, 0o700); err != nil {
		t.Fatalf("mkdir .kube: %v", err)
	}
	kubeconfig := "apiVersion: v1\n" +
		"kind: Config\n" +
		"clusters:\n" +
		"  - name: test-cluster\n" +
		"    cluster:\n" +
		"      server: " + apiServer.URL + "\n" +
		"contexts:\n" +
		"  - name: test-context\n" +
		"    context:\n" +
		"      cluster: test-cluster\n" +
		"      user: test-user\n" +
		"users:\n" +
		"  - name: test-user\n" +
		"    user:\n" +
		"      token: test-token\n" +
		"current-context: test-context\n"
	if err := os.WriteFile(filepath.Join(kubeDir, "config"), []byte(kubeconfig), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}

	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "lsof", "ps")...)
	envVars = append(envVars, openHostOSOverride)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "fell through to the kubectl subprocess") {
		t.Fatalf("wake wait used the kubectl subprocess despite library execution mode:\n%s", result.Combined)
	}
	if got := apiHits.Load(); got == 0 {
		t.Fatalf("fake API server received no requests; library path never ran")
	}
}

func testOpenReconnectDryRunReattachesARunningRuntime(t *testing.T) {
	t.Parallel()
	// A reconnect writes no config, so a stop intent recorded elsewhere survives.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlRunState(t, setup, 1, 1)
	result := erun.Run(t, []string{"open", "team", "dev", "--reconnect", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/reconnect_dry_run_reattaches_a_running_runtime", normalize.Apply(result.Combined))
}

func testOpenDryRunRunningRuntimeWakeIsQuiet(t *testing.T) {
	t.Parallel()
	// A running environment gets one read and no scale call.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlRunState(t, setup, 1, 1)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/dry_run_running_runtime_wake_is_quiet", normalize.Apply(result.Combined))
}

func testOpenDeployFlagDryRunClearsStopIntentBeforeRenderingTheChart(t *testing.T) {
	t.Parallel()
	// The stop must be cleared before helm renders, or the rollout re-applies replicas: 0.
	setup := env.New(t)
	fixture.SeedStoppedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlRunState(t, setup, 0, 0)
	// Stands in for the registry read confirming the chart is published.
	envVars = append(envVars, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	result := erun.Run(t, []string{"open", "team", "dev", "--deploy", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/deploy_flag_dry_run_clears_stop_intent_before_rendering_the_chart", normalize.Apply(result.Combined))
}

func testOpenDeployFlagDryRunFreshEnvRequiresRuntimeVersion(t *testing.T) {
	t.Parallel()
	// A fresh env with no runtimeversion must fail --deploy, not deploy nothing.
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnvNoVersion(t, setup, "team", "dev")
	result := erun.Run(t, []string{"open", "team", "dev", "--deploy", "--no-shell", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when no runtime version can be resolved, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "open/deploy_flag_dry_run_fresh_env_requires_runtime_version", normalize.Apply(result.Combined))
}

func testOpenDryRunUnaffectedByKubectlDeploymentGetLibraryExecutionMode(t *testing.T) {
	t.Parallel()
	// Library mode must not change the dry-run trace.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHD(t, setup, "team", "dev")
	seedExecutionMode(t, setup, "kubectl-deployment-get", "library")
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_HOST_OS_OVERRIDE=darwin")
	result := erun.Run(t, []string{"open", "team", "dev", "--intellij", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/intellij_dry_run", normalize.Apply(result.Combined))
}

func testOpenRealRunKubectlDeploymentGetLibraryExecutionModeReachesTheApiServerDirectly(t *testing.T) {
	// The stub fails only the get-deployment argv, so a fallback to the subprocess
	// path is visible.
	skipIfPortsBusy(t, 26100, 26133)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithPortRange(t, setup, "team", "dev", 26100)
	seedExecutionMode(t, setup, "kubectl-deployment-get", "library")

	var apiHits atomic.Int64
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiHits.Add(1)
		if r.URL.Path != "/apis/apps/v1/namespaces/team-dev/deployments/team-api" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"kind":"Deployment","apiVersion":"apps/v1","metadata":{"name":"team-api","namespace":"team-dev"}}`)
	}))
	defer apiServer.Close()
	kubeDir := filepath.Join(setup.Home, ".kube")
	if err := os.MkdirAll(kubeDir, 0o700); err != nil {
		t.Fatalf("mkdir .kube: %v", err)
	}
	kubeconfig := "apiVersion: v1\n" +
		"kind: Config\n" +
		"clusters:\n" +
		"  - name: test-cluster\n" +
		"    cluster:\n" +
		"      server: " + apiServer.URL + "\n" +
		"contexts:\n" +
		"  - name: test-context\n" +
		"    context:\n" +
		"      cluster: test-cluster\n" +
		"      user: test-user\n" +
		"users:\n" +
		"  - name: test-user\n" +
		"    user:\n" +
		"      token: test-token\n" +
		"current-context: test-context\n"
	if err := os.WriteFile(filepath.Join(kubeDir, "config"), []byte(kubeconfig), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}

	stubsDir := filepath.Join(setup.Cwd, "stubs")
	envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
		DeploymentName:       "team-devops",
		ContainerName:        "team-devops",
		RepoPath:             "/home/erun/git/team",
		MCPPort:              26100,
		SSHPort:              26122,
		ExecExitCodes:        []int{0},
		FailingArgvSubstring: "get deployment team-api -o name",
	})...)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "fell through to the kubectl subprocess") {
		t.Fatalf("API deployment check used the kubectl subprocess despite library execution mode:\n%s", result.Combined)
	}
	if got := apiHits.Load(); got == 0 {
		t.Fatalf("fake API server received no requests; library path never ran")
	}
}

func testOpenRealRunKubectlDeploymentWaitLibraryExecutionModeReachesTheApiServerDirectly(t *testing.T) {
	// The stub fails only the wait argv, so a fallback to the subprocess path is visible.
	skipIfPortsBusy(t, 26100, 26133)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithPortRange(t, setup, "team", "dev", 26100)
	seedExecutionMode(t, setup, "kubectl-deployment-wait", "library")

	var apiHits atomic.Int64
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiHits.Add(1)
		if r.URL.Path != "/apis/apps/v1/namespaces/team-dev/deployments/team-devops" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"kind":"Deployment","apiVersion":"apps/v1","metadata":{"name":"team-devops","namespace":"team-dev"},`+
			`"status":{"conditions":[{"type":"Available","status":"True"}]}}`)
	}))
	defer apiServer.Close()
	kubeDir := filepath.Join(setup.Home, ".kube")
	if err := os.MkdirAll(kubeDir, 0o700); err != nil {
		t.Fatalf("mkdir .kube: %v", err)
	}
	kubeconfig := "apiVersion: v1\n" +
		"kind: Config\n" +
		"clusters:\n" +
		"  - name: test-cluster\n" +
		"    cluster:\n" +
		"      server: " + apiServer.URL + "\n" +
		"contexts:\n" +
		"  - name: test-context\n" +
		"    context:\n" +
		"      cluster: test-cluster\n" +
		"      user: test-user\n" +
		"users:\n" +
		"  - name: test-user\n" +
		"    user:\n" +
		"      token: test-token\n" +
		"current-context: test-context\n"
	if err := os.WriteFile(filepath.Join(kubeDir, "config"), []byte(kubeconfig), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}

	stubsDir := filepath.Join(setup.Cwd, "stubs")
	envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
		DeploymentName:       "team-devops",
		ContainerName:        "team-devops",
		RepoPath:             "/home/erun/git/team",
		MCPPort:              26100,
		SSHPort:              26122,
		ExecExitCodes:        []int{0},
		FailingArgvSubstring: "wait --for=condition=Available",
	})...)
	// WaitForShellDeployment only runs ahead of the interactive shell.
	envVars = append(envVars, "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"open", "team", "dev", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "fell through to the kubectl subprocess") {
		t.Fatalf("deployment wait used the kubectl subprocess despite library execution mode:\n%s", result.Combined)
	}
	if got := apiHits.Load(); got == 0 {
		t.Fatalf("fake API server received no requests; library path never ran")
	}
}
