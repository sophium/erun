package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestList(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"help", testListHelp},

		// `erun list --tenant` reports erun-version drift across a
		// tenant's own environments instead of the full listing, and
		// --gate-environment additionally flags whether the environment driving
		// that tenant's merge-queue gate runs an older erun version than any
		// environment it gates -- the concrete defect this exists to catch.
		{"version_drift_gate_environment_requires_tenant", testListVersionDriftGateEnvironmentRequiresTenant},
		{"version_drift_tenant_not_found", testListVersionDriftTenantNotFound},
		{"version_drift_reports_every_environment_and_the_max", testListVersionDriftReportsEveryEnvironmentAndTheMax},
		{"version_drift_gate_environment_behind_flags_what_it_gates", testListVersionDriftGateEnvironmentBehindFlagsWhatItGates},
		{"version_drift_gate_environment_up_to_date", testListVersionDriftGateEnvironmentUpToDate},
		{"version_drift_gate_environment_not_found", testListVersionDriftGateEnvironmentNotFound},
		{"version_drift_gate_environment_version_unresolved", testListVersionDriftGateEnvironmentVersionUnresolved},

		// Three distinct situations must not collapse into `version=none` or drop
		// silently from the drift verdict: nothing deployed, a release deployed
		// but this config never learned its version, and an unreachable cluster.
		// These four scenarios lock each rendering plus the dry-run trace that makes the live check auditable.
		{"version_drift_environment_confirmed_not_deployed", testListVersionDriftEnvironmentConfirmedNotDeployed},
		{"version_drift_resolves_live_version_from_deployed_release", testListVersionDriftResolvesLiveVersionFromDeployedRelease},
		{"version_drift_environment_cluster_unreachable", testListVersionDriftEnvironmentClusterUnreachable},
		{"fail_on_drift_with_unresolved_environment_exits_non_zero", testListFailOnDriftWithUnresolvedEnvironmentExitsNonZero},
		{"version_drift_dry_run_traces_live_check_for_unresolved_environment", testListVersionDriftDryRunTracesLiveCheckForUnresolvedEnvironment},
		{"version_drift_unresolved_environment_json_output", testListVersionDriftUnresolvedEnvironmentJSONOutput},
		{"version_drift_json_output", testListVersionDriftJSONOutput},

		// `list` is a reporting command and always exits 0 on its own
		// findings; --fail-on-drift is the opt-in that lets one invocation of a
		// drift report be wired into a gate instead (erun-cli/AGENTS.md § "Exit-
		// Code Contract: Reporting Commands Vs Gating Checks").
		{"fail_on_drift_requires_tenant_or_control_planes", testListFailOnDriftRequiresTenantOrControlPlanes},
		{"fail_on_drift_with_tenant_behind_max_exits_non_zero", testListFailOnDriftWithTenantBehindMaxExitsNonZero},
		{"fail_on_drift_with_tenant_no_drift_exits_zero", testListFailOnDriftWithTenantNoDriftExitsZero},
		{"fail_on_drift_with_gate_environment_behind_exits_non_zero", testListFailOnDriftWithGateEnvironmentBehindExitsNonZero},
		{"empty_config", testListEmptyConfig},
		{"with_seeded_tenant_env", testListWithSeededTenantEnv},
		{"with_seeded_host_env", testListWithSeededHostEnv},
		{"platform_account_env_shows_enabled", testListPlatformAccountEnvShowsEnabled},

		// runtime-version alone reads as an erun version even when it
		// is not one, and a Helm release name can disagree with the image it
		// actually runs. These four scenarios lock the shapes `erun list` must
		// tell apart: a tenant's own aligned line, erun's own stock line, the
		// release-name-disagrees-with-image trap, and honestly not knowing.
		{"runtime_version_line_aligned_tenant_line", testListRuntimeVersionLineAlignedTenantLine},
		{"runtime_version_line_stock_erun_line", testListRuntimeVersionLineStockErunLine},
		{"runtime_version_line_release_name_disagrees_with_image", testListRuntimeVersionLineReleaseNameDisagreesWithImage},
		{"runtime_version_line_undetermined_on_malformed_recorded_image", testListRuntimeVersionLineUndeterminedOnMalformedRecordedImage},
		{"erun_version_reads_off_a_stated_runtime_chart_reference", testListErunVersionReadsOffAStatedRuntimeChartReference},

		// The undetermined shape (a deployed env whose deploy never recorded a
		// resolved image -- predates this feature, or went through a repo-local
		// runtime chart whose own values decide the image) is already covered by
		// every plain SeedTenantEnv scenario above, e.g. with_seeded_tenant_env:
		// it never sets runtimerunningimage, so "line undetermined" is exactly
		// what its golden shows. Must never guess a line from the tenant name
		// instead.
		{"with_orchestrator_environment_roles", testListWithOrchestratorEnvironmentRoles},
		{"with_orchestrator_runtime_role_on_a_runtime_type_environment", testListWithOrchestratorRuntimeRoleOnARuntimeTypeEnvironment},
		{"with_orchestrator_pairing_invalid_before_the_role_gate_existed", testListWithOrchestratorPairingInvalidBeforeTheRoleGateExisted},
		{"with_orchestrator_directories", testListWithOrchestratorDirectories},
		{"corrupted_env_config_errors", testListCorruptedEnvConfigErrors},
		{"corrupted_tenant_config_errors", testListCorruptedTenantConfigErrors},
		{"tenant_api_url_and_envless_tenant", testListTenantAPIURLAndEnvlessTenant},
		{"explicit_runtime_type", testListExplicitRuntimeType},
		{"legacy_remote_yaml_migrates_to_runtime", testListLegacyRemoteYAMLMigratesToRuntime},
		{"multi_tenant_with_multiple_envs", testListMultiTenantWithMultipleEnvs},
		{"cwd_overrides_default_tenant", testListCwdOverridesDefaultTenant},
		{"sshd_enabled_but_host_alias_missing", testListSSHDEnabledButHostAliasMissing},
		{"sshd_enabled_and_host_alias_configured", testListSSHDEnabledAndHostAliasConfigured},
		{"orphaned_host_alias_blocks_are_reported", testListOrphanedHostAliasBlocksAreReported},
		{"with_cloud_providers_and_runtime_details", testListWithCloudProvidersAndRuntimeDetails},
		{"with_claude_config", testListWithClaudeConfig},

		// The sizing recommendation's four directions, driven from a retained
		// history rather than from a live container. The history is the recommender's
		// whole input, so seeding it is what makes each direction reachable — a real
		// 26-hour observation window is not something a scenario can wait for, and
		// the shrink gate exists precisely to require one.
		//
		// Each seeds an env whose declared runtimepod is deliberately absent, which
		// is the in-pod reality: the chart injects the container's limits and the
		// pod's own config never learns them. The lines must therefore score against
		// the cgroup limit in the history, not against the defaults an empty
		// runtimepod normalizes to.
		{"runtime_sizing_lowers_memory_on_a_long_quiet_window", testListRuntimeSizingLowersMemoryOnALongQuietWindow},
		{"runtime_sizing_raises_memory_on_an_oom_kill", testListRuntimeSizingRaisesMemoryOnAnOOMKill},
		{"runtime_sizing_raises_cpu_on_sustained_throttling", testListRuntimeSizingRaisesCPUOnSustainedThrottling},
		{"runtime_sizing_raises_memory_when_the_peak_nears_the_limit", testListRuntimeSizingRaisesMemoryWhenThePeakNearsTheLimit},
		{"runtime_sizing_bounds_a_raise_by_the_namespace_quota", testListRuntimeSizingBoundsARaiseByTheNamespaceQuota},
		{"runtime_sizing_holds_cpu_on_tolerable_throttling", testListRuntimeSizingHoldsCPUOnTolerableThrottling},
		{"runtime_sizing_withholds_a_shrink_on_a_short_window", testListRuntimeSizingWithholdsAShrinkOnAShortWindow},
	} {
		t.Run(scenario.name, scenario.run)
	}
}

func testListHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"list", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/help", normalize.Apply(result.Combined))
}

func testListEmptyConfig(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "list/empty_config", normalize.Apply(result.Combined))
}

func testListWithSeededTenantEnv(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/with_seeded_tenant_env", normalize.Apply(result.Combined))
}

func testListWithSeededHostEnv(t *testing.T) {
	// A host env lists like any other environment, with its type named
	// plainly — it is not shown as a pod that failed to start.
	setup := env.New(t)
	fixture.SeedHostTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/with_seeded_host_env", normalize.Apply(result.Combined))
}

func testListPlatformAccountEnvShowsEnabled(t *testing.T) {
	// An env flagged platformaccount:true surfaces `platform-account: enabled`
	// in its detail block, so an operator can see the env holds cluster-admin.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"platformaccount: true\n")
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/platform_account_env_shows_enabled", normalize.Apply(result.Combined))
}

func testListCorruptedEnvConfigErrors(t *testing.T) {
	// A corrupted env config.yaml must fail list outright, not be silently skipped.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	mustWrite(t, filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml"), "{{{ not yaml")
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a corrupted env config, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "list/corrupted_env_config_errors", normalize.Apply(result.Combined))
}

func testListCorruptedTenantConfigErrors(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	mustWrite(t, filepath.Join(setup.ConfigHome, "erun", "team", "config.yaml"), "{{{ not yaml")
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a corrupted tenant config, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "list/corrupted_tenant_config_errors", normalize.Apply(result.Combined))
}

