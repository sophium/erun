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

func seedUpgradeEnv(t testing.TB, setup env.Setup, tenant, environment, body string) {
	t.Helper()
	envDir := filepath.Join(setup.ConfigHome, "erun", tenant, environment)
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", envDir, err)
	}
	mustWriteFile(t, filepath.Join(envDir, "config.yaml"), "name: "+environment+"\n"+body)
}

func TestUpgrade(t *testing.T) {
	t.Parallel()
	t.Run("help", testUpgradeHelp)
	t.Run("dry_run_no_members", testUpgradeDryRunNoMembers)
	t.Run("dry_run_version_override_lagging", testUpgradeDryRunVersionOverrideLagging)
	t.Run("dry_run_up_to_date", testUpgradeDryRunUpToDate)
	t.Run("dry_run_channel_targets_via_registry_seam", testUpgradeDryRunChannelTargetsViaRegistrySeam)
	t.Run("dry_run_snapshot_channel_stable_supersedes_snapshot", testUpgradeDryRunSnapshotChannelStableSupersedesSnapshot)
	t.Run("dry_run_snapshot_channel_converged_on_stable", testUpgradeDryRunSnapshotChannelConvergedOnStable)
	t.Run("dry_run_target_unresolved_reports_reason", testUpgradeDryRunTargetUnresolvedReportsReason)
	t.Run("dry_run_snapshot_channel_without_published_snapshot", testUpgradeDryRunSnapshotChannelWithoutPublishedSnapshot)
	t.Run("dry_run_failed_deploy_reports_failure", testUpgradeDryRunFailedDeployReportsFailure)
	t.Run("dry_run_no_members_scoped_to_environment", testUpgradeDryRunNoMembersScopedToEnvironment)
	t.Run("dry_run_no_members_scoped_to_tenant", testUpgradeDryRunNoMembersScopedToTenant)
	t.Run("dry_run_corrupt_tenant_config_fails", testUpgradeDryRunCorruptTenantConfigFails)
	t.Run("flags_environment_without_tenant_errors", testUpgradeFlagsEnvironmentWithoutTenantErrors)
	t.Run("dry_run_scoped_flags_lagging", testUpgradeDryRunScopedFlagsLagging)
	t.Run("flags_fleet_requires_tenant", testUpgradeFlagsFleetRequiresTenant)
	t.Run("flags_gate_environment_requires_tenant", testUpgradeFlagsGateEnvironmentRequiresTenant)
	t.Run("dry_run_gate_environment_not_found_fails", testUpgradeDryRunGateEnvironmentNotFoundFails)
	t.Run("dry_run_fleet_rolls_gate_environment_first", testUpgradeDryRunFleetRollsGateEnvironmentFirst)
	t.Run("dry_run_held_lease_refuses_and_names_holder", testUpgradeDryRunHeldLeaseRefusesAndNamesHolder)
	t.Run("dry_run_held_lease_with_override_proceeds", testUpgradeDryRunHeldLeaseWithOverrideProceeds)
}

func testUpgradeHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"upgrade", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "upgrade/help", normalize.Apply(result.Combined))
}

