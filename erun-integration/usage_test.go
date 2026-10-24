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

func TestUsage(t *testing.T) {
	t.Parallel()
	t.Run("help", testUsageHelp)

	t.Run("dry_run", testUsageDryRun)

	t.Run("dry_run_custom_interval", testUsageDryRunCustomInterval)

	t.Run("dry_run_missing_tenant", testUsageDryRunMissingTenant)

	t.Run("real_run_reports_cgroup_v2_usage", testUsageRealRunReportsCgroupV2Usage)

	t.Run("real_run_reports_the_scheduler_request", testUsageRealRunReportsTheSchedulerRequest)

	t.Run("real_run_runtime_env_omits_builds_caveat", testUsageRealRunRuntimeEnvOmitsBuildsCaveat)

	t.Run("real_run_local_agent_env_states_the_builds_caveat", testUsageRealRunLocalAgentEnvStatesTheBuildsCaveat)

	t.Run("real_run_warns_when_the_sidecars_throttling_is_material", testUsageRealRunWarnsWhenTheSidecarsThrottlingIsMaterial)

	t.Run("real_run_cgroup_v1_reports_unavailable_not_an_error", testUsageRealRunCgroupV1ReportsUnavailableNotAnError)

	t.Run("real_run_warns_near_the_memory_limit", testUsageRealRunWarnsNearTheMemoryLimit)

	t.Run("real_run_disk_warning_names_the_node_shared_scope", testUsageRealRunDiskWarningNamesTheNodeSharedScope)

	t.Run("real_run_unreadable_peak_renders_unavailable_not_zero", testUsageRealRunUnreadablePeakRendersUnavailableNotZero)

	t.Run("real_run_unreadable_peak_json_omits_peak_fields", testUsageRealRunUnreadablePeakJSONOmitsPeakFields)

	t.Run("real_run_dind_sidecar_usage_reported_separately", testUsageRealRunDinDSidecarUsageReportedSeparately)

	t.Run("real_run_genuine_zero_peak_renders_unchanged", testUsageRealRunGenuineZeroPeakRendersUnchanged)
}

func testUsageHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"usage", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "usage/help", normalize.Apply(result.Combined))
}

func testUsageDryRun(t *testing.T) {
	// team/dev is build-capable, so the dind sidecar exec is traced too.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"usage", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "usage/dry_run", normalize.Apply(result.Combined))
}

func testUsageDryRunCustomInterval(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"usage", "--interval", "2.5", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "usage/dry_run_custom_interval", normalize.Apply(result.Combined))
}

func testUsageDryRunMissingTenant(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"usage", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit with no tenant configured, got 0: %s", result.Combined)
	}
	golden.Equal(t, "usage/dry_run_missing_tenant", normalize.Apply(result.Combined))
}

// The reading must come from the container's cgroup v2 accounting, not metrics-server.
func testUsageRealRunReportsCgroupV2Usage(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	stubUsageKubectlExec(t, stubs, []string{
		"cgroup_type=cgroup2fs",
		"memory_current=413589504",
		"memory_max=2147483648",
		"memory_peak=1027301376",
		"memory_oom_kill=0",
		"cpu_max=100000 100000",
		"cpu_usage_before=581511501",
		"cpu_usage_after=581611501",
		"cpu_time_before_ns=1000000000",
		"cpu_time_after_ns=2000000000",
		"cpu_periods=376556",
		"cpu_throttled_periods=425",
		"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
		"disk_own_used_kb=44040192",
	})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"usage", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	// normalize masks any /home/<user> as <HOME>, so assert the literal mount once.
	if !strings.Contains(result.Combined, `"mount": "/home/erun"`) {
		t.Fatalf("expected the watched mount to be the literal /home/erun, got:\n%s", result.Combined)
	}
	// 425 of 376,556 cumulative periods is 0.11%: residue, so no throttling warning.
	if !strings.Contains(result.Combined, `"periods": 376556`) || !strings.Contains(result.Combined, `"throttledPeriods": 425`) {
		t.Fatalf("expected cpu.periods/throttledPeriods parsed from cpu.stat, got:\n%s", result.Combined)
	}
	// The mount figures are the node's; ownUsedBytes is this environment's.
	if !strings.Contains(result.Combined, `"nodeShared": true`) || !strings.Contains(result.Combined, `"ownUsedBytes": 45097156608`) {
		t.Fatalf("expected nodeShared and ownUsedBytes parsed from du, got:\n%s", result.Combined)
	}
	golden.Equal(t, "usage/real_run_reports_cgroup_v2_usage", normalize.Apply(result.Combined))
}

