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
	// team-devops is RuntimeReleaseName("team").
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
	// The guard is scoped to the runtime chart; components carry no environment shape.
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
	// Naming the chart makes the selection deliberate, not its projection-resolved values correct.
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
	// In-pod values come from the config projection and would reshape the env. The
	// harness sets ERUN_TENANT/ERUN_ENVIRONMENT to stand in for running in the pod.
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
	// The projection's sshd, port and sizing values are wrong for a remote-agent env too.
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
	// The projection has no deploy block, so an empty selection here is unknown,
	// not a choice.
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
	// A deliberate selection still resolves values from the pod's projection.
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
	// A saved selection resolves, so the runtime-chart guard refuses, not the empty-selection one.
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
