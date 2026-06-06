package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// seedUpgradeTenant writes the tenant + global config for the upgrade tests.
func seedUpgradeTenant(t testing.TB, setup env.Setup, tenant, defaultEnv string) string {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, tenant)
	if err := os.MkdirAll(tenantDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", tenantDir, err)
	}
	mustWriteFile(t, filepath.Join(root, "config.yaml"), "defaulttenant: "+tenant+"\n")
	mustWriteFile(t, filepath.Join(tenantDir, "config.yaml"),
		"projectroot: "+setup.Cwd+"\nname: "+tenant+"\ndefaultenvironment: "+defaultEnv+"\n")
	return tenantDir
}

// seedUpgradeEnv writes one env config with explicit upgrade fields. body is
// the env config.yaml contents minus the name (added here).
func seedUpgradeEnv(t testing.TB, setup env.Setup, tenant, environment, body string) {
	t.Helper()
	envDir := filepath.Join(setup.ConfigHome, "erun", tenant, environment)
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", envDir, err)
	}
	mustWriteFile(t, filepath.Join(envDir, "config.yaml"), "name: "+environment+"\n"+body)
}

func TestUpgrade(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"upgrade", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "upgrade/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_no_members", func(t *testing.T) {
		// An env that is not opted in (no autoupgrade) produces an empty plan
		// and the "no environments opted in" message — never a deploy.
		setup := env.New(t)
		seedUpgradeTenant(t, setup, "team", "dev")
		seedUpgradeEnv(t, setup, "team", "dev",
			"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\nruntimeversion: 1.0.0\ntype: runtime\n")
		result := erun.Run(t, []string{"upgrade", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "upgrade/dry_run_no_members", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_version_override_lagging", func(t *testing.T) {
		// One opted-in env lagging behind an explicit --version override:
		// the plan marks it (will upgrade) and the deploy dry-run for it is
		// traced. --version pins the target so no registry lookup is needed.
		setup := env.New(t)
		seedUpgradeTenant(t, setup, "team", "dev")
		seedUpgradeEnv(t, setup, "team", "dev",
			"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\ncontainerregistry: registry.example/test\nruntimeversion: 1.0.0\ntype: runtime\nautoupgrade: true\nupgradechannel: stable\n")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"upgrade", "team", "dev", "--version", "2.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "upgrade/dry_run_version_override_lagging", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_up_to_date", func(t *testing.T) {
		// An opted-in env already at the target version is skipped — no deploy.
		setup := env.New(t)
		seedUpgradeTenant(t, setup, "team", "dev")
		seedUpgradeEnv(t, setup, "team", "dev",
			"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\ncontainerregistry: registry.example/test\nruntimeversion: 1.0.0\ntype: runtime\nautoupgrade: true\nupgradechannel: stable\n")
		result := erun.Run(t, []string{"upgrade", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "upgrade/dry_run_up_to_date", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_channel_targets_via_registry_seam", func(t *testing.T) {
		// Two opted-in envs tracking different channels. The channel each
		// tracks is resolved (runtime->stable default, remote-agent->snapshot
		// default), and the target for each channel comes from the registry —
		// supplied here via the ERUN_UPGRADE_VERSIONS_OVERRIDE test seam so the
		// registry-resolution path is deterministic without network. Both are
		// seeded already at their channel target, so the plan shows the
		// resolved channels + targets with no deploy.
		setup := env.New(t)
		seedUpgradeTenant(t, setup, "team", "prod")
		seedUpgradeEnv(t, setup, "team", "prod",
			"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\nruntimeversion: 2.0.0\ntype: runtime\nautoupgrade: true\n")
		seedUpgradeEnv(t, setup, "team", "agent",
			"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\nruntimeversion: 2.0.0-snapshot-20260101000000\ntype: remote-agent\nautoupgrade: true\n")
		envVars := append(setup.Env(), "ERUN_UPGRADE_VERSIONS_OVERRIDE=stable=2.0.0,snapshot=2.0.0-snapshot-20260101000000")
		result := erun.Run(t, []string{"upgrade", "team", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "upgrade/dry_run_channel_targets_via_registry_seam", normalize.Apply(result.Combined))
	})
}
