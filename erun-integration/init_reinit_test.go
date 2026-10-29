package integration

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func initReinitScenarios(t *testing.T) {
	t.Run("reinit_real_run_updates_kubernetes_context", testInitReinitRealRunUpdatesKubernetesContext)
	t.Run("reinit_remote_real_run_updates_runtime_settings", testInitReinitRemoteRealRunUpdatesRuntimeSettings)
	t.Run("reinit_remote_real_run_updates_dind_resources_independent_of_runtime_pod", testInitReinitRemoteRealRunUpdatesDindResourcesIndependentOfRuntimePod)
	t.Run("reinit_remote_real_run_redirects_the_runtime_registry", testInitReinitRemoteRealRunRedirectsTheRuntimeRegistry)
	t.Run("reinit_records_an_image_pull_secret_without_restating_the_type", testInitReinitRecordsAnImagePullSecretWithoutRestatingTheType)
	t.Run("reinit_sets_saved_deploy_components", testInitReinitSetsSavedDeployComponents)
	t.Run("reinit_clears_saved_deploy_components_returns_env_to_plan", testInitReinitClearsSavedDeployComponentsReturnsEnvToPlan)
	t.Run("reinit_runtime_env_records_a_registry_and_keeps_its_type", testInitReinitRuntimeEnvRecordsARegistryAndKeepsItsType)
	t.Run("reinit_retypes_a_runtime_env_to_remote_agent", testInitReinitRetypesARuntimeEnvToRemoteAgent)
	t.Run("reinit_retypes_a_remote_env_to_local_agent", testInitReinitRetypesARemoteEnvToLocalAgent)
	t.Run("reinit_refuses_a_local_agent_retype_with_no_host_repo_path", testInitReinitRefusesALocalAgentRetypeWithNoHostRepoPath)
	t.Run("reinit_dry_run_traces_what_it_keeps_and_what_it_changes", testInitReinitDryRunTracesWhatItKeepsAndWhatItChanges)
	t.Run("reinit_dry_run_sets_dind_resources_independent_of_runtime_pod", testInitReinitDryRunSetsDindResourcesIndependentOfRuntimePod)
}

func testInitReinitRealRunUpdatesKubernetesContext(t *testing.T) {
	// The kubectl stub reports the namespace NotFound so the create branch runs.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "kubectl", strings.Join([]string{
		`case "$*" in`,
		`  *"get namespace"*)`,
		`    printf '%s\n' 'Error from server (NotFound): namespaces "team-dev" not found' >&2`,
		`    exit 1 ;;`,
		`  *) : ;;`,
		`esac`,
		`exit 0`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	args := []string{
		"init", "team", "dev",
		"--kubernetes-context", "new-context",
		"--container-registry", "registry.example/test",
		"--set-default-tenant=true",
		"--confirm-environment=true",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "init/reinit_real_run_updates_kubernetes_context", normalize.Apply(result.Combined))
	assertEnvConfigContains(t, setup, "team", "dev", "kubernetescontext: new-context")
}

func testInitReinitRemoteRealRunUpdatesRuntimeSettings(t *testing.T) {
	// The kubectl stub reports the clone present, so the flow pulls instead of prompting.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{RepoExists: true})
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "git", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
	envVars = append(envVars, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:2.0.0")
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "2.0.0",
		"--runtime-cpu", "8",
		"--runtime-memory", "16Gi",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--set-default-tenant=true",
		"--confirm-environment=true",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "init/reinit_remote_real_run_updates_runtime_settings", normalize.Apply(result.Combined))
	assertEnvConfigContains(t, setup, "team", "dev", "runtimeversion: 2.0.0", "cpu: \"8\"", "memory: 16Gi")
}

func testInitReinitRemoteRealRunUpdatesDindResourcesIndependentOfRuntimePod(t *testing.T) {
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	appendEnvConfig(t, setup, "team", "dev", "runtimepod:\n  cpu: \"8\"\n  memory: 16Gi\n")
	stubs := setup.Cwd + "/stubs"
	stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{RepoExists: true})
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "git", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
	envVars = append(envVars, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:2.0.0")
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "2.0.0",
		"--dind-cpu", "6",
		"--dind-memory", "16Gi",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--set-default-tenant=true",
		"--confirm-environment=true",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "init/reinit_remote_real_run_updates_dind_resources_independent_of_runtime_pod", normalize.Apply(result.Combined))
	assertEnvConfigContains(t, setup, "team", "dev", "runtimeversion: 2.0.0", "runtimedindpod:\n    cpu: \"6\"\n    memory: 16Gi", "runtimepod:\n    cpu: \"8\"\n    memory: 16Gi")
}

func testInitReinitRemoteRealRunRedirectsTheRuntimeRegistry(t *testing.T) {
	// --runtime-registry is written before this run's deploy resolves a chart; the probe publishes
	// the chart only in the redirected registry.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{RepoExists: true})
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "git", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
	envVars = append(envVars, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=ghcr.io/sophium/erun-devops:1.0.0")
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "1.0.0",
		"--runtime-registry", "ghcr.io/sophium",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--set-default-tenant=true",
		"--confirm-environment=true",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "init/reinit_remote_real_run_redirects_the_runtime_registry", normalize.Apply(result.Combined))
	assertEnvConfigContains(t, setup, "team", "dev", "runtimeregistry: ghcr.io/sophium")
}

