package integration

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testDeployRealRunRecreatesDeploymentOnImmutableSelectorChange(t *testing.T) {
	// Real run only: the immutable-selector recovery fires on helm's failure. PVCs
	// survive the Deployment delete.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinaryFailFirstThenSucceed(t, stubs, "helm",
		`Error: UPGRADE FAILED: cannot patch "team-devops" with kind Deployment: Deployment.apps "team-devops" is invalid: spec.selector: Invalid value: {"matchLabels":{"app":"erun-devops"}}: field is immutable`, 1)
	fixture.StubBinary(t, stubs, "docker", "")
	// The probe override marks erun-devops published so the runtime chart ladder confirms it.
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	envVars = append(envVars, fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/real_run_recreates_deployment_on_immutable_selector_change", normalize.Apply(result.Combined))
}

func testDeployProjectK8sPlanGroupsParallelStep(t *testing.T) {
	// The runtime is selected explicitly so both parallel members deploy.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
	fixture.SeedProjectK8sConfig(t, setup, "environments:\n  dev:\n    k8s:\n      deployments:\n        - [team-devops, erun-backend-postgres]\n        - erun-backend-db\n        - erun-backend-api\n")
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "1.0.0",
		"--components", "team-devops,erun-backend-postgres,erun-backend-db,erun-backend-api",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "deploy/project_k8s_plan_groups_parallel_step", normalize.Apply(result.Combined))
}

func testDeployRealRunViaStubs(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "deploy/real_run_via_stubs", normalize.Apply(result.Combined))
}

func testDeployRealRunFailureShowsDeployFailedHeaderAtDefaultVerbosity(t *testing.T) {
	// The failure header must appear at default verbosity. No whole-output golden:
	// the helm stub's stderr is not reliably flushed before exit.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinaryAdvanced(t, stubs, "helm", fixture.StubBinarySpec{
		Stderr:   "Error: UPGRADE FAILED: post-upgrade hooks failed: timed out waiting for the condition",
		ExitCode: 1,
	})
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a failing helm upgrade, got 0:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "==> Deploy failed team/dev:") {
		t.Fatalf("expected the header at default verbosity (no -vv), got:\n%s", result.Combined)
	}
}

