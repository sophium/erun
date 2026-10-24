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

func testCloudSetRealRunSameAliasHealsManagedCloud(t *testing.T) {
	// `cloud set` with the alias the env already carries takes the
	// no-change path but must still backfill managedcloud=true for a
	// remote worktree that predates the flag (a side effect read from
	// disk, not the captured streams).
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	envCfgPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	body := "name: dev\n" +
		"repopath: /home/erun/git/team\n" +
		"kubernetescontext: test-context\n" +
		"containerregistry: registry.example/test\n" +
		"runtimeversion: 1.0.0\n" +
		"type: remote-agent\n" +
		"cloudprovideralias: team-cloud\n"
	if err := os.WriteFile(envCfgPath, []byte(body), 0o644); err != nil {
		t.Fatalf("rewrite env config with alias: %v", err)
	}
	result := erun.Run(t, []string{"cloud", "set", "team", "dev", "--alias", "team-cloud"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/set_real_run_same_alias_heals_managed_cloud", normalize.Apply(result.Combined))
	raw, err := os.ReadFile(envCfgPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	if !strings.Contains(string(raw), "managedcloud: true") {
		t.Errorf("expected managedcloud backfilled on the remote env, got:\n%s", raw)
	}
}

func testCloudSetRealRunNewAliasMarksManagedCloud(t *testing.T) {
	// Assigning a new alias to a remote env must set managedcloud=true (a
	// remote worktree implies a managed cloud runtime) and persist both
	// fields. The config deliberately omits `name:` so the
	// environment-name backfill branch also runs.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	envCfgPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	body := "repopath: " + filepath.Join(setup.Home, "git", "team") + "\n" +
		"kubernetescontext: test-context\n" +
		"containerregistry: registry.example/test\n" +
		"runtimeversion: 1.0.0\n" +
		"type: remote-agent\n"
	if err := os.WriteFile(envCfgPath, []byte(body), 0o644); err != nil {
		t.Fatalf("rewrite env config without alias: %v", err)
	}
	result := erun.Run(t, []string{"cloud", "set", "team", "dev", "--alias", "team-cloud"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/set_real_run_new_alias_marks_managed_cloud", normalize.Apply(result.Combined))
	raw, err := os.ReadFile(envCfgPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	for _, want := range []string{"cloudprovideralias: team-cloud", "managedcloud: true", "name: dev"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("expected persisted env config to contain %q, got:\n%s", want, raw)
		}
	}
}

func testCloudSetMissingEnvironmentFails(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"cloud", "set", "team", "ghost", "--alias", "team-cloud"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a missing environment, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "cloud/set_missing_environment_fails", normalize.Apply(result.Combined))
}

func testCloudSetEmptyAliasFails(t *testing.T) {
	// --alias is cobra-required, but an explicitly empty value still passes
	// cobra's presence check and must be rejected by the params validation.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"cloud", "set", "team", "dev", "--alias", ""}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an empty alias, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "cloud/set_empty_alias_fails", normalize.Apply(result.Combined))
}

func testCloudSetEmptyTenantFails(t *testing.T) {
	// TENANT/ENVIRONMENT are cobra-positional (ExactArgs(2)), so an
	// explicitly empty value still satisfies the arg-count check and must
	// be rejected by normalizeEnvironmentCloudProviderAliasParams before
	// any env lookup runs.
	setup := env.New(t)
	result := erun.Run(t, []string{"cloud", "set", "", "dev", "--alias", "team-cloud"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an empty tenant, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "cloud/set_empty_tenant_fails", normalize.Apply(result.Combined))
}

func testCloudSetEmptyEnvironmentFails(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"cloud", "set", "team", "", "--alias", "team-cloud"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an empty environment, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "cloud/set_empty_environment_fails", normalize.Apply(result.Combined))
}

func testCloudSetDryRunTracesEnvAliasWrite(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"cloud", "set", "team", "dev", "--alias", "team-cloud", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/set_dry_run_traces_env_alias_write", normalize.Apply(result.Combined))
}
