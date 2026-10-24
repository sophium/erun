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
	// Re-init with a changed --kubernetes-context: the new context is
	// persisted and its namespace ensured, the cloud-provider alias stays
	// empty (no matching provider), and the unchanged registry is left
	// alone. The kubectl stub reports the namespace NotFound so the create
	// branch runs. The persisted env config must carry the new context.
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
	// Re-init of an existing remote env updates it in place: repopath
	// converges on the in-pod worktree convention path, the runtime version
	// moves to the new --version, and the pod limits are recorded — without
	// recreating the env. The kubectl stub reports the clone already present,
	// so the flow pulls instead of prompting for a URL.
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
	// --dind-cpu/--dind-memory on a re-init update the sidecar's own
	// recorded resources without touching the runtime pod's separately
	// recorded --runtime-cpu/--runtime-memory from an earlier init.
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
	// The way out of the bootstrap deadlock: runtimeregistry is the only field
	// that redirects chart resolution, and every other writer of it is a side
	// effect of a deploy that already succeeded. --runtime-registry writes it
	// before this run's own deploy resolves a chart, so the recovery is one
	// command rather than a hand-edit of erun's config store. The probe seam
	// publishes the platform chart only in the redirected registry, so the
	// resolved chart reference is the proof the redirect took effect.
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
	// A supplied setting lands on the env for having been supplied. The
	// invocation names no --type, so it resolves to the local-agent default,
	// and the setting still reaches an env stored as remote-agent — accepting
	// the flag and dropping it is the failure this pins. The env keeps its
	// type: a flag nobody passed must not retype anything.
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
	// erun init gained --components so a saved deploy default can be
	// set without hand-editing the env's config.yaml.
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
	// The way back: an env stuck on a saved selection that shadows the
	// repo's k8s.deployments plan had no command to return to the plan short
	// of hand-editing erun's config store. `--components ''` is that command:
	// it clears the saved set outright rather than being read as "nothing
	// passed, leave it alone".
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
	// The documented escape from a chart-resolution deadlock, run the way an
	// operator reaches for it: --runtime-registry alone. It has to land on an
	// existing env whatever type that env is, and the runtime env has to still
	// be a runtime env afterwards — the default an omitted --type resolves to
	// is local-agent, and acting on it would silently retype the env.
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
	// Both env types keep their worktree off the laptop, so the old reconcile
	// could never move between them and a runtime env could never become
	// orchestratable. Named explicitly, the type moves, and the run does the
	// remote-agent work — the runtime deploy and the checkout — a fresh
	// --type=remote-agent init would. The kubectl stub reports the clone
	// already present so the flow pulls instead of prompting for a URL.
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
	// The other direction, which needs more than the type field: a local-agent
	// worktree is hostPath-mounted, and the path a remote env carries names an
	// in-pod directory. The retype adopts the host project root supplied with
	// it, so the env it writes is one that can actually mount.
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
	// The refusal that keeps the flag honest: the run is not in a git repo and
	// names no --project-root, so there is no host path to mount. Writing the
	// type anyway would move the failure to a later deploy, and reporting
	// success would drop the flag. It refuses, naming what to supply.
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
	// The audit line for a re-init: every setting the run decided about shows
	// up, whether it was supplied or deliberately left as found. The pod
	// resources are the ones that matter most — they used to be reset to the
	// defaults by any re-init that reached the reconcile at all, so an operator
	// adding one flag lost limits they never mentioned.
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
	// A dry run traces the write and performs none of it: the limits it said it
	// would keep are untouched, and what it said it would set never landed.
	assertEnvConfigContains(t, setup, "team", "dev", "cpu: \"8\"", "memory: 16Gi")
	assertEnvConfigLacks(t, setup, "team", "dev", "imagepullsecrets", "runtimeimage")
}

func testInitReinitDryRunSetsDindResourcesIndependentOfRuntimePod(t *testing.T) {
	// --dind-cpu/--dind-memory on a re-init trace as set while an
	// untouched runtimepod (from an earlier init) traces as kept —
	// applyEnvRuntimeDindPod and applyEnvRuntimePod act independently.
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