func testListTenantAPIURLAndEnvlessTenant(t *testing.T) {
	// A tenant with a config but no env subdirectories shows "environments: none", not an empty list.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	tenantCfg := filepath.Join(setup.ConfigHome, "erun", "team", "config.yaml")
	mustWrite(t, tenantCfg,
		"projectroot: "+setup.Cwd+"\n"+
			"name: team\n"+
			"defaultenvironment: dev\n"+
			"api_url: https://api.example/erun\n")
	envlessDir := filepath.Join(setup.ConfigHome, "erun", "envless")
	if err := os.MkdirAll(envlessDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", envlessDir, err)
	}
	mustWrite(t, filepath.Join(envlessDir, "config.yaml"), "name: envless\nprojectroot: "+setup.Home+"\n")
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/tenant_api_url_and_envless_tenant", normalize.Apply(result.Combined))
}

func testListExplicitRuntimeType(t *testing.T) {
	// An explicit `type:` must win over the legacy fallback in the resolver.
	setup := env.New(t)
	seedExplicitTypeEnv(t, setup, "team", "prod", "runtime")
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/explicit_runtime_type", normalize.Apply(result.Combined))
}

func testListLegacyRemoteYAMLMigratesToRuntime(t *testing.T) {
	// A legacy remote=true env with no build-here signal migrates to Type=runtime on
	// read, preserving the remote-worktree / no-local-build semantics of the retired
	// remote/snapshot fields.
	setup := env.New(t)
	fixture.SeedLegacyRemoteTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/legacy_remote_yaml_migrates_to_runtime", normalize.Apply(result.Combined))
}

func testListMultiTenantWithMultipleEnvs(t *testing.T) {
	// Distinct port ranges are assigned per tenant by tenant index.
	setup := env.New(t)
	seedListMultiTenant(t, setup)
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/multi_tenant_with_multiple_envs", normalize.Apply(result.Combined))
}

func testListCwdOverridesDefaultTenant(t *testing.T) {
	// cwd under a non-default tenant's project root marks that tenant `effective`, overriding the configured default.
	setup := env.New(t)
	seedListMultiTenant(t, setup)
	// cwd resolution walks up to a git root, so tenant-b's project root must be a real git repo.
	tenantBPath := filepath.Join(setup.Home, "git", "tenant-b")
	fixture.SeedGitRepo(t, tenantBPath)
	nested := filepath.Join(tenantBPath, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: nested, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/cwd_overrides_default_tenant", normalize.Apply(result.Combined))
}

func testListWithCloudProvidersAndRuntimeDetails(t *testing.T) {
	// The aws stub fails `sts get-caller-identity` deterministically; without it the
	// developer's real aws CLI would shape the status line and drift the golden between machines.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	root := filepath.Join(setup.ConfigHome, "erun")
	mustWrite(t, filepath.Join(root, "config.yaml"),
		"defaulttenant: team\n"+
			"cloudproviders:\n"+
			"  - alias: test-user+123456789012@aws\n"+
			"    provider: aws\n"+
			"    username: test-user\n"+
			"    accountid: \"123456789012\"\n"+
			"    profile: test-profile\n",
	)
	mustWrite(t, filepath.Join(root, "team", "dev", "config.yaml"),
		"name: dev\n"+
			"repopath: "+setup.Cwd+"\n"+
			"kubernetescontext: test-context\n"+
			"containerregistry: registry.example/test\n"+
			"runtimeversion: 1.0.0\n"+
			"cloudprovideralias: test-user+123456789012@aws\n"+
			"runtimepod:\n"+
			"  cpu: \"2\"\n"+
			"  memory: 4Gi\n"+
			"idle:\n"+
			"  timeout: 10m\n"+
			"  workinghours: 09:00-18:00\n"+
			"  timezone: Europe/Riga\n"+
			"  idletrafficbytes: 2048\n",
	)
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "aws", fixture.StubBinarySpec{
		Stderr:   "The config profile (test-profile) could not be found",
		ExitCode: 255,
	})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/with_cloud_providers_and_runtime_details", normalize.Apply(result.Combined))
}

