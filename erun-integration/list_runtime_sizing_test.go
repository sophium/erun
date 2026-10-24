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

func testListRuntimeSizingLowersMemoryOnALongQuietWindow(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedUsageHistory(t, setup, "team", "dev", usageHistorySpec{
		windowHours: 31, samples: 240,
		peakMemoryBytes: 12742377472, limitBytes: 24696061952,
		quotaMilli: 12000, periods: 376556, throttled: 0, peakCPUMilli: 4567,
	})
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/runtime_sizing_lowers_memory_on_a_long_quiet_window", normalize.Apply(result.Combined))
}

func testListRuntimeSizingRaisesMemoryOnAnOOMKill(t *testing.T) {
	// One kill outranks a comfortable peak and needs no observation window.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedUsageHistory(t, setup, "team", "dev", usageHistorySpec{
		windowHours: 0, samples: 2,
		peakMemoryBytes: 1073741824, limitBytes: 2147483648, oomKills: 1,
		quotaMilli: 1000, periods: 5000, throttled: 0, peakCPUMilli: 300,
	})
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/runtime_sizing_raises_memory_on_an_oom_kill", normalize.Apply(result.Combined))
}

func testListRuntimeSizingRaisesCPUOnSustainedThrottling(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedUsageHistory(t, setup, "team", "dev", usageHistorySpec{
		windowHours: 31, samples: 240,
		peakMemoryBytes: 1073741824, limitBytes: 8589934592,
		quotaMilli: 2000, periods: 100000, throttled: 12000, peakCPUMilli: 1900,
	})
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/runtime_sizing_raises_cpu_on_sustained_throttling", normalize.Apply(result.Combined))
}

func testListRuntimeSizingRaisesMemoryWhenThePeakNearsTheLimit(t *testing.T) {
	// Sampling means the true peak is at least the high-water, so the raise needs
	// no kill first.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedUsageHistory(t, setup, "team", "dev", usageHistorySpec{
		windowHours: 5, samples: 30,
		peakMemoryBytes: 2040109465, limitBytes: 2147483648,
		quotaMilli: 1000, periods: 50000, throttled: 0, peakCPUMilli: 600,
	})
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/runtime_sizing_raises_memory_when_the_peak_nears_the_limit", normalize.Apply(result.Combined))
}

func testListRuntimeSizingBoundsARaiseByTheNamespaceQuota(t *testing.T) {
	// A raise is clamped to the namespace quota less the dind sidecar's limit.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"namespacequota:\n  cpu: \"8\"\n  memory: 32Gi\n  storage: 80Gi\n")
	seedUsageHistory(t, setup, "team", "dev", usageHistorySpec{
		windowHours: 5, samples: 30,
		peakMemoryBytes: 21474836480, limitBytes: 21474836480, oomKills: 3,
		quotaMilli: 4000, periods: 50000, throttled: 0, peakCPUMilli: 1000,
	})
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/runtime_sizing_bounds_a_raise_by_the_namespace_quota", normalize.Apply(result.Combined))
}

func testListRuntimeSizingHoldsCPUOnTolerableThrottling(t *testing.T) {
	// Sub-threshold throttling must neither grow the environment nor license a shrink.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedUsageHistory(t, setup, "team", "dev", usageHistorySpec{
		windowHours: 31, samples: 240,
		peakMemoryBytes: 1027301376, limitBytes: 2147483648,
		quotaMilli: 4000, periods: 308631, throttled: 425, peakCPUMilli: 300,
	})
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/runtime_sizing_holds_cpu_on_tolerable_throttling", normalize.Apply(result.Combined))
}

func testListRuntimeSizingWithholdsAShrinkOnAShortWindow(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedUsageHistory(t, setup, "team", "dev", usageHistorySpec{
		windowHours: 1, samples: 120,
		peakMemoryBytes: 12742377472, limitBytes: 24696061952,
		quotaMilli: 12000, periods: 376556, throttled: 0, peakCPUMilli: 4567,
	})
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/runtime_sizing_withholds_a_shrink_on_a_short_window", normalize.Apply(result.Combined))
}
