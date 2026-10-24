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
	// Real-run deploy where the installed Deployment's immutable
	// spec.selector differs from the chart's: helm aborts the first
	// upgrade with "field is immutable". DeployHelmChart classifies that as
	// a HelmImmutableSelectorError and RunHelmDeploy recovers — deleting the
	// named Deployment (its PVCs are separate objects and survive) and
	// retrying the upgrade, which the fail-first helm stub lets succeed.
	// This branch is reachable only in real-run (the recovery fires on a
	// helm side-effect failure, not a pre-action decision), so it is a
	// stub-driven non-dry-run scenario. It locks the recover+retry trace.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinaryFailFirstThenSucceed(t, stubs, "helm",
		`Error: UPGRADE FAILED: cannot patch "team-devops" with kind Deployment: Deployment.apps "team-devops" is invalid: spec.selector: Invalid value: {"matchLabels":{"app":"erun-devops"}}: field is immutable`, 1)
	fixture.StubBinary(t, stubs, "docker", "")
	// The ERUN_PUBLISHED_CHART_PROBE_OVERRIDE seam marks erun-devops published
	// so the runtime chart ladder confirms it instead of refusing.
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	envVars = append(envVars, fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/real_run_recreates_deployment_on_immutable_selector_change", normalize.Apply(result.Combined))
}

func testDeployProjectK8sPlanGroupsParallelStep(t *testing.T) {
	// When .erun/config.yaml declares a k8s.deployments plan with a
	// parallel-group step (a list as the item), deploy must group those
	// charts into one step and emit a single "step N (parallel): ..."
	// trace line. Other steps stay serial. Order across steps matches
	// the config, not the alphabetical chart-discovery order. The runtime
	// (team-devops) is selected explicitly so the parallel step's two members
	// both deploy (opt-in-only: an unselected runtime would not).
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
	// Drive the non-dry-run helm/kubectl runners via stub binaries so
	// the deploy execution path (deploy.go's run* helpers, post-helm
	// kubectl waits, helm-recovery branches) gets covered.
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
	// A report claimed the "==> Deploy failed tenant/env: reason" header
	// (activityDeployFailedLineRe's contract in the desktop) went through
	// ctx.Trace and was invisible below -vv, making the parser dead code.
	// That does not hold against this codebase: common.Context.Trace
	// has aliased Logger.Info (visible at default verbosity; only
	// TraceCommand's raw argv is gated to -vv) since the primitives split in
	// #559, well before this issue was filed. This scenario locks that
	// contract in — the header must appear at default verbosity (no
	// --dry-run, no -v/-vv) on a real helm failure — as a regression guard,
	// not a fix. A whole-output golden is deliberately not used here: the
	// helm stub's stderr capture through cmd.Run()'s buffer is not always
	// flushed by the time the process exits, so the detail after "reason:"
	// is not reliably reproducible; the header itself is.
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
	// Exercises wrapHelmDeployWithReleaseRecovery + the production
	// helm-recovery path: a stubbed `helm` exits with the pending
	// upgrade error message on the first invocation, succeeds on
	// the retry, and exits 0 for the recovery rollback. The new
	// ERUN_AUTO_RECOVER_HELM=1 env var bypasses the interactive
	// confirmation prompt so this can run from the harness.
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
	// The reported silent no-op: a deploy against a release an earlier
	// failed rollout left locked in pending-upgrade exited without
	// complaint and replaced no pods. helm does fail on that state, but the
	// recovery prompt is the only thing the deploy did with the failure,
	// and a caller with no terminal -- the harness's own stdin, an
	// orchestrator, a CI job, the desktop's piped shell -- reads EOF, so
	// the prompt's own EOF error replaced the diagnosis. The deploy must
	// fail with what is actually wrong and the command that clears it.
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
	// Safety net: when the resolved published runtime chart is not pullable
	// at the requested version (a registry that has since evicted the tag, or
	// a race between the probe and the pull), helm upgrade exits non-zero with
	// a registry "not found" on stderr. DeployHelmChart must classify that
	// into a PublishedChartNotFoundError naming the version + registry and
	// pointing at the recovery (deploy a released version / publish first),
	// instead of surfacing a bare "exit status 1". The
	// ERUN_PUBLISHED_CHART_PROBE_OVERRIDE seam confirms erun-devops published
	// so resolution proceeds to the real pull instead of refusing up front;
	// the helm stub then fails that pull with a 404-shaped message, so the
	// failure is isolated to the chart pull, not the search that precedes it.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "docker", "")
	// helm errors land on stderr; the stub scans every arg (the real argv
	// carries --install, --wait, … before "upgrade") and fails the upgrade
	// with a registry 404 shape.
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
	// Exercises runDeployStep's real-run parallel branch (the goroutine
	// fan-out + errors.Join), which dry-run never takes because dry-run
	// always runs specs serially for stable trace ordering. The project
	// k8s plan groups the runtime chart and erun-backend-postgres into one
	// parallel step; both helm releases deploy via exit-0 stubs. The two
	// goroutines interleave their Info lines nondeterministically, so the
	// whole stream cannot be goldened — assert the deterministic
	// parallel-step trace line and count both rollouts instead.
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
	// The runtime chart names only the env, while the non-runtime
	// component names itself after a ` · ` separator so a component
	// rollout is not mistaken for a full-env redeploy. Both rollouts
	// still appear; exactly one of each pair names the component.
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
	// Regression: a runtime rollout replaces the pod, which orphans the
	// env's `kubectl port-forward` — the local socket keeps listening while
	// every request fails with EOF, so the MCP endpoint looks broken rather
	// than disconnected, and the deploy said nothing. deploy does not own
	// port-forward lifecycle (`erun open` starts and tracks them), so it
	// reports the condition and names the repair command.
	//
	// The orphan is modelled faithfully: a listener that accepts and closes
	// without answering, exactly what a forward to a deleted pod does. It
	// binds :0 so the scenario never contends for a fixed host port.
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
	// An env nobody opened on this host has no tracked forward, so there is
	// nothing to repair and the deploy stays quiet. Together with
	// real_run_reports_stale_runtime_port_forward this locks both arms; the
	// only difference between the two goldens is the warning.
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

// seedMCPPortForwardState records a tracked MCP port-forward for the env, the
// state `erun open` writes when it starts or adopts one.
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

// startOrphanedPortForwardListener binds a local port that accepts connections
// and closes them without answering — what a kubectl port-forward to a deleted
// pod does, and the condition deploy must report.
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