// The request lives in the pod spec, not a cgroup file; a limit reserves nothing.
func testUsageRealRunReportsTheSchedulerRequest(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	stubUsageKubectlExec(t, stubs, []string{
		"cgroup_type=cgroup2fs",
		"memory_current=413589504",
		"memory_max=28991029248",
		"memory_peak=1027301376",
		"memory_oom_kill=0",
		"cpu_max=1400000 100000",
		"cpu_usage_before=581511501",
		"cpu_usage_after=581611501",
		"cpu_time_before_ns=1000000000",
		"cpu_time_after_ns=2000000000",
		"cpu_periods=376556",
		"cpu_throttled_periods=425",
		"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
		"disk_own_used_kb=44040192",
	})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	jsonRun := erun.Run(t, []string{"usage", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if jsonRun.ExitCode != 0 {
		t.Fatalf("exit %d: %s", jsonRun.ExitCode, jsonRun.Combined)
	}
	// Cores and millicores normalize alike; the reservation sums requests, not limits.
	for _, want := range []string{
		`"cpuMilli": 250`,
		`"memoryBytes": 1073741824`,
		`"cpuMilli": 500`,
		`"memoryBytes": 2147483648`,
	} {
		if !strings.Contains(jsonRun.Combined, want) {
			t.Fatalf("expected %s parsed from the pod spec, got:\n%s", want, jsonRun.Combined)
		}
	}
	textRun := erun.Run(t, []string{"usage"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if textRun.ExitCode != 0 {
		t.Fatalf("exit %d: %s", textRun.ExitCode, textRun.Combined)
	}
	golden.Equal(t, "usage/real_run_reports_the_scheduler_request", normalize.Apply(textRun.Combined))
}

// A runtime env has no dind sidecar, so no "excludes builds" caveat.
func testUsageRealRunRuntimeEnvOmitsBuildsCaveat(t *testing.T) {
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnv(t, setup, "team", "prod")
	stubs := setup.Cwd + "/stubs"
	stubUsageKubectlExec(t, stubs, []string{
		"cgroup_type=cgroup2fs",
		"memory_current=413589504",
		"memory_max=2147483648",
		"memory_peak=1027301376",
		"memory_oom_kill=0",
		"cpu_max=100000 100000",
		"cpu_usage_before=581511501",
		"cpu_usage_after=581611501",
		"cpu_time_before_ns=1000000000",
		"cpu_time_after_ns=2000000000",
		"cpu_periods=376556",
		"cpu_throttled_periods=425",
		"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
		"disk_own_used_kb=44040192",
	})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"usage", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "excludesBuilds") {
		t.Fatalf("a runtime-type env has no dind sidecar and must not report excludesBuilds, got:\n%s", result.Combined)
	}
	golden.Equal(t, "usage/real_run_runtime_env_omits_builds_caveat", normalize.Apply(result.Combined))
}

// Builds run in the dind sidecar's separate cgroup, so the reading must say so.
func testUsageRealRunLocalAgentEnvStatesTheBuildsCaveat(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	stubUsageKubectlExec(t, stubs, []string{
		"cgroup_type=cgroup2fs",
		"memory_current=413589504",
		"memory_max=2147483648",
		"memory_peak=1027301376",
		"memory_oom_kill=0",
		"cpu_max=100000 100000",
		"cpu_usage_before=581511501",
		"cpu_usage_after=581611501",
		"cpu_time_before_ns=1000000000",
		"cpu_time_after_ns=2000000000",
		"cpu_periods=376556",
		"cpu_throttled_periods=425",
		"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
		"disk_own_used_kb=44040192",
	})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"usage"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "erun-dind sidecar") {
		t.Fatalf("a build-capable env must state the builds-excluded caveat, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "Disk (node, shared):") || !strings.Contains(result.Combined, "this environment's own usage:") {
		t.Fatalf("expected the node-shared disk label and the own-usage line, got:\n%s", result.Combined)
	}
	golden.Equal(t, "usage/real_run_local_agent_env_states_the_builds_caveat", normalize.Apply(result.Combined))
}

// Only the sidecar is materially throttled (4,000 of 20,000 periods); the runtime stays
// at 0.11%, so the readings differ only in what decides the verdict.
func testUsageRealRunWarnsWhenTheSidecarsThrottlingIsMaterial(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	stubUsageKubectlExecPerContainer(t, stubs,
		[]string{
			"cgroup_type=cgroup2fs",
			"memory_current=104857600",
			"memory_max=24696061952",
			"memory_peak=104857600",
			"memory_oom_kill=0",
			"cpu_max=1200000 100000",
			"cpu_usage_before=1000000",
			"cpu_usage_after=1003000",
			"cpu_time_before_ns=1000000000",
			"cpu_time_after_ns=2000000000",
			"cpu_periods=376556",
			"cpu_throttled_periods=425",
			"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
			"disk_own_used_kb=44040192",
		},
		[]string{
			"cgroup_type=cgroup2fs",
			"memory_current=2040109465",
			"memory_max=15032385536",
			"memory_peak=3435973836",
			"memory_oom_kill=0",
			"cpu_max=400000 100000",
			"cpu_usage_before=1000000",
			"cpu_usage_after=2900000",
			"cpu_time_before_ns=1000000000",
			"cpu_time_after_ns=2000000000",
			"cpu_periods=20000",
			"cpu_throttled_periods=4000",
			"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
		},
	)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"usage"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "the build was throttled in 4000 of 20000 cgroup periods") {
		t.Fatalf("a materially throttled sidecar must still be named as starvation, got:\n%s", result.Combined)
	}
	golden.Equal(t, "usage/real_run_warns_when_the_sidecars_throttling_is_material", normalize.Apply(result.Combined))
}

func testUsageRealRunCgroupV1ReportsUnavailableNotAnError(t *testing.T) {
	// Without cgroup v2 the command still succeeds, reporting CPU and memory unavailable.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	stubUsageKubectlExec(t, stubs, []string{
		"cgroup_type=tmpfs",
		"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
	})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"usage", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	// Missing own usage must report unavailable, not zero.
	if strings.Contains(result.Combined, `"ownUsedBytes"`) || strings.Contains(result.Combined, `"ownUsageObserved"`) {
		t.Fatalf("an unread own-usage figure must omit both ownUsedBytes and ownUsageObserved, got:\n%s", result.Combined)
	}
	golden.Equal(t, "usage/real_run_cgroup_v1_reports_unavailable_not_an_error", normalize.Apply(result.Combined))
}

