package integration

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testDeployDevopsK8sDeployComponentDryRun(t *testing.T) {
	// Exercises the `devops k8s deploy COMPONENT` single-component path:
	// --version wins as the version override, no tenant/env args resolves
	// the default tenant, and the component resolves to its chart under the
	// tenant repo.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	result := erun.Run(t, []string{"devops", "k8s", "deploy", "team-devops", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/devops_k8s_deploy_component_dry_run", normalize.Apply(result.Combined))
}

func testDeployDevopsK8sDeployComponentInPodLocalAgentRuntimeRefused(t *testing.T) {
	// Regression: ResolveDeploySpec (the component-named path behind
	// `devops k8s deploy` and the MCP deploy tool's component branch) already
	// applied the runtime chart's other guards — the chart override and the
	// MCP-auth downgrade refusal — but not this one, so naming the runtime
	// release as a component reached exactly the in-pod local-agent redeploy
	// this guard exists to refuse. team-devops is RuntimeReleaseName("team").
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	envVars := append(setup.Env(), "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev")
	result := erun.Run(t, []string{"devops", "k8s", "deploy", "team-devops", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for an in-pod local-agent component-named runtime deploy:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/devops_k8s_deploy_component_in_pod_local_agent_runtime_refused", normalize.Apply(result.Combined))
}

func testDeployDevopsK8sDeployComponentInPodLocalAgentNonRuntimeAllowed(t *testing.T) {
	// The guard must stay scoped to the runtime chart alone: naming a
	// non-runtime component from inside the pod carries no environment shape,
	// so it must keep working even in-pod on a local-agent env.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
	envVars := append(setup.Env(), "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev")
	result := erun.Run(t, []string{"devops", "k8s", "deploy", "erun-backend-api", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/devops_k8s_deploy_component_in_pod_local_agent_non_runtime_allowed", normalize.Apply(result.Combined))
}

func testDeployDevopsK8sDeployComponentInPodNonLocalAgentRuntimeRefused(t *testing.T) {
	// Regression: naming the runtime release as a component from inside a
	// remote-agent env's own pod reached the same projection-resolved rollout
	// the local-agent case already refused. Naming the chart makes the
	// selection deliberate, not the values authoritative — the resolve still
	// reads the pod's projection, so it still carries the substituted sshd
	// state, port block, and runtime sizing. Mirrors
	// in_pod_remote_agent_runtime_deploy_refused for the component-named path.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	repoPath := filepath.Join(setup.Home, "git", "team")
	fixture.SeedDevopsRepoAt(t, repoPath, "team", "dev")
	envVars := append(setup.Env(), "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev")
	result := erun.Run(t, []string{"devops", "k8s", "deploy", "team-devops", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for an in-pod non-local-agent component-named runtime deploy:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/devops_k8s_deploy_component_in_pod_non_local_agent_runtime_refused", normalize.Apply(result.Combined))
}

func testDeployInPodLocalAgentRuntimeDeployRefused(t *testing.T) {
	// Regression: calling a local-agent env's own in-pod MCP deploy resolved
	// every environment-shaping value from the in-pod config projection —
	// default ports, the in-pod mount path as worktreeHostPath, default pod
	// resources, the project's deploy registry for the chart — so the rollout
	// reshaped the env and cut the channel that asked for it. The in-pod
	// marker is the chart-set ERUN_TENANT/ERUN_ENVIRONMENT pair, which the
	// harness sets here to stand in for running inside the pod.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	envVars := append(setup.Env(), "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for an in-pod local-agent runtime deploy:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/in_pod_local_agent_runtime_deploy_refused", normalize.Apply(result.Combined))
}

func testDeployInPodRemoteAgentRuntimeDeployRefused(t *testing.T) {
	// Regression: the guard keyed on the target being local-agent, so a
	// remote-agent env's own pod rolled its own runtime chart from a resolve
	// that had substituted the projection's values — sshdEnabled=false where
	// the environment runs the sshd that serves workspace-sync, and the
	// projection's port block and runtime sizing in place of the host store's.
	// Owning the worktree inside the pod makes the deploy reachable; it does
	// not make the values it resolved correct.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	envVars := append(setup.Env(), "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev", "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for an in-pod remote-agent runtime deploy:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/in_pod_remote_agent_runtime_deploy_refused", normalize.Apply(result.Combined))
}

func testDeployInPodRuntimeEnvRuntimeDeployRefused(t *testing.T) {
	// Regression: a runtime env's pod carries only the config projection the
	// chart injected, which has no deploy block — so `deploy.components` is
	// empty in there even when the env has a saved selection, the
	// runtime-chart-alone fallback is reached for lack of information rather
	// than by choice, and the deploy exited 0 having rolled one chart of the
	// nine the operator selected. The refusal names the runtime chart it would
	// have rolled alone and both remedies.
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnv(t, setup, "team", "dev")
	envVars := append(setup.Env(), "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev", "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for a blind in-pod runtime deploy:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/in_pod_runtime_env_runtime_deploy_refused", normalize.Apply(result.Combined))
}

func testDeployInPodRuntimeEnvExplicitRuntimeComponentRefused(t *testing.T) {
	// Regression: naming the runtime release used to be the empty-selection
	// refusal's own second remedy, and it carried the same
	// projection-substituted values as the inferred fallback the first remedy
	// avoids. A deliberate selection is not an authoritative one: the resolve
	// still reads the pod's projection, so the rollout still emits the
	// projection's sshd state and port block for this pod's own environment.
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnv(t, setup, "team", "dev")
	envVars := append(setup.Env(), "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev", "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--components", "team-devops", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for an in-pod runtime-env runtime-chart deploy:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/in_pod_runtime_env_explicit_runtime_component_refused", normalize.Apply(result.Combined))
}

func testDeployInPodRuntimeEnvSavedSelectionRefusedByTheRuntimeChartGuard(t *testing.T) {
	// A saved deploy.components selection resolves, so the empty-selection
	// guard stays silent — its diagnosis, that the pod cannot tell a genuinely
	// empty selection from the one saved on the host, does not apply. What
	// refuses the rollout is the runtime-chart guard one layer on, because a
	// selection that resolved still resolves the runtime chart's values from
	// the pod's projection.
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnvWithDeployComponents(t, setup, "team", "dev", []string{"team-devops"})
	envVars := append(setup.Env(), "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev", "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for an in-pod runtime-env deploy of its saved selection:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "refusing to roll the runtime chart alone") {
		t.Fatalf("a resolved saved selection must not reach the empty-selection guard:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/in_pod_runtime_env_saved_selection_refused_by_the_runtime_chart_guard", normalize.Apply(result.Combined))
}

func testDeployInPodLocalAgentComponentDeployAllowed(t *testing.T) {
	// A component chart carries no environment shape, so an in-pod
	// local-agent deploy that selects only components keeps working — the
	// guard fires on the runtime chart alone.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
	envVars := append(setup.Env(), "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--components", "erun-backend-api", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/in_pod_local_agent_component_deploy_allowed", normalize.Apply(result.Combined))
}