func testUpgradeDryRunNoMembers(t *testing.T) {
	setup := env.New(t)
	seedUpgradeTenant(t, setup, "team", "dev")
	seedUpgradeEnv(t, setup, "team", "dev",
		"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\nruntimeversion: 1.0.0\ntype: runtime\n")
	result := erun.Run(t, []string{"upgrade", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "upgrade/dry_run_no_members", normalize.Apply(result.Combined))
}

func testUpgradeDryRunVersionOverrideLagging(t *testing.T) {
	// --version pins the target so no registry lookup is needed.
	setup := env.New(t)
	seedUpgradeTenant(t, setup, "team", "dev")
	seedUpgradeEnv(t, setup, "team", "dev",
		"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\ncontainerregistry: registry.example/test\nruntimeversion: 1.0.0\ntype: runtime\nautoupgrade: true\nupgradechannel: stable\n")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	// Confirm erun-devops published so the deploy upgrades instead of refusing.
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:2.0.0")
	result := erun.Run(t, []string{"upgrade", "team", "dev", "--version", "2.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	// A dry run must read as conditional, never as a performed rollout.
	if !strings.Contains(result.Combined, "Dry run: erun upgrade planned.") {
		t.Fatalf("expected an explicit dry-run marker, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "==> Would upgrade team/dev") {
		t.Fatalf("expected the per-environment banner to speak in the conditional, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "==> Dry run: would upgrade 1, 0 up to date, 0 unresolved, 0 failed") {
		t.Fatalf("expected the completion summary to speak in the conditional with a dry-run marker, got:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "==> Upgrading") || strings.Contains(result.Combined, "==> Upgrade complete") {
		t.Fatalf("expected no line to assert the upgrade as performed, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "deploy: would watch pods") {
		t.Fatalf("expected the pod-watch trace to speak in the conditional, got:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "deploy: watching pods") {
		t.Fatalf("expected no line to assert pods are being watched, got:\n%s", result.Combined)
	}
	golden.Equal(t, "upgrade/dry_run_version_override_lagging", normalize.Apply(result.Combined))
}

func testUpgradeDryRunUpToDate(t *testing.T) {
	setup := env.New(t)
	seedUpgradeTenant(t, setup, "team", "dev")
	seedUpgradeEnv(t, setup, "team", "dev",
		"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\ncontainerregistry: registry.example/test\nruntimeversion: 1.0.0\ntype: runtime\nautoupgrade: true\nupgradechannel: stable\n")
	result := erun.Run(t, []string{"upgrade", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "upgrade/dry_run_up_to_date", normalize.Apply(result.Combined))
}

func testUpgradeDryRunChannelTargetsViaRegistrySeam(t *testing.T) {
	// Channels resolve from defaults (runtime->stable, remote-agent->snapshot); targets come
	// from ERUN_UPGRADE_VERSIONS_OVERRIDE. Both envs are already at target.
	setup := env.New(t)
	seedUpgradeTenant(t, setup, "team", "prod")
	seedUpgradeEnv(t, setup, "team", "prod",
		"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\nruntimeversion: 2.0.0\ntype: runtime\nautoupgrade: true\n")
	seedUpgradeEnv(t, setup, "team", "agent",
		"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\nruntimeversion: 2.1.0-snapshot-20260101000000\ntype: remote-agent\nautoupgrade: true\n")
	envVars := append(setup.Env(), "ERUN_UPGRADE_VERSIONS_OVERRIDE=stable=2.0.0,snapshot=2.1.0-snapshot-20260101000000")
	result := erun.Run(t, []string{"upgrade", "team", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "upgrade/dry_run_channel_targets_via_registry_seam", normalize.Apply(result.Combined))
}

func testUpgradeDryRunSnapshotChannelStableSupersedesSnapshot(t *testing.T) {
	// A stable with the same base supersedes the snapshot as the snapshot channel's target.
	setup := env.New(t)
	seedUpgradeTenant(t, setup, "team", "dev")
	seedUpgradeEnv(t, setup, "team", "dev",
		"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\ncontainerregistry: registry.example/test\nruntimeversion: 2.0.0-snapshot-20260101000000\ntype: runtime\nautoupgrade: true\nupgradechannel: snapshot\n")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	// Confirm erun-devops published so the deploy upgrades instead of refusing.
	envVars := append(setup.Env(),
		"ERUN_UPGRADE_VERSIONS_OVERRIDE=stable=2.0.0,snapshot=2.0.0-snapshot-20260101000000",
		"ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:2.0.0")
	result := erun.Run(t, []string{"upgrade", "team", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	// Normalization masks stable vs snapshot, so assert the raw plan line.
	if !strings.Contains(result.Combined, "[snapshot] 2.0.0-snapshot-20260101000000 -> 2.0.0  (will upgrade)") {
		t.Fatalf("expected the snapshot-channel member to target the superseding stable 2.0.0, got: %s", result.Combined)
	}
	golden.Equal(t, "upgrade/dry_run_snapshot_channel_stable_supersedes_snapshot", normalize.Apply(result.Combined))
}

func testUpgradeDryRunSnapshotChannelConvergedOnStable(t *testing.T) {
	// The member must not flap back to the older snapshot tag.
	setup := env.New(t)
	seedUpgradeTenant(t, setup, "team", "agent")
	seedUpgradeEnv(t, setup, "team", "agent",
		"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\nruntimeversion: 2.0.0\ntype: remote-agent\nautoupgrade: true\n")
	envVars := append(setup.Env(), "ERUN_UPGRADE_VERSIONS_OVERRIDE=stable=2.0.0,snapshot=2.0.0-snapshot-20260101000000")
	result := erun.Run(t, []string{"upgrade", "team", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	// Normalization masks stable vs snapshot, so assert the raw plan line.
	if !strings.Contains(result.Combined, "[snapshot] 2.0.0 -> 2.0.0  (up to date)") {
		t.Fatalf("expected the converged member to stay up to date at the stable 2.0.0, got: %s", result.Combined)
	}
	golden.Equal(t, "upgrade/dry_run_snapshot_channel_converged_on_stable", normalize.Apply(result.Combined))
}

func testUpgradeDryRunTargetUnresolvedReportsReason(t *testing.T) {
	// An unresolved target is never treated as up to date.
	setup := env.New(t)
	seedUpgradeTenant(t, setup, "team", "dev")
	seedUpgradeEnv(t, setup, "team", "dev",
		"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\nruntimeversion: 1.0.0\ntype: runtime\nautoupgrade: true\nupgradechannel: stable\n")
	envVars := append(setup.Env(), "ERUN_UPGRADE_VERSIONS_OVERRIDE=error=ghcr token request failed: 403 Forbidden")
	result := erun.Run(t, []string{"upgrade", "team", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "upgrade/dry_run_target_unresolved_reports_reason", normalize.Apply(result.Combined))
}

func testUpgradeDryRunSnapshotChannelWithoutPublishedSnapshot(t *testing.T) {
	// The dangling "ignored" segment also covers the seam parser's malformed-segment skip.
	setup := env.New(t)
	seedUpgradeTenant(t, setup, "team", "agent")
	seedUpgradeEnv(t, setup, "team", "agent",
		"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\nruntimeversion: 2.0.0-snapshot-20260101000000\ntype: remote-agent\nautoupgrade: true\n")
	// Confirm erun-devops published so the deploy upgrades instead of refusing.
	envVars := append(setup.Env(),
		"ERUN_UPGRADE_VERSIONS_OVERRIDE=stable=2.0.0,ignored",
		"ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:2.0.0,erun-devops:2.0.0-snapshot-20260101000000")
	result := erun.Run(t, []string{"upgrade", "team", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "upgrade/dry_run_snapshot_channel_without_published_snapshot", normalize.Apply(result.Combined))
}

func testUpgradeDryRunFailedDeployReportsFailure(t *testing.T) {
	// No kubernetescontext makes the deploy fail before touching a cluster. The extra tenant
	// and env cover the scope filters.
	setup := env.New(t)
	seedUpgradeTenant(t, setup, "other", "prod")
	seedUpgradeEnv(t, setup, "other", "prod",
		"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\nruntimeversion: 1.0.0\ntype: runtime\nautoupgrade: true\n")
	seedUpgradeTenant(t, setup, "team", "dev")
	seedUpgradeEnv(t, setup, "team", "dev",
		"repopath: "+setup.Cwd+"\ntype: runtime\nautoupgrade: true\nupgradechannel: stable\n")
	seedUpgradeEnv(t, setup, "team", "staging",
		"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\nruntimeversion: 1.0.0\ntype: runtime\nautoupgrade: true\n")
	result := erun.Run(t, []string{"upgrade", "team", "dev", "--version", "2.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for failed upgrade deploy, got 0: %s", result.Combined)
	}
	golden.Equal(t, "upgrade/dry_run_failed_deploy_reports_failure", normalize.Apply(result.Combined))
}

func testUpgradeDryRunNoMembersScopedToEnvironment(t *testing.T) {
	setup := env.New(t)
	seedUpgradeTenant(t, setup, "team", "dev")
	seedUpgradeEnv(t, setup, "team", "dev",
		"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\nruntimeversion: 1.0.0\ntype: runtime\n")
	result := erun.Run(t, []string{"upgrade", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "upgrade/dry_run_no_members_scoped_to_environment", normalize.Apply(result.Combined))
}

func testUpgradeDryRunNoMembersScopedToTenant(t *testing.T) {
	setup := env.New(t)
	seedUpgradeTenant(t, setup, "team", "dev")
	seedUpgradeEnv(t, setup, "team", "dev",
		"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\nruntimeversion: 1.0.0\ntype: runtime\n")
	result := erun.Run(t, []string{"upgrade", "team", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "upgrade/dry_run_no_members_scoped_to_tenant", normalize.Apply(result.Combined))
}

func testUpgradeDryRunCorruptTenantConfigFails(t *testing.T) {
	// A broken tenant listing cannot say which envs are opted in.
	setup := env.New(t)
	tenantDir := seedUpgradeTenant(t, setup, "team", "dev")
	mustWriteFile(t, filepath.Join(tenantDir, "config.yaml"), "{notyaml\n")
	result := erun.Run(t, []string{"upgrade", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for corrupt tenant config, got 0: %s", result.Combined)
	}
	golden.Equal(t, "upgrade/dry_run_corrupt_tenant_config_fails", normalize.Apply(result.Combined))
}

func testUpgradeFlagsEnvironmentWithoutTenantErrors(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"upgrade", "--environment", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for --environment without tenant, got 0: %s", result.Combined)
	}
	golden.Equal(t, "upgrade/flags_environment_without_tenant_errors", normalize.Apply(result.Combined))
}

func testUpgradeDryRunScopedFlagsLagging(t *testing.T) {
	setup := env.New(t)
	seedUpgradeTenant(t, setup, "team", "dev")
	seedUpgradeEnv(t, setup, "team", "dev",
		"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\ncontainerregistry: registry.example/test\nruntimeversion: 1.0.0\ntype: runtime\nautoupgrade: true\nupgradechannel: stable\n")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	// Confirm erun-devops published so the deploy upgrades instead of refusing.
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:2.0.0")
	result := erun.Run(t, []string{"upgrade", "--tenant", "team", "--environment", "dev", "--version", "2.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "upgrade/dry_run_scoped_flags_lagging", normalize.Apply(result.Combined))
}

func testUpgradeFlagsFleetRequiresTenant(t *testing.T) {
	// Rolling every tenant is too large a blast radius to resolve implicitly.
	setup := env.New(t)
	result := erun.Run(t, []string{"upgrade", "--fleet", "--version", "1.2.3", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for --fleet without --tenant, got 0: %s", result.Combined)
	}
	golden.Equal(t, "upgrade/flags_fleet_requires_tenant", normalize.Apply(result.Combined))
}

func testUpgradeFlagsGateEnvironmentRequiresTenant(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"upgrade", "--gate-environment", "build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for --gate-environment without --tenant, got 0: %s", result.Combined)
	}
	golden.Equal(t, "upgrade/flags_gate_environment_requires_tenant", normalize.Apply(result.Combined))
}

func testUpgradeDryRunGateEnvironmentNotFoundFails(t *testing.T) {
	setup := env.New(t)
	seedUpgradeTenant(t, setup, "team", "dev")
	seedUpgradeEnv(t, setup, "team", "dev",
		"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\nruntimeversion: 1.0.0\ntype: runtime\n")
	result := erun.Run(t, []string{"upgrade", "team", "--gate-environment", "ghost", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an unknown --gate-environment, got 0: %s", result.Combined)
	}
	golden.Equal(t, "upgrade/dry_run_gate_environment_not_found_fails", normalize.Apply(result.Combined))
}

func testUpgradeDryRunFleetRollsGateEnvironmentFirst(t *testing.T) {
	// --fleet ignores autoupgrade; --gate-environment rolls the gate first even though
	// "zzz-build" sorts last, since it validates every other change.
	setup := env.New(t)
	seedUpgradeTenant(t, setup, "team", "aaa-dev")
	seedUpgradeEnv(t, setup, "team", "aaa-dev",
		"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\ncontainerregistry: registry.example/test\nruntimeversion: 1.0.0\ntype: runtime\n")
	seedUpgradeEnv(t, setup, "team", "zzz-build",
		"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\ncontainerregistry: registry.example/test\nruntimeversion: 1.0.0\ntype: runtime\n")
	fixture.SeedDevopsRepo(t, setup, "team", "aaa-dev")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:2.0.0")
	result := erun.Run(t, []string{"upgrade", "team", "--fleet", "--version", "2.0.0", "--gate-environment", "zzz-build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "1. team/zzz-build") {
		t.Fatalf("expected the gate environment first in the resolved order, got:\n%s", result.Combined)
	}
	golden.Equal(t, "upgrade/dry_run_fleet_rolls_gate_environment_first", normalize.Apply(result.Combined))
}

func testUpgradeDryRunHeldLeaseRefusesAndNamesHolder(t *testing.T) {
	// A held activity lease refuses the roll, even under --dry-run.
	setup := env.New(t)
	seedUpgradeTenant(t, setup, "team", "dev")
	seedUpgradeEnv(t, setup, "team", "dev",
		"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\ncontainerregistry: registry.example/test\nruntimeversion: 1.0.0\ntype: runtime\nautoupgrade: true\nupgradechannel: stable\n")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	seedHeldExclusiveLease(t, setup, "team", "dev", "eng-42", "exec_job_attach")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:2.0.0")
	result := erun.Run(t, []string{"upgrade", "team", "dev", "--version", "2.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit while a lease is held, got 0: %s", result.Combined)
	}
	if !strings.Contains(result.Combined, "orchestrator eng-42") {
		t.Fatalf("expected the refusal to name the holder, got:\n%s", result.Combined)
	}
	golden.Equal(t, "upgrade/dry_run_held_lease_refuses_and_names_holder", normalize.Apply(result.Combined))
}

func testUpgradeDryRunHeldLeaseWithOverrideProceeds(t *testing.T) {
	setup := env.New(t)
	seedUpgradeTenant(t, setup, "team", "dev")
	seedUpgradeEnv(t, setup, "team", "dev",
		"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\ncontainerregistry: registry.example/test\nruntimeversion: 1.0.0\ntype: runtime\nautoupgrade: true\nupgradechannel: stable\n")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	seedHeldExclusiveLease(t, setup, "team", "dev", "eng-42", "exec_job_attach")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:2.0.0")
	result := erun.Run(t, []string{"upgrade", "team", "dev", "--version", "2.0.0", "--override-lease", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "overriding") {
		t.Fatalf("expected the trace to record the override, got:\n%s", result.Combined)
	}
	golden.Equal(t, "upgrade/dry_run_held_lease_with_override_proceeds", normalize.Apply(result.Combined))
}