func testUsageRealRunWarnsNearTheMemoryLimit(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	stubUsageKubectlExec(t, stubs, []string{
		"cgroup_type=cgroup2fs",
		"memory_current=1932735283", // ~90% of 2048Mi
		"memory_max=2147483648",
		"memory_peak=2100000000", // ~98% peak: also crosses the peak threshold
		"memory_oom_kill=1",
		"cpu_max=100000 100000",
		"cpu_usage_before=0",
		"cpu_usage_after=0",
		"cpu_time_before_ns=1000000000",
		"cpu_time_after_ns=2000000000",
		"disk_workspace=overlay 198234112 99999999 99117056 95% /home/erun",
		"disk_own_used_kb=10000000",
	})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"usage", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "usage/real_run_warns_near_the_memory_limit", normalize.Apply(result.Combined))
	if !strings.Contains(result.Combined, "\"warnings\"") {
		t.Fatalf("expected a warnings field in the JSON output, got:\n%s", result.Combined)
	}
}

// The disk warning names the node-shared scope so operators do not clean the wrong env.
func testUsageRealRunDiskWarningNamesTheNodeSharedScope(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	stubUsageKubectlExec(t, stubs, []string{
		"cgroup_type=cgroup2fs",
		"memory_current=413589504",
		"memory_max=2147483648",
		"memory_peak=1027301376",
		"memory_oom_kill=0",
		"cpu_max=100000 100000",
		"cpu_usage_before=0",
		"cpu_usage_after=0",
		"cpu_time_before_ns=1000000000",
		"cpu_time_after_ns=2000000000",
		"disk_workspace=overlay 198234112 178410701 19823411 90% /home/erun",
		"disk_own_used_kb=10000000",
	})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"usage"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "node disk is at 90%") || !strings.Contains(result.Combined, "shared with every environment on this node") {
		t.Fatalf("expected the disk warning to name the node-shared scope, got:\n%s", result.Combined)
	}
	golden.Equal(t, "usage/real_run_disk_warning_names_the_node_shared_scope", normalize.Apply(result.Combined))
}