func testListWithClaudeConfig(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envCfg := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	mustWrite(t, envCfg,
		"name: dev\n"+
			"repopath: "+setup.Cwd+"\n"+
			"kubernetescontext: test-context\n"+
			"containerregistry: registry.example/test\n"+
			"runtimeversion: 1.0.0\n"+
			"claude:\n"+
			"  usemantle: true\n"+
			"  usebedrock: true\n"+
			"  models: [opus, sonnet, haiku]\n"+
			"  maxoutputtokens: 16384\n",
	)
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/with_claude_config", normalize.Apply(result.Combined))
}

// seedExplicitTypeEnv is shared by list and delete scenarios that assert the resolver honors an explicit `type:`.
func seedExplicitTypeEnv(t testing.TB, setup env.Setup, tenant, environment, envType string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, tenant)
	envDir := filepath.Join(tenantDir, environment)
	for _, dir := range []string{root, tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	repoPath := filepath.Join(setup.Home, "git", tenant)
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo %s: %v", repoPath, err)
	}
	mustWrite(t, filepath.Join(root, "config.yaml"), "defaulttenant: "+tenant+"\n")
	mustWrite(t, filepath.Join(tenantDir, "config.yaml"),
		"projectroot: "+repoPath+"\n"+
			"name: "+tenant+"\n"+
			"defaultenvironment: "+environment+"\n",
	)
	mustWrite(t, filepath.Join(envDir, "config.yaml"),
		"name: "+environment+"\n"+
			"type: "+envType+"\n"+
			"kubernetescontext: test-context\n"+
			"containerregistry: registry.example/test\n"+
			"runtimeversion: 1.0.0\n",
	)
}

func seedListMultiTenant(t testing.TB, setup env.Setup) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	mustWrite(t, filepath.Join(root, "config.yaml"), "defaulttenant: tenant-a\n")
	tenantAPath := filepath.Join(setup.Home, "git", "tenant-a")
	tenantBPath := filepath.Join(setup.Home, "git", "tenant-b")
	for _, dir := range []string{tenantAPath, tenantBPath} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir repo %s: %v", dir, err)
		}
	}
	writeListTenantConfig(t, root, "tenant-a", "local", tenantAPath)
	writeListTenantConfig(t, root, "tenant-b", "dev", tenantBPath)
	writeListEnvConfig(t, root, "tenant-a", "local", tenantAPath, "cluster-local", "")
	writeListEnvConfig(t, root, "tenant-a", "prod", tenantAPath, "cluster-prod", "")
	writeListEnvConfig(t, root, "tenant-b", "dev", tenantBPath, "cluster-b", "")
}

func writeListTenantConfig(t testing.TB, root, tenant, defaultEnv, projectRoot string) {
	t.Helper()
	tenantDir := filepath.Join(root, tenant)
	if err := os.MkdirAll(tenantDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", tenantDir, err)
	}
	mustWrite(t, filepath.Join(tenantDir, "config.yaml"),
		"name: "+tenant+"\n"+
			"projectroot: "+projectRoot+"\n"+
			"defaultenvironment: "+defaultEnv+"\n",
	)
}

func writeListEnvConfig(t testing.TB, root, tenant, environment, repoPath, kubeContext, registry string) {
	t.Helper()
	envDir := filepath.Join(root, tenant, environment)
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", envDir, err)
	}
	body := "name: " + environment + "\n" +
		"repopath: " + repoPath + "\n" +
		"kubernetescontext: " + kubeContext + "\n"
	if registry != "" {
		body += "containerregistry: " + registry + "\n"
	}
	mustWrite(t, filepath.Join(envDir, "config.yaml"), body)
}

