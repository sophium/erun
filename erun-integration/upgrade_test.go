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
		// The upgrade's own deploy resolves the runtime chart ladder; the seam
		// confirms erun-devops published so it upgrades instead of refusing.
		envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:2.0.0")
		result := erun.Run(t, []string{"upgrade", "team", "dev", "--version", "2.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
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
		// registry-resolution path is deterministic without network. The
		// snapshot stream's base (2.1.0) outranks the stable (2.0.0), so the
		// snapshot stays the snapshot channel's target. Both envs
		// are seeded already at their channel target, so the plan shows the
		// resolved channels + targets with no deploy.
		setup := env.New(t)
		seedUpgradeTenant(t, setup, "team", "prod")
		seedUpgradeEnv(t, setup, "team", "prod",
			"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\nruntimeversion: 2.0.0\ntype: runtime\nautoupgrade: true\n")
		seedUpgradeEnv(t, setup, "team", "agent",
			"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\nruntimeversion: 2.1.0-snapshot-20260101000000\ntype: remote-agent\nautoupgrade: true\n")
		envVars := append(setup.Env(), "ERUN_UPGRADE_VERSIONS_OVERRIDE=stable=2.0.0,snapshot=2.1.0-snapshot-20260101000000")
		result := erun.Run(t, []string{"upgrade", "team", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "upgrade/dry_run_channel_targets_via_registry_seam", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_snapshot_channel_stable_supersedes_snapshot", func(t *testing.T) {
		// A snapshot-channel env at the latest snapshot while a stable release
		// with the same base version exists: the snapshot is a pre-release of
		// that stable, so the stable supersedes it and becomes the snapshot
		// channel's target — the supersede decision is traced and
		// the member upgrades to the stable, with the deploy dry-run traced.
		setup := env.New(t)
		seedUpgradeTenant(t, setup, "team", "dev")
		seedUpgradeEnv(t, setup, "team", "dev",
			"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\ncontainerregistry: registry.example/test\nruntimeversion: 2.0.0-snapshot-20260101000000\ntype: runtime\nautoupgrade: true\nupgradechannel: snapshot\n")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		// The upgrade's own deploy resolves the runtime chart ladder; the seam
		// confirms erun-devops published at the superseding stable so it
		// upgrades instead of refusing.
		envVars := append(setup.Env(),
			"ERUN_UPGRADE_VERSIONS_OVERRIDE=stable=2.0.0,snapshot=2.0.0-snapshot-20260101000000",
			"ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:2.0.0")
		result := erun.Run(t, []string{"upgrade", "team", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		// Normalization collapses both the stable and the snapshot tag to
		// <VERSION>, so the golden alone cannot prove the stable was chosen
		// over the snapshot — assert the un-normalized plan line for that.
		if !strings.Contains(result.Combined, "[snapshot] 2.0.0-snapshot-20260101000000 -> 2.0.0  (will upgrade)") {
			t.Fatalf("expected the snapshot-channel member to target the superseding stable 2.0.0, got: %s", result.Combined)
		}
		golden.Equal(t, "upgrade/dry_run_snapshot_channel_stable_supersedes_snapshot", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_snapshot_channel_converged_on_stable", func(t *testing.T) {
		// A snapshot-channel env that already adopted the superseding stable
		// must stay up to date — the channel target remains the stable, so the
		// member never flaps back to the older snapshot tag.
		setup := env.New(t)
		seedUpgradeTenant(t, setup, "team", "agent")
		seedUpgradeEnv(t, setup, "team", "agent",
			"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\nruntimeversion: 2.0.0\ntype: remote-agent\nautoupgrade: true\n")
		envVars := append(setup.Env(), "ERUN_UPGRADE_VERSIONS_OVERRIDE=stable=2.0.0,snapshot=2.0.0-snapshot-20260101000000")
		result := erun.Run(t, []string{"upgrade", "team", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		// Normalization masks stable-vs-snapshot in the golden; the
		// un-normalized line proves the up-to-date comparison ran against the
		// stable target, not the older snapshot.
		if !strings.Contains(result.Combined, "[snapshot] 2.0.0 -> 2.0.0  (up to date)") {
			t.Fatalf("expected the converged member to stay up to date at the stable 2.0.0, got: %s", result.Combined)
		}
		golden.Equal(t, "upgrade/dry_run_snapshot_channel_converged_on_stable", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_target_unresolved_reports_reason", func(t *testing.T) {
		// An opted-in env whose tenant's registry resolution fails (staged via
		// the seam's error= form) is never treated as up to date:
		// the plan line carries "(target unresolved: <reason>)", the run skips
		// it with the same reason, and the completion accounting counts it as
		// unresolved — distinct from upgraded / up to date / failed.
		setup := env.New(t)
		seedUpgradeTenant(t, setup, "team", "dev")
		seedUpgradeEnv(t, setup, "team", "dev",
			"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\nruntimeversion: 1.0.0\ntype: runtime\nautoupgrade: true\nupgradechannel: stable\n")
		envVars := append(setup.Env(), "ERUN_UPGRADE_VERSIONS_OVERRIDE=error=ghcr token request failed: 403 Forbidden")
		result := erun.Run(t, []string{"upgrade", "team", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "upgrade/dry_run_target_unresolved_reports_reason", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_snapshot_channel_without_published_snapshot", func(t *testing.T) {
		// A snapshot-channel env whose tenant has a resolvable stable but no
		// published snapshot: the channel target comes back empty without a
		// recorded failure, so ResolveUpgradePlan must fall back to the
		// default "no snapshot version found in the registry" reason and the
		// run must skip the member as unresolved. The seam supplies only
		// stable=; the dangling "ignored" segment (no '=') additionally locks
		// the seam parser's skip-malformed-segment branch without changing
		// the output.
		setup := env.New(t)
		seedUpgradeTenant(t, setup, "team", "agent")
		seedUpgradeEnv(t, setup, "team", "agent",
			"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\nruntimeversion: 2.0.0-snapshot-20260101000000\ntype: remote-agent\nautoupgrade: true\n")
		// The upgrade's own deploy resolves the runtime chart ladder; the seam
		// confirms erun-devops published at whichever version this member
		// resolves to so it upgrades instead of refusing.
		envVars := append(setup.Env(),
			"ERUN_UPGRADE_VERSIONS_OVERRIDE=stable=2.0.0,ignored",
			"ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:2.0.0,erun-devops:2.0.0-snapshot-20260101000000")
		result := erun.Run(t, []string{"upgrade", "team", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "upgrade/dry_run_snapshot_channel_without_published_snapshot", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_failed_deploy_reports_failure", func(t *testing.T) {
		// A lagging member whose deploy fails — the env has no
		// kubernetescontext, so the deploy's env resolution rejects it
		// (ErrKubernetesContextNotConfigured) before any cluster-facing
		// action — must be recorded as failed: the run continues, the
		// completion accounting counts it under "failed", and the command
		// exits non-zero naming the member. The env carries no runtimeversion so the plan line
		// renders the "(unset)" current version. A second tenant and a
		// second env are seeded but out of scope: the positional team/dev
		// scope must filter both (the tenant-scope and env-scope walker
		// branches) without a trace.
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
	})

	t.Run("dry_run_no_members_scoped_to_environment", func(t *testing.T) {
		// Scoping to one env that is not opted in yields the empty-plan
		// message with the tenant/environment suffix, never a deploy.
		setup := env.New(t)
		seedUpgradeTenant(t, setup, "team", "dev")
		seedUpgradeEnv(t, setup, "team", "dev",
			"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\nruntimeversion: 1.0.0\ntype: runtime\n")
		result := erun.Run(t, []string{"upgrade", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "upgrade/dry_run_no_members_scoped_to_environment", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_no_members_scoped_to_tenant", func(t *testing.T) {
		// Scoping to a tenant with no opted-in envs yields the empty-plan
		// message with the "for tenant" suffix.
		setup := env.New(t)
		seedUpgradeTenant(t, setup, "team", "dev")
		seedUpgradeEnv(t, setup, "team", "dev",
			"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\nruntimeversion: 1.0.0\ntype: runtime\n")
		result := erun.Run(t, []string{"upgrade", "team", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "upgrade/dry_run_no_members_scoped_to_tenant", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_corrupt_tenant_config_fails", func(t *testing.T) {
		// A tenant config.yaml that fails to parse must fail the whole
		// plan resolution with the "plan resolution failed" trace — the
		// upgrade fan-out cannot safely guess which envs are opted in when
		// the tenant listing itself is broken.
		setup := env.New(t)
		tenantDir := seedUpgradeTenant(t, setup, "team", "dev")
		mustWriteFile(t, filepath.Join(tenantDir, "config.yaml"), "{notyaml\n")
		result := erun.Run(t, []string{"upgrade", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for corrupt tenant config, got 0: %s", result.Combined)
		}
		golden.Equal(t, "upgrade/dry_run_corrupt_tenant_config_fails", normalize.Apply(result.Combined))
	})

	t.Run("flags_environment_without_tenant_errors", func(t *testing.T) {
		// --environment without --tenant (or a positional tenant) is
		// ambiguous; the command must fail before resolving anything.
		setup := env.New(t)
		result := erun.Run(t, []string{"upgrade", "--environment", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for --environment without tenant, got 0: %s", result.Combined)
		}
		golden.Equal(t, "upgrade/flags_environment_without_tenant_errors", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_scoped_flags_lagging", func(t *testing.T) {
		// The --tenant/--environment flag form scopes the run to one env —
		// the shape the desktop's per-env Upgrade-all fan-out uses,
		// equivalent to the positional form.
		setup := env.New(t)
		seedUpgradeTenant(t, setup, "team", "dev")
		seedUpgradeEnv(t, setup, "team", "dev",
			"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\ncontainerregistry: registry.example/test\nruntimeversion: 1.0.0\ntype: runtime\nautoupgrade: true\nupgradechannel: stable\n")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		// The upgrade's own deploy resolves the runtime chart ladder; the seam
		// confirms erun-devops published so it upgrades instead of refusing.
		envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:2.0.0")
		result := erun.Run(t, []string{"upgrade", "--tenant", "team", "--environment", "dev", "--version", "2.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "upgrade/dry_run_scoped_flags_lagging", normalize.Apply(result.Combined))
	})

	t.Run("flags_fleet_requires_tenant", func(t *testing.T) {
		// --fleet with no tenant in scope is refused outright: rolling every
		// environment across every tenant is far too high a blast radius to
		// resolve implicitly.
		setup := env.New(t)
		result := erun.Run(t, []string{"upgrade", "--fleet", "--version", "1.2.3", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for --fleet without --tenant, got 0: %s", result.Combined)
		}
		golden.Equal(t, "upgrade/flags_fleet_requires_tenant", normalize.Apply(result.Combined))
	})

	t.Run("flags_gate_environment_requires_tenant", func(t *testing.T) {
		// Mirrors `erun list --gate-environment`'s own "requires --tenant"
		// refusal for the same reason: naming a gate makes no sense without
		// naming whose fleet it gates.
		setup := env.New(t)
		result := erun.Run(t, []string{"upgrade", "--gate-environment", "build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for --gate-environment without --tenant, got 0: %s", result.Combined)
		}
		golden.Equal(t, "upgrade/flags_gate_environment_requires_tenant", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_gate_environment_not_found_fails", func(t *testing.T) {
		// A typo'd --gate-environment must fail loudly rather than silently
		// resolving a plan with no gate verdict -- the same contract `erun
		// list --gate-environment` already enforces for drift detection.
		setup := env.New(t)
		seedUpgradeTenant(t, setup, "team", "dev")
		seedUpgradeEnv(t, setup, "team", "dev",
			"repopath: "+setup.Cwd+"\nkubernetescontext: test-context\nruntimeversion: 1.0.0\ntype: runtime\n")
		result := erun.Run(t, []string{"upgrade", "team", "--gate-environment", "ghost", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for an unknown --gate-environment, got 0: %s", result.Combined)
		}
		golden.Equal(t, "upgrade/dry_run_gate_environment_not_found_fails", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_fleet_rolls_gate_environment_first", func(t *testing.T) {
		// The fleet-remediation shape: a tenant with envs that never opted
		// into Upgrade all (autoupgrade=false) still all roll under --fleet,
		// and --gate-environment forces the merge-queue gate to the front of
		// the resolved order regardless of where it sorts alphabetically --
		// "zzz-build" would otherwise resolve last. This is the release
		// cadence policy's "immediate, unconditional" gate redeploy: the gate
		// must never be the last environment rolled, since it validates
		// every change landing on the others.
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
	})

	t.Run("dry_run_held_lease_refuses_and_names_holder", func(t *testing.T) {
		// A roll must never yank an environment out from under a running
		// agent session: a held activity lease (the same signal `erun
		// resize` already refuses on) refuses the deploy and names the
		// holder, even under --dry-run.
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
	})

	t.Run("dry_run_held_lease_with_override_proceeds", func(t *testing.T) {
		// --override-lease is explicit and recorded: the same held lease as
		// above must let the dry-run plan through, tracing the override
		// rather than staying silent about it.
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
	})
}