// An unreadable memory.peak must render unavailable, not "peak 0B".
func testUsageRealRunUnreadablePeakRendersUnavailableNotZero(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	stubUsageKubectlExec(t, stubs, []string{
		"cgroup_type=cgroup2fs",
		"memory_current=413589504",
		"memory_max=2147483648",
		"memory_peak=",
		"memory_oom_kill=0",
		"cpu_max=100000 100000",
		"cpu_usage_before=0",
		"cpu_usage_after=0",
		"cpu_time_before_ns=1000000000",
		"cpu_time_after_ns=2000000000",
		"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
	})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"usage"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "peak 0B") {
		t.Fatalf("an unreadable peak must never render as a measured 0B, got:\n%s", result.Combined)
	}
	golden.Equal(t, "usage/real_run_unreadable_peak_renders_unavailable_not_zero", normalize.Apply(result.Combined))
}

// JSON must distinguish unreadable peak from zero, and not warn from an untaken reading.
func testUsageRealRunUnreadablePeakJSONOmitsPeakFields(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	stubUsageKubectlExec(t, stubs, []string{
		"cgroup_type=cgroup2fs",
		"memory_current=413589504",
		"memory_max=2147483648",
		"memory_peak=",
		"memory_oom_kill=0",
		"cpu_max=100000 100000",
		"cpu_usage_before=0",
		"cpu_usage_after=0",
		"cpu_time_before_ns=1000000000",
		"cpu_time_after_ns=2000000000",
		"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
	})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"usage", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "peakBytes") || strings.Contains(result.Combined, "peakObserved") {
		t.Fatalf("an unreadable peak must omit both peakBytes and peakObserved, got:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "memory.peak reached") {
		t.Fatalf("an unreadable peak must not warn from a percentage it never computed, got:\n%s", result.Combined)
	}
	golden.Equal(t, "usage/real_run_unreadable_peak_json_omits_peak_fields", normalize.Apply(result.Combined))
}