func testDeployRealRunHelmPendingRecoveryViaAutoRecoverEnv(t *testing.T) {
	// ERUN_AUTO_RECOVER_HELM=1 bypasses the interactive confirmation.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "docker", "")
	counter := filepath.Join(stubs, "helm-counter")
	fixture.StubBinaryWithScript(t, stubs, "helm", strings.Join([]string{
		`first_arg="$1"`,
		`if [ "$first_arg" = "upgrade" ]; then`,
		`  count=0`,
		`  if [ -f '` + counter + `' ]; then count=$(cat '` + counter + `'); fi`,
		`  count=$((count + 1))`,
		`  printf '%s' "$count" > '` + counter + `'`,
		`  if [ "$count" = "1" ]; then`,
		`    printf '%s\n' "Error: UPGRADE FAILED: another operation (install/upgrade/rollback) is in progress" >&2`,
		`    exit 1`,
		`  fi`,
		`fi`,
		`exit 0`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	envVars = append(envVars, "ERUN_AUTO_RECOVER_HELM=1")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/real_run_helm_pending_recovery_via_auto_recover_env", normalize.Apply(result.Combined))
}

func testDeployRealRunPendingHelmLockFailsAndNamesTheRecovery(t *testing.T) {
	// Without a terminal the recovery prompt reads EOF; the deploy must still
	// report the pending-upgrade lock and the command that clears it.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "docker", "")
	fixture.StubBinaryWithScript(t, stubs, "helm", strings.Join([]string{
		`if [ "$1" = "upgrade" ]; then`,
		`  printf '%s\n' "Error: UPGRADE FAILED: another operation (install/upgrade/rollback) is in progress" >&2`,
		`  exit 1`,
		`fi`,
		`exit 0`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("a deploy that cannot proceed must fail, got 0: %s", result.Combined)
	}
	for _, want := range []string{
		"helm release recovery not run: stdin reached EOF before the prompt could be answered",
		`helm release "team-devops" is locked by an unfinished install/upgrade/rollback`,
		"erun doctor --clear-pending-helm team dev",
		"another operation (install/upgrade/rollback) is in progress",
	} {
		if !strings.Contains(result.Combined, want) {
			t.Fatalf("expected %q in the failed deploy's output, got:\n%s", want, result.Combined)
		}
	}
	golden.Equal(t, "deploy/real_run_pending_helm_lock_fails_and_names_the_recovery", normalize.Apply(result.Combined))
}

func testDeployRealRunPublishedChartNotFoundReportsActionableError(t *testing.T) {
	// The probe override passes resolution so the failure is isolated to the
	// chart pull, which must map to PublishedChartNotFoundError.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "docker", "")
	// The real argv carries flags before "upgrade", so the stub scans every arg.
	fixture.StubBinaryWithScript(t, stubs, "helm", strings.Join([]string{
		`for a in "$@"; do`,
		`  if [ "$a" = "upgrade" ]; then`,
		`    printf '%s\n' 'Error: failed to perform "FetchReference" on source: registry.example/test/charts/erun-devops: not found' >&2`,
		`    exit 1`,
		`  fi`,
		`done`,
		`exit 0`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	envVars = append(envVars, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when the published chart is not found, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/real_run_published_chart_not_found_reports_actionable_error", normalize.Apply(result.Combined))
}

func testDeployRealRunParallelStepDeploysChartsConcurrently(t *testing.T) {
	// Dry-run is always serial, so only a real run takes the parallel branch;
	// interleaved output cannot be goldened.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
	fixture.SeedProjectK8sConfig(t, setup, "environments:\n  dev:\n    k8s:\n      deployments:\n        - [team-devops, erun-backend-postgres]\n")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	out := normalize.Apply(result.Combined)
	if !strings.Contains(out, "deploy: step 1 (parallel): team-devops, erun-backend-postgres") {
		t.Fatalf("expected parallel-step trace line, got:\n%s", out)
	}
	// A component rollout names itself after ` · ` so it isn't mistaken for a full-env redeploy.
	if got := strings.Count(out, "==> Deploying team/dev"); got != 2 {
		t.Fatalf("expected 2 parallel ==> Deploying lines, got %d:\n%s", got, out)
	}
	if got := strings.Count(out, "==> Deploying team/dev · erun-backend-postgres"); got != 1 {
		t.Fatalf("expected the component ==> Deploying line to name the release, got %d:\n%s", got, out)
	}
	if got := strings.Count(out, "==> Deployed team/dev"); got != 2 {
		t.Fatalf("expected 2 ==> Deployed completions, got %d:\n%s", got, out)
	}
	if got := strings.Count(out, "==> Deployed team/dev · erun-backend-postgres"); got != 1 {
		t.Fatalf("expected the component ==> Deployed line to name the release, got %d:\n%s", got, out)
	}
}

func testDeployRealRunReportsStaleRuntimePortForward(t *testing.T) {
	// A runtime rollout orphans the env's port-forward; deploy reports it and names
	// the repair. The listener accepts and closes, like a forward to a deleted pod.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	port := startOrphanedPortForwardListener(t)
	seedMCPPortForwardState(t, setup, "team", "dev", port)
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/real_run_reports_stale_runtime_port_forward", normalize.Apply(result.Combined))
}

func testDeployRealRunSilentWhenEnvHasNoTrackedPortForward(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/real_run_silent_when_env_has_no_tracked_port_forward", normalize.Apply(result.Combined))
}

func seedMCPPortForwardState(t *testing.T, setup env.Setup, tenant, environment string, localPort int) {
	t.Helper()
	path := portForwardStateFile(setup, "mcp", tenant, environment)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	mustWriteFile(t, path, fmt.Sprintf(
		`{"tenant":%q,"environment":%q,"kubernetesContext":"test-context","namespace":"%s-%s","localPort":%d}`,
		tenant, environment, tenant, environment, localPort))
}

// startOrphanedPortForwardListener accepts and closes connections without answering.
func startOrphanedPortForwardListener(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %v is not a TCP address", listener.Addr())
	}
	return addr.Port
}
