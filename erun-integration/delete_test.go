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
		// A runtime-type env counts as remote for delete: it must trace the
		// kubectl namespace delete like a remote=true env.
		setup := env.New(t)
		seedExplicitTypeEnv(t, setup, "team", "prod", "runtime")
		result := erun.Run(t, []string{"delete", "team", "prod", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "delete/dry_run_with_runtime_type_traces_namespace_delete", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_with_remote_env_traces_namespace_delete", func(t *testing.T) {
		// A remote env's dry-run traces the kubectl namespace delete
		// alongside the local config removal, without touching the cluster.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"delete", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "delete/dry_run_with_remote_env_traces_namespace_delete", normalize.Apply(result.Combined))
	})

	t.Run("rejects_remote_env_without_kubernetes_context", func(t *testing.T) {
		// Regression: a remote env missing its kubernetescontext field used
		// to silently delete the namespace from the host's current kubectl
		// context (e.g. a developer's orbstack). Delete now errors up front.
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
		// Happy path without --yes: the prompt requires the literal
		// "<tenant>-<environment>" string to proceed. Deleting the tenant's
		// last (local) env cascades to clearing the tenant config and the
		// root default tenant.
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
		// A mismatched confirmation (anything but "<tenant>-<environment>")
		// must abort before any state is touched — config tree stays intact.
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
		// Deleting the tenant's default env while another env remains must
		// keep the tenant and promote the next env to default, not leave a
		// dangling default reference.
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
		// Removing the last env of a secondary (non-default) tenant deletes
		// that tenant's config but must leave the root default tenant
		// untouched.
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
		// A failed namespace delete is non-fatal: kubectl's error is surfaced
		// as a warning on stderr and the local config delete still proceeds.
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
		// --yes bypasses the confirmation prompt and really removes the env
		// config tree.
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

	t.Run("real_run_removes_the_environments_port_forward_state", func(t *testing.T) {
		// A port-forward state file names a local port, and that port range is
		// freed and reissued to whichever environment is created next. Leaving
		// the file behind after delete lets it resolve to a live forward that
		// now belongs to somebody else, so delete must clear it the same way it
		// clears the rest of the environment's footprint.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		seedMCPPortForwardState(t, setup, "team", "dev", 26100)
		statePath := portForwardStateFile(setup, "mcp", "team", "dev")
		if _, err := os.Stat(statePath); err != nil {
			t.Fatalf("seeded port-forward state missing before delete: %v", err)
		}
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)

		result := erun.Run(t, []string{"delete", "team", "dev", "--yes"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		// Filesystem state — golden cannot assert this; keep the os.Stat check
		// (mirrors real_run_with_yes_flag_skips_confirmation_and_removes_config).
		if _, err := os.Stat(statePath); !os.IsNotExist(err) {
			t.Errorf("expected the port-forward state file to be removed at %s, stat err: %v", statePath, err)
		}
	})

	t.Run("dry_run_traces_the_port_forward_state_removal_without_deleting_it", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		seedMCPPortForwardState(t, setup, "team", "dev", 26100)
		statePath := portForwardStateFile(setup, "mcp", "team", "dev")

		result := erun.Run(t, []string{"delete", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "y\n"})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, statePath) {
			t.Fatalf("expected the dry-run plan to name the port-forward state file, got:\n%s", result.Combined)
		}
		if _, err := os.Stat(statePath); err != nil {
			t.Errorf("a dry run must not remove the port-forward state file, stat err: %v", err)
		}
	})
}
