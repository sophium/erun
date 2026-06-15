package integration

import (
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

func TestDelete(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"delete", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "delete/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_with_seeded_env", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"delete", "team", "dev", "--dry-run"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   setup.Env(),
			Stdin: "y\n",
		})
		golden.Equal(t, "delete/dry_run_with_seeded_env", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_with_runtime_type_traces_namespace_delete", func(t *testing.T) {
		// Exercises delete.go on a runtime-type env (explicit type=runtime
		// in YAML). RemoteWorktree() returns true via Type, so delete must
		// trace the kubectl namespace delete the same as a remote=true env.
		setup := env.New(t)
		seedExplicitTypeEnv(t, setup, "team", "prod", "runtime")
		result := erun.Run(t, []string{"delete", "team", "prod", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "delete/dry_run_with_runtime_type_traces_namespace_delete", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_with_remote_env_traces_namespace_delete", func(t *testing.T) {
		// Exercises delete.go on a remote environment: --dry-run must
		// trace the kubectl namespace delete command (with --ignore-not-found)
		// in addition to the local config rm trace, without touching the
		// cluster or prompting for confirmation.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"delete", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "delete/dry_run_with_remote_env_traces_namespace_delete", normalize.Apply(result.Combined))
	})

	t.Run("rejects_remote_env_without_kubernetes_context", func(t *testing.T) {
		// Regression: a remote env whose config lost its kubernetescontext
		// field used to silently delete the namespace from whatever
		// `kubectl config current-context` was on the host (orbstack on a
		// developer machine), because Context.EnsureKubernetesContext is
		// a no-op on empty input. The mutating call now goes through
		// RequireKubernetesContext, which errors up front.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		envCfgPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		body := "name: dev\n" +
			"repopath: " + setup.Cwd + "\n" +
			"runtimeversion: 1.0.0\n" +
			"type: remote-agent\n"
		if err := os.WriteFile(envCfgPath, []byte(body), 0o644); err != nil {
			t.Fatalf("rewrite env config without kubernetescontext: %v", err)
		}
		stubs := setup.Cwd + "/stubs"
		// kubectl stub still emits success if invoked; the test asserts
		// erun never gets there.
		fixture.StubBinary(t, stubs, "kubectl", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"delete", "team", "dev", "--yes"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when remote env has no kubernetescontext, got 0:\n%s", result.Combined)
		}
		out := normalize.Apply(result.Combined)
		if !strings.Contains(out, "kubernetes context is required") {
			t.Fatalf("expected RequireKubernetesContext error, got:\n%s", out)
		}
		// The local config tree must remain on disk: erun must not delete
		// local state when the remote-side delete cannot proceed safely.
		if _, err := os.Stat(filepath.Join(setup.ConfigHome, "erun", "team", "dev")); err != nil {
			t.Errorf("env config tree should remain on disk when delete aborts, stat err: %v", err)
		}
	})

	t.Run("real_run_confirmation_prompt_accepts_matching_input", func(t *testing.T) {
		// Exercises cmd/delete.go confirmDeleteCommand's happy path: without
		// --yes the command prompts for the literal "<tenant>-<environment>"
		// string; typing it proceeds with the delete. The env is local
		// (no remote worktree) so no kubectl is involved, and it is the
		// tenant's last env, so the tenant config and the root default
		// tenant are cleared too. The typed confirm is the run's single
		// interactive prompt (readline read-ahead).
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envDir := filepath.Join(setup.ConfigHome, "erun", "team", "dev")
		result := erun.Run(t, []string{"delete", "team", "dev"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   setup.Env(),
			Stdin: "team-dev\n",
		})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "delete/real_run_confirmation_prompt_accepts_matching_input", normalize.Apply(result.Combined))
		if _, err := os.Stat(envDir); !os.IsNotExist(err) {
			t.Errorf("expected env config tree to be removed at %s, stat err: %v", envDir, err)
		}
	})

	t.Run("real_run_confirmation_mismatch_aborts", func(t *testing.T) {
		// Exercises confirmDeleteCommand's mismatch branch: typing anything
		// other than "<tenant>-<environment>" must abort before any state
		// is touched — non-zero exit, config tree intact.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envDir := filepath.Join(setup.ConfigHome, "erun", "team", "dev")
		result := erun.Run(t, []string{"delete", "team", "dev"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   setup.Env(),
			Stdin: "team-prod\n",
		})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit on confirmation mismatch, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "delete/real_run_confirmation_mismatch_aborts", normalize.Apply(result.Combined))
		if _, err := os.Stat(envDir); err != nil {
			t.Errorf("env config tree must remain on disk after aborted delete, stat err: %v", err)
		}
	})

	t.Run("real_run_default_env_reassigned_when_other_envs_remain", func(t *testing.T) {
		// Exercises erun-common/delete.go clearDeletedDefaultEnvironment:
		// deleting the tenant's default environment while another env
		// remains must keep the tenant and promote the next remaining env
		// to default instead of leaving a dangling reference.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stagingDir := filepath.Join(setup.ConfigHome, "erun", "team", "staging")
		if err := os.MkdirAll(stagingDir, 0o755); err != nil {
			t.Fatalf("mkdir staging env: %v", err)
		}
		mustWrite(t, filepath.Join(stagingDir, "config.yaml"),
			"name: staging\n"+
				"repopath: "+setup.Cwd+"\n"+
				"kubernetescontext: test-context\n"+
				"containerregistry: registry.example/test\n"+
				"runtimeversion: 1.0.0\n",
		)
		result := erun.Run(t, []string{"delete", "team", "dev", "--yes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "delete/real_run_default_env_reassigned_when_other_envs_remain", normalize.Apply(result.Combined))
		tenantCfg, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "team", "config.yaml"))
		if err != nil {
			t.Fatalf("read tenant config (tenant must survive while envs remain): %v", err)
		}
		if !strings.Contains(string(tenantCfg), "defaultenvironment: staging") {
			t.Errorf("expected default environment reassigned to staging, got:\n%s", tenantCfg)
		}
	})

	t.Run("real_run_last_env_of_non_default_tenant_keeps_root_default", func(t *testing.T) {
		// Exercises clearDeletedDefaultTenant's not-the-default branch:
		// removing the last env of a secondary tenant deletes that tenant's
		// config but must leave the root defaulttenant (team) untouched.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedSecondaryTenantEnv(t, setup, "other", "staging", 0)
		result := erun.Run(t, []string{"delete", "other", "staging", "--yes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "delete/real_run_last_env_of_non_default_tenant_keeps_root_default", normalize.Apply(result.Combined))
		if _, err := os.Stat(filepath.Join(setup.ConfigHome, "erun", "other")); !os.IsNotExist(err) {
			t.Errorf("expected secondary tenant config tree to be removed, stat err: %v", err)
		}
		rootCfg, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
		if err != nil {
			t.Fatalf("read root config: %v", err)
		}
		if !strings.Contains(string(rootCfg), "defaulttenant: team") {
			t.Errorf("root default tenant must survive deleting a non-default tenant, got:\n%s", rootCfg)
		}
	})

	t.Run("real_run_namespace_delete_failure_warns_and_continues", func(t *testing.T) {
		// Exercises deleteRemoteEnvironmentNamespace's real-run failure arm
		// plus runDeleteCommand's warning print: when kubectl cannot delete
		// the namespace, the error is surfaced as a warning on stderr and
		// the local config delete still proceeds. The kubectl stub's
		// non-zero exit is the decision input driving the failure branch.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{
			Stderr:   `Error from server (Forbidden): namespaces "team-dev" is forbidden`,
			ExitCode: 1,
		})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		envDir := filepath.Join(setup.ConfigHome, "erun", "team", "dev")
		result := erun.Run(t, []string{"delete", "team", "dev", "--yes"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "delete/real_run_namespace_delete_failure_warns_and_continues", normalize.Apply(result.Combined))
		if _, err := os.Stat(envDir); !os.IsNotExist(err) {
			t.Errorf("local config delete must continue after namespace failure, stat err: %v", err)
		}
	})

	t.Run("real_run_with_yes_flag_skips_confirmation_and_removes_config", func(t *testing.T) {
		// Exercises delete.go runDeleteCommand real-run path with --yes:
		// the confirmation prompt is bypassed, the env config tree is
		// physically removed, and the "deleted environment" line shows on
		// stdout. Stubs kubectl so the namespace delete succeeds without
		// touching a cluster.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		envDir := filepath.Join(setup.ConfigHome, "erun", "team", "dev")
		if _, err := os.Stat(envDir); err != nil {
			t.Fatalf("seeded env config missing before delete: %v", err)
		}
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"delete", "team", "dev", "--yes"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		// Filesystem state — golden cannot assert this; keep the os.Stat
		// check.
		if _, err := os.Stat(envDir); !os.IsNotExist(err) {
			t.Errorf("expected env config tree to be removed at %s, stat err: %v", envDir, err)
		}
		golden.Equal(t, "delete/real_run_with_yes_flag_skips_confirmation_and_removes_config", normalize.Apply(result.Combined))
	})
}
