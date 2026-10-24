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
	// --deploy is the operator-convenience shortcut: open
	// deploys the runtime before opening. The kubectl NotFound stub is the
	// deployment-check decision input so the resolver picks the deploy
	// branch and traces the full published-chart deploy plan, then the
	// shell preview. This locks the deploy-decision path (maybeDeployRuntime
	// → shouldDeployRuntime → checkKubernetesDeployment → deployRuntime)
	// that bare pure `open` no longer exercises.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlNotFound(t, setup)
	// The runtime chart ladder must confirm erun-devops published at the
	// version before installing it; the seam stands in for that registry read.
	envVars = append(envVars, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	result := erun.Run(t, []string{"open", "team", "dev", "--deploy", "--no-shell", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/deploy_flag_dry_run_deploys_runtime_before_opening", normalize.Apply(result.Combined))
}

func testOpenDryRunWakesStoppedRuntimeBeforeForwarding(t *testing.T) {
	t.Parallel()
	// The load-bearing wake: kubectl port-forward cannot attach to a
	// Deployment with zero replicas, so a stopped environment must be scaled
	// back up and waited for BEFORE the forwards are traced. The run-state
	// stub is the decision input — dry-run cannot know the replica count
	// without asking the cluster.
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
	// Locks the dry-run/audit contract for kubectl-deployment-wait: the
	// wake plan must stay byte-identical to the subprocess-mode golden
	// (dry_run_wakes_stopped_runtime_before_forwarding) even with
	// execution.modes.kubectl-deployment-wait=library, since
	// WaitRuntimeAvailable only ever renders the trace line and never
	// calls libraryWaitForDeploymentAvailable on a dry run.
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
	// The other half of the wake contract. A supervisor respawning `open` to
	// re-establish a dropped session is not the operator opening the
	// environment — and a stop is exactly what drops every session — so the
	// reattach must fail rather than scale the runtime back up. The golden
	// shows no scale command and no `stopped=false` config write.
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
	// Proves the wake path's dispatch (WaitRuntimeAvailable, stop.go)
	// engages the library path too, not just open.go's/doctor.go's: it
	// bypasses RunRawCommand entirely in library mode rather than
	// sharing its subprocess plumbing, so it needs its own real-run
	// proof rather than inheriting the other two call sites' coverage.
	// The kubectl stub fails loudly and distinctively only for the
	// "wait --for=condition=Available" argv shape (the run-state read
	// and the scale call still succeed), so a fallback to the
	// subprocess path surfaces as a recognizable failure instead of
	// silently passing.
	//
	// Pinned to the 26100 range (not the default, and not t.Parallel(),
	// per the port-bound-scenario rules) because the stub now runs a
	// real port-forward simulator for the post-wake MCP/API forwards:
	// open --no-shell has no shell to fall back on, so those forwards
	// must actually answer or the run fails.
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
	// A reconnect against a running environment is the common case and must
	// stay a normal open: forwards rebound, no scale, and — the part that
	// matters — no config write, so an intent recorded from elsewhere is not
	// retired by a background reattach.
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
	// The common path must stay silent: an environment already running gets
	// one run-state read and no scale call, so `open` does not churn the
	// cluster on every invocation. The negative is the point of the golden.
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
	// stop → deploy → open, third leg. The recorded stop must be cleared
	// BEFORE helm renders, or the rollout would re-apply replicas: 0 and the
	// wake would have to undo its own deploy. The golden therefore shows the
	// `stopped=false` config write ahead of the helm plan, and no
	// `--set stopped=true` in the rendered helm args.
	setup := env.New(t)
	fixture.SeedStoppedTenantEnv(t, setup, "team", "dev")
	envVars := stubKubectlRunState(t, setup, 0, 0)
	// The runtime chart ladder must confirm erun-devops published at the
	// version before installing it; the seam stands in for that registry read.
	envVars = append(envVars, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	result := erun.Run(t, []string{"open", "team", "dev", "--deploy", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/deploy_flag_dry_run_clears_stop_intent_before_rendering_the_chart", normalize.Apply(result.Combined))
}

func testOpenDeployFlagDryRunFreshEnvRequiresRuntimeVersion(t *testing.T) {
	t.Parallel()
	// The fresh-env coverage gap that hid the regression: an env with
	// no persisted runtimeversion and no local/published chart. With
	// --deploy, the published-chart resolver bails with the "runtime
	// version is required" decision trace + error instead of silently
	// deploying nothing. The desktop must avoid this by composing deploy at
	// a built version on create; this scenario locks the decision so the
	// fresh-env path cannot regress unnoticed. (Bare pure `open` would not
	// deploy at all — the version is only required on the --deploy path.)
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
	// Locks the dry-run/audit contract for kubectl-deployment-get: the
	// plan must stay byte-identical to the subprocess-mode golden
	// (intellij_dry_run, which already exercises the API-deployment
	// presence trace) even with
	// execution.modes.kubectl-deployment-get=library, since
	// ensureAPIPortForward only ever renders the trace line
	// (common.KubectlGetDeploymentArgs) and never calls
	// common.DeploymentPresent on a dry run.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHD(t, setup, "team", "dev")
	seedExecutionMode(t, setup, "kubectl-deployment-get", "library")
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_HOST_OS_OVERRIDE=darwin")
	result := erun.Run(t, []string{"open", "team", "dev", "--intellij", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/intellij_dry_run", normalize.Apply(result.Combined))
}

func testOpenRealRunKubectlDeploymentGetLibraryExecutionModeReachesTheApiServerDirectly(t *testing.T) {
	// Proves the library path produces the same observable result as the
	// subprocess path (the team-api deployment is present, so the API
	// port-forward proceeds) via a real client-go call against a fake API
	// server instead of shelling out to kubectl. The kubectl stub fails
	// loudly and distinctively only for the "get deployment team-api -o
	// name" argv shape (every other kubectl subcommand this open needs --
	// the runtime deployment checks, the wake wait, the port-forward
	// simulator -- still succeeds), so a fallback to the subprocess path
	// for the API-deployment check surfaces as a recognizable failure
	// instead of silently passing.
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
	// Proves the library path produces the same observable result as the
	// subprocess path (the runtime deployment reports Available, so the
	// shell exec proceeds) via a real client-go call against a fake API
	// server instead of shelling out to kubectl. The kubectl stub fails
	// loudly and distinctively only for the "wait --for=condition=Available"
	// argv shape (every other kubectl subcommand this open needs -- the
	// deployment presence check, the port-forward simulator, the
	// interactive exec -- still succeeds), so a fallback to the
	// subprocess path for the wait surfaces as a recognizable failure
	// instead of silently passing.
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
	// ERUN_FORCE_TTY=1: WaitForShellDeployment only runs ahead of the
	// interactive shell exec (runOpenShellWait); this harness's stdin is
	// otherwise non-TTY, which takes the no-shell branch instead and
	// never calls it at all.
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