func testInitReinitRecordsAnImagePullSecretWithoutRestatingTheType(t *testing.T) {
	// No --type resolves to local-agent, which must neither drop the setting nor retype the env.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	args := []string{
		"init", "team", "dev",
		"--image-pull-secret", "ecr-pull",
		"--set-default-tenant=true",
		"--confirm-environment=true",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "init/reinit_records_an_image_pull_secret_without_restating_the_type", normalize.Apply(result.Combined))
	assertEnvConfigContains(t, setup, "team", "dev", "imagepullsecrets:", "- ecr-pull", "type: remote-agent", "runtimeversion: 1.0.0")
}

func testInitReinitSetsSavedDeployComponents(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	args := []string{
		"init", "team", "dev",
		"--components", "erun-backend-api,erun-backend-db",
		"--set-default-tenant=true",
		"--confirm-environment=true",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "init/reinit_sets_saved_deploy_components", normalize.Apply(result.Combined))
	assertEnvConfigContains(t, setup, "team", "dev", "components:", "- erun-backend-api", "- erun-backend-db")
}

func testInitReinitClearsSavedDeployComponentsReturnsEnvToPlan(t *testing.T) {
	// An empty --components clears the saved set rather than meaning "leave it alone".
	setup := env.New(t)
	fixture.SeedTenantEnvWithDeployComponents(t, setup, "team", "dev", []string{"erun-backend-postgres"})
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	args := []string{
		"init", "team", "dev",
		"--components", "",
		"--set-default-tenant=true",
		"--confirm-environment=true",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "init/reinit_clears_saved_deploy_components_returns_env_to_plan", normalize.Apply(result.Combined))
	assertEnvConfigLacks(t, setup, "team", "dev", "erun-backend-postgres", "components:")
}

func testInitReinitRuntimeEnvRecordsARegistryAndKeepsItsType(t *testing.T) {
	// The omitted --type default (local-agent) must not retype the runtime env.
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	args := []string{
		"init", "team", "dev",
		"--runtime-registry", "ghcr.io/sophium",
		"--set-default-tenant=true",
		"--confirm-environment=true",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "init/reinit_runtime_env_records_a_registry_and_keeps_its_type", normalize.Apply(result.Combined))
	assertEnvConfigContains(t, setup, "team", "dev", "runtimeregistry: ghcr.io/sophium", "type: runtime")
}

func testInitReinitRetypesARuntimeEnvToRemoteAgent(t *testing.T) {
	// The kubectl stub reports the clone present, so the flow pulls instead of prompting.
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{RepoExists: true})
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "git", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
	envVars = append(envVars, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	args := []string{
		"init", "team", "dev",
		"--type", "remote-agent",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--set-default-tenant=true",
		"--confirm-environment=true",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "init/reinit_retypes_a_runtime_env_to_remote_agent", normalize.Apply(result.Combined))
	assertEnvConfigContains(t, setup, "team", "dev", "type: remote-agent", "runtimeversion: 1.0.0")
}

func testInitReinitRetypesARemoteEnvToLocalAgent(t *testing.T) {
	// A local-agent worktree is hostPath-mounted, so the retype adopts the host project root.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	args := []string{
		"init", "team", "dev",
		"--type", "local-agent",
		"--project-root", setup.Cwd,
		"--set-default-tenant=true",
		"--confirm-environment=true",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "init/reinit_retypes_a_remote_env_to_local_agent", normalize.Apply(result.Combined))
	assertEnvConfigContains(t, setup, "team", "dev", "type: local-agent", "localrepopath: "+setup.Cwd)
}

func testInitReinitRefusesALocalAgentRetypeWithNoHostRepoPath(t *testing.T) {
	// With no git repo and no --project-root there is no host path to mount.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	args := []string{
		"init", "team", "dev",
		"--type", "local-agent",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "init/reinit_refuses_a_local_agent_retype_with_no_host_repo_path", normalize.Apply(result.Combined))
	assertEnvConfigContains(t, setup, "team", "dev", "type: remote-agent")
}

func testInitReinitDryRunTracesWhatItKeepsAndWhatItChanges(t *testing.T) {
	// Pod resources not mentioned must be traced as kept, not reset to defaults.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	appendEnvConfig(t, setup, "team", "dev", "runtimepod:\n  cpu: \"8\"\n  memory: 16Gi\n")
	args := []string{
		"init", "team", "dev",
		"--runtime-image", "registry.example/team-devops",
		"--image-pull-secret", "ecr-pull",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "init/reinit_dry_run_traces_what_it_keeps_and_what_it_changes", normalize.Apply(result.Combined))
	assertEnvConfigContains(t, setup, "team", "dev", "cpu: \"8\"", "memory: 16Gi")
	assertEnvConfigLacks(t, setup, "team", "dev", "imagepullsecrets", "runtimeimage")
}

func testInitReinitDryRunSetsDindResourcesIndependentOfRuntimePod(t *testing.T) {
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	appendEnvConfig(t, setup, "team", "dev", "runtimepod:\n  cpu: \"8\"\n  memory: 16Gi\n")
	args := []string{
		"init", "team", "dev",
		"--dind-cpu", "6",
		"--dind-memory", "16Gi",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "init/reinit_dry_run_sets_dind_resources_independent_of_runtime_pod", normalize.Apply(result.Combined))
	assertEnvConfigContains(t, setup, "team", "dev", "cpu: \"8\"", "memory: 16Gi")
	assertEnvConfigLacks(t, setup, "team", "dev", "runtimedindpod")
}
