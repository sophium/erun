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

func TestUnexpose(t *testing.T) {
	t.Parallel()
	t.Run("help", testUnexposeHelp)
	t.Run("dry_run", testUnexposeDryRun)
	t.Run("dry_run_with_platform_override", testUnexposeDryRunWithPlatformOverride)
	t.Run("requires_platform_config", testUnexposeRequiresPlatformConfig)
	t.Run("real_run_via_stub", testUnexposeRealRunViaStub)
	t.Run("real_run_via_platform_alias", testUnexposeRealRunViaPlatformAlias)
	t.Run("dry_run_via_platform_alias", testUnexposeDryRunViaPlatformAlias)
	t.Run("dry_run_skip_if_unconfigured", testUnexposeDryRunSkipIfUnconfigured)
}

func testUnexposeHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"unexpose", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "unexpose/help", normalize.Apply(result.Combined))
}

func testUnexposeDryRun(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
	result := erun.Run(t, []string{"unexpose", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "unexpose/dry_run", normalize.Apply(result.Combined))
}

func testUnexposeDryRunWithPlatformOverride(t *testing.T) {
	// The hosted delete Job has no checkout, so the overrides replace the project.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{
		"unexpose", "team", "dev",
		"--services-zone", "services.erunpaas.com",
		"--platform-namespace", "frs-prod",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "unexpose/dry_run_with_platform_override", normalize.Apply(result.Combined))
}

func testUnexposeRequiresPlatformConfig(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"unexpose", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit without a platform block, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "unexpose/requires_platform_config", normalize.Apply(result.Combined))
}

func testUnexposeRealRunViaStub(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"unexpose", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "unexpose/real_run_via_stub", normalize.Apply(result.Combined))
}

func testUnexposeRealRunViaPlatformAlias(t *testing.T) {
	// Substring assertion because the stub server's port is ephemeral.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
	server, calls := hostnameAPIStubServer(t)
	platformAlias(t, setup, server)
	result := erun.Run(t, []string{"unexpose", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "removed wildcard DNS record *.team-dev.services.erunpaas.com for team/dev") {
		t.Fatalf("expected the removed wildcard record in output, got:\n%s", result.Combined)
	}
	want := []string{"GET /v1/environments", "DELETE /v1/environments/env-1/hostname"}
	if got := *calls; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("stub server calls = %v, want %v", got, want)
	}
}

func testUnexposeDryRunViaPlatformAlias(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{"unexpose", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "unexpose/dry_run_via_platform_alias", normalize.Apply(result.Combined))
}

func testUnexposeDryRunSkipIfUnconfigured(t *testing.T) {
	// The delete Job composes unexpose without knowing whether the target was exposed.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"unexpose", "team", "dev", "--skip-if-unconfigured", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "unexpose/dry_run_skip_if_unconfigured", normalize.Apply(result.Combined))
}
