package integration

import (
	"reflect"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func initTypeScenarios(t *testing.T) {
	t.Run("type_local_agent_dry_run", testInitTypeLocalAgentDryRun)
	t.Run("type_remote_agent_dry_run", testInitTypeRemoteAgentDryRun)
	t.Run("type_runtime_dry_run", testInitTypeRuntimeDryRun)
	t.Run("type_host_dry_run", testInitTypeHostDryRun)
	t.Run("type_host_real_run_persists_config", testInitTypeHostRealRunPersistsConfig)
	t.Run("reinit_retypes_a_local_agent_env_to_host", testInitReinitRetypesALocalAgentEnvToHost)
	t.Run("reinit_refuses_a_host_retype_with_no_host_repo_path", testInitReinitRefusesAHostRetypeWithNoHostRepoPath)
	t.Run("type_conflicts_with_remote", testInitTypeConflictsWithRemote)
	t.Run("type_rejects_invalid_value", testInitTypeRejectsInvalidValue)
}

func testInitTypeLocalAgentDryRun(t *testing.T) {
	// --type local-agent is the explicit form of the no-flag default: it
	// creates the env without any remote-init work.
	setup := env.New(t)
	args := []string{
		"init", "team", "local",
		"--type", "local-agent",
		"--project-root", setup.Cwd,
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "init/type_local_agent_dry_run", normalize.Apply(result.Combined))
}

func testInitTypeRemoteAgentDryRun(t *testing.T) {
	// --type remote-agent is the canonical replacement for --remote and must
	// produce the same plan as remote_dry_run.
	setup := env.New(t)
	args := []string{
		"init", "team", "dev",
		"--type", "remote-agent",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--no-git",
		"--bootstrap",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "init/type_remote_agent_dry_run", normalize.Apply(result.Combined))
}

func testInitTypeRuntimeDryRun(t *testing.T) {
	// --type runtime feeds downstream chart wiring (worktreeStorage=none);
	// because runtime envs live in-cluster, init still walks the remote
	// namespace setup path.
	setup := env.New(t)
	args := []string{
		"init", "team", "prod",
		"--type", "runtime",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--no-git",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "init/type_runtime_dry_run", normalize.Apply(result.Combined))
}

func testInitTypeHostDryRun(t *testing.T) {
	// A host env has no pod and no cluster at all, so init never asks for a
	// kubernetes context or a container registry — unlike local-agent and
	// remote-agent above, which both require one of those flags here or an
	// interaction prompt fires. Leaving both off and still getting a clean
	// dry run is a direct behavioral proof of no cluster contact.
	setup := env.New(t)
	args := []string{
		"init", "team", "work",
		"--type", "host",
		"--project-root", setup.Cwd,
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "init/type_host_dry_run", normalize.Apply(result.Combined))
}

func testInitTypeHostRealRunPersistsConfig(t *testing.T) {
	// The real run persists exactly a name, type, and local repo path — no
	// kubernetescontext, containerregistry, or runtimeversion key, since
	// none of those apply to a type with no pod.
	setup := env.New(t)
	args := []string{
		"init", "team", "work",
		"--type", "host",
		"--project-root", setup.Cwd,
		"--set-default-tenant=true",
		"--confirm-environment=true",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "init/type_host_real_run_persists_config", normalize.Apply(result.Combined))
	// KubernetesContext has no yaml `omitempty`, so it always serializes;
	// asserting it is empty is the honest form of "unset" for this field.
	assertEnvConfigContains(t, setup, "team", "work", "type: host", "localrepopath: "+setup.Cwd, `kubernetescontext: ""`)
	assertEnvConfigLacks(t, setup, "team", "work", "containerregistry", "runtimeversion")
}

func testInitReinitRetypesALocalAgentEnvToHost(t *testing.T) {
	// Retyping to host drops the env's pod-oriented fields' relevance but
	// keeps the same local repo path — it is, after all, the same directory
	// on the same machine, just with no pod mounting it anymore.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	args := []string{
		"init", "team", "dev",
		"--type", "host",
		"--project-root", setup.Cwd,
		"--set-default-tenant=true",
		"--confirm-environment=true",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "init/reinit_retypes_a_local_agent_env_to_host", normalize.Apply(result.Combined))
	assertEnvConfigContains(t, setup, "team", "dev", "type: host", "localrepopath: "+setup.Cwd)
}

func testInitReinitRefusesAHostRetypeWithNoHostRepoPath(t *testing.T) {
	// The same refusal local-agent gets: retyping to host needs a host
	// directory, and this run names neither a git repo cwd nor
	// --project-root, so there is nothing to record.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	args := []string{
		"init", "team", "dev",
		"--type", "host",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "init/reinit_refuses_a_host_retype_with_no_host_repo_path", normalize.Apply(result.Combined))
	assertEnvConfigContains(t, setup, "team", "dev", "type: remote-agent")
}

func testInitTypeConflictsWithRemote(t *testing.T) {
	// A --type that disagrees with --remote must error before any side
	// effect, so the user never ends up with a half-configured env.
	setup := env.New(t)
	args := []string{
		"init", "team", "local",
		"--type", "local-agent",
		"--remote",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "init/type_conflicts_with_remote", normalize.Apply(result.Combined))
}

func testInitTypeRejectsInvalidValue(t *testing.T) {
	setup := env.New(t)
	args := []string{
		"init", "team", "dev",
		"--type", "invalid",
		"--dry-run",
	}
	before := snapshotConfigTree(t, setup.ConfigHome)
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "init/type_rejects_invalid_value", normalize.Apply(result.Combined))
	// A validation failure before tenant/environment resolution must leave
	// no directory behind, dry-run or not.
	after := snapshotConfigTree(t, setup.ConfigHome)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("invalid --type mutated the config directory\nbefore: %v\nafter:  %v", before, after)
	}
}