func mustWrite(t testing.TB, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

type usageHistorySpec struct {
	windowHours     int
	samples         int
	peakMemoryBytes int64
	limitBytes      int64
	oomKills        int64
	quotaMilli      int64
	periods         int64
	throttled       int64
	peakCPUMilli    int64
}

// seedUsageHistory writes the retained usage store directly. The on-disk shape
// is the contract between the in-pod monitor that writes it and every reader, so
// spelling the JSON out here pins that shape rather than round-tripping through
// the same structs it is meant to check.
func seedUsageHistory(t testing.TB, setup env.Setup, tenant, environment string, spec usageHistorySpec) {
	t.Helper()
	dir := filepath.Join(setup.CacheHome, "erun", "activity", tenant, environment)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	// Fixed instants, so the observed window is a property of the fixture rather
	// than of when the suite happened to run.
	first := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	last := first.Add(time.Duration(spec.windowHours)*time.Hour + 12*time.Minute)
	sample := fmt.Sprintf(
		`{"cpu":{"quotaCores":%g,"usageUsec":%d,"periods":%d,"throttledPeriods":%d},"memory":{"limitBytes":%d,"currentBytes":%d,"peakBytes":%d,"oomKills":%d}}`,
		float64(spec.quotaMilli)/1000, spec.periods*100, spec.periods, spec.throttled,
		spec.limitBytes, spec.peakMemoryBytes/2, spec.peakMemoryBytes, spec.oomKills)
	samples := make([]string, 0, spec.samples)
	for i := 0; i < spec.samples; i++ {
		samples = append(samples, sample)
	}
	body := fmt.Sprintf(
		`{"firstObservedAt":%q,"lastObservedAt":%q,"observedPeakMemoryBytes":%d,"observedOomKills":%d,"observedPeakCpuMilli":%d,"observedPeriods":%d,"observedThrottledPeriods":%d,"samples":[%s]}`,
		first.Format(time.RFC3339Nano), last.Format(time.RFC3339Nano), spec.peakMemoryBytes,
		spec.oomKills, spec.peakCPUMilli, spec.periods, spec.throttled, strings.Join(samples, ","))
	mustWrite(t, filepath.Join(dir, "usage-history.json"), body+"\n")
}

type orchestratorEnvSeed struct {
	tenant      string
	environment string
	directory   string
	role        string
}

type orchestratorSeed struct {
	id           string
	name         string
	environments []orchestratorEnvSeed
	// directories are the orchestrator's own: paths that belong to no environment.
	// A seed may carry these and no environment at all, which is a complete
	// definition.
	directories []string
	// alias is the erun platform alias the orchestrator declares as its own.
	// Empty is the default and the common case, and writes no key at all, so
	// goldens seeded before this field existed are unchanged by it.
	alias string
	// useGateway, model and effort are the launch settings. Their zero values
	// are the defaults and write no key, for the same reason as alias.
	useGateway bool
	model      string
	effort     string
}

// seedOrchestratorsWithEnvRoles appends a persisted orchestrators list,
// including each linked environment's role, to the isolated root config. It
// writes every orchestrator in one "orchestrators:" block (unlike
// whip_test.go's seedOrchestrator, calling it more than once would overwrite
// rather than append, since YAML only keeps the last of a repeated top-level
// key).
func seedOrchestratorsWithEnvRoles(t testing.TB, setup env.Setup, orchestrators []orchestratorSeed) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	path := filepath.Join(root, "config.yaml")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read root config %s: %v", path, err)
	}
	var sb strings.Builder
	sb.WriteString("orchestrators:\n")
	for _, orchestrator := range orchestrators {
		writeOrchestratorSeed(&sb, orchestrator)
	}
	if err := os.WriteFile(path, append(existing, []byte(sb.String())...), 0o644); err != nil {
		t.Fatalf("write root config %s: %v", path, err)
	}
}

// writeOrchestratorSeed writes one orchestrators entry, omitting every
// optional key left at its default.
func writeOrchestratorSeed(sb *strings.Builder, orchestrator orchestratorSeed) {
	sb.WriteString("  - id: " + orchestrator.id + "\n")
	sb.WriteString("    name: " + orchestrator.name + "\n")
	if orchestrator.alias != "" {
		sb.WriteString("    alias: " + orchestrator.alias + "\n")
	}
	if orchestrator.useGateway {
		sb.WriteString("    usegateway: true\n")
	}
	if orchestrator.model != "" {
		sb.WriteString("    model: " + orchestrator.model + "\n")
	}
	if orchestrator.effort != "" {
		sb.WriteString("    effort: " + orchestrator.effort + "\n")
	}
	writeOrchestratorSeedLinks(sb, orchestrator)
}

// writeOrchestratorSeedLinks writes an orchestrators entry's linked
// environments and own directories, each block only when it has entries.
func writeOrchestratorSeedLinks(sb *strings.Builder, orchestrator orchestratorSeed) {
	if len(orchestrator.environments) > 0 {
		sb.WriteString("    environments:\n")
		for _, e := range orchestrator.environments {
			sb.WriteString("      - tenant: " + e.tenant + "\n")
			sb.WriteString("        environment: " + e.environment + "\n")
			sb.WriteString("        directory: " + e.directory + "\n")
			if e.role != "" {
				sb.WriteString("        role: " + e.role + "\n")
			}
		}
	}
	if len(orchestrator.directories) > 0 {
		sb.WriteString("    directories:\n")
		for _, directory := range orchestrator.directories {
			sb.WriteString("      - directory: " + directory + "\n")
		}
	}
}