// The runtime can read idle while the dind sidecar nears its memory limit; each must
// report separately.
func testUsageRealRunDinDSidecarUsageReportedSeparately(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	stubUsageKubectlExecPerContainer(t, stubs,
		[]string{
			"cgroup_type=cgroup2fs",
			"memory_current=104857600",
			"memory_max=24696061952",
			"memory_peak=104857600",
			"memory_oom_kill=0",
			"cpu_max=1200000 100000",
			"cpu_usage_before=1000000",
			"cpu_usage_after=1003000",
			"cpu_time_before_ns=1000000000",
			"cpu_time_after_ns=2000000000",
			"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
		},
		[]string{
			"cgroup_type=cgroup2fs",
			"memory_current=13421772800",
			"memory_max=15032385536",
			"memory_peak=13958643712",
			"memory_oom_kill=0",
			"cpu_max=400000 100000",
			"cpu_usage_before=1000000",
			"cpu_usage_after=2900000",
			"cpu_time_before_ns=1000000000",
			"cpu_time_after_ns=2000000000",
			"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
		},
	)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"usage", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, `"dind"`) {
		t.Fatalf("expected a dind reading in the JSON output for a build-capable env, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, `"currentBytes": 13421772800`) {
		t.Fatalf("expected the sidecar's own (busy) memory reading distinct from the runtime container's idle one, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "erun-dind: memory is at") {
		t.Fatalf("expected the sidecar's own near-limit memory to warn distinctly from the runtime container, got:\n%s", result.Combined)
	}
	golden.Equal(t, "usage/real_run_dind_sidecar_usage_reported_separately", normalize.Apply(result.Combined))
}

func testUsageRealRunGenuineZeroPeakRendersUnchanged(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	stubUsageKubectlExec(t, stubs, []string{
		"cgroup_type=cgroup2fs",
		"memory_current=0",
		"memory_max=2147483648",
		"memory_peak=0",
		"memory_oom_kill=0",
		"cpu_max=100000 100000",
		"cpu_usage_before=0",
		"cpu_usage_after=0",
		"cpu_time_before_ns=1000000000",
		"cpu_time_after_ns=2000000000",
		"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
	})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"usage"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "peak 0B") {
		t.Fatalf("a genuinely-zero, observed peak must still render as peak 0B, got:\n%s", result.Combined)
	}
	golden.Equal(t, "usage/real_run_genuine_zero_peak_renders_unchanged", normalize.Apply(result.Combined))
}

// stubUsagePodRequestsList mirrors the chart's fixed 0.25 CPU / 1024Mi requests against
// large limits: reservation 0.5 CPU / 2GiB.
const stubUsagePodRequestsList = `{"items": [{"metadata": {"name": "team-devops-7d9f-abcde"}, "spec": {"containers": [{"name": "erun-devops", "resources": {"limits": {"cpu": "14", "memory": "27Gi"}, "requests": {"cpu": "0.25", "memory": "1024Mi"}}}, {"name": "erun-dind", "resources": {"limits": {"cpu": "8", "memory": "20Gi"}, "requests": {"cpu": "0.25", "memory": "1024Mi"}}}], "initContainers": [{"name": "prepare-volumes", "resources": {"requests": {"cpu": "100m", "memory": "64Mi"}}}]}, "status": {"phase": "Running"}}]}`

// stubUsagePodRequestsArm matches argv with and without context/namespace.
func stubUsagePodRequestsArm() []string {
	return []string{
		`  *" get pods "*|"get pods "*)`,
		`    printf '%s\n' '` + stubUsagePodRequestsList + `'`,
		`    ;;`,
	}
}

// stubUsageKubectlExec answers every container's usage exec identically.
func stubUsageKubectlExec(t testing.TB, stubsDir string, lines []string) {
	t.Helper()
	body := make([]string, 0, len(lines)+8)
	body = append(body, `case "$*" in`)
	body = append(body, stubUsagePodRequestsArm()...)
	body = append(body, `  *" exec "*)`)
	for _, line := range lines {
		body = append(body, `    printf '%s\n' '`+line+`'`)
	}
	body = append(body, `    ;;`, `esac`, `exit 0`)
	fixture.StubBinaryWithScript(t, stubsDir, "kubectl", strings.Join(body, "\n"))
}

// stubUsageKubectlExecPerContainer answers the `-c erun-dind` exec with dindLines and
// other execs with runtimeLines.
func stubUsageKubectlExecPerContainer(t testing.TB, stubsDir string, runtimeLines, dindLines []string) {
	t.Helper()
	body := make([]string, 0, len(runtimeLines)+len(dindLines)+10)
	body = append(body, `case "$*" in`)
	body = append(body, stubUsagePodRequestsArm()...)
	body = append(body, `  *" -c erun-dind "*)`)
	for _, line := range dindLines {
		body = append(body, `    printf '%s\n' '`+line+`'`)
	}
	body = append(body, `    ;;`, `  *" exec "*)`)
	for _, line := range runtimeLines {
		body = append(body, `    printf '%s\n' '`+line+`'`)
	}
	body = append(body, `    ;;`, `esac`, `exit 0`)
	fixture.StubBinaryWithScript(t, stubsDir, "kubectl", strings.Join(body, "\n"))
}
