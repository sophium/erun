package podruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/adrg/xdg"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// Each threshold must fire exactly at its boundary and stay silent below it.
func TestParseRuntimeUsageWarnings(t *testing.T) {
	req := config.ShellLaunchParams{Tenant: "frs", Environment: "local"}
	interval := time.Second

	t.Run("memory warning fires at the threshold and not below it", func(t *testing.T) {
		below := parseRuntimeUsage(req, cgroupMemoryFixture(84, 50), interval)
		if hasWarningContaining(below.Warnings, "memory is at") {
			t.Errorf("84%% should not warn, got %v", below.Warnings)
		}

		at := parseRuntimeUsage(req, cgroupMemoryFixture(85, 50), interval)
		if !hasWarningContaining(at.Warnings, "memory is at") {
			t.Errorf("85%% should warn, got %v", at.Warnings)
		}
	})

	t.Run("memory.peak warning fires independently of current usage", func(t *testing.T) {
		usage := parseRuntimeUsage(req, cgroupMemoryFixture(10, 96), interval)
		if !hasWarningContaining(usage.Warnings, "memory.peak reached") {
			t.Errorf("a 96%% peak should warn even with low current usage, got %v", usage.Warnings)
		}
	})

	t.Run("OOM kill warning does not fire when oom_kill could not be read", func(t *testing.T) {
		output := strings.Join([]string{
			"cgroup_type=cgroup2fs",
			"memory_current=52428800",
			"memory_max=2147483648",
			"memory_peak=52428800",
			"memory_oom_kill=",
		}, "\n")

		usage := parseRuntimeUsage(req, output, interval)
		if usage.Memory.OOMKillsObserved {
			t.Fatalf("expected OOMKillsObserved=false when memory.events is unreadable, got %+v", usage.Memory)
		}
		if hasWarningContaining(usage.Warnings, "OOM kill") {
			t.Errorf("an unread oom_kill counter must not report a confident zero, got %v", usage.Warnings)
		}
	})
}

func cgroupMemoryFixture(currentPercent, peakPercent int) string {
	const limit = int64(1000)
	current := limit * int64(currentPercent) / 100
	peak := limit * int64(peakPercent) / 100
	return strings.Join([]string{
		"cgroup_type=cgroup2fs",
		"memory_current=" + strconv.FormatInt(current, 10),
		"memory_max=" + strconv.FormatInt(limit, 10),
		"memory_peak=" + strconv.FormatInt(peak, 10),
		"memory_oom_kill=0",
	}, "\n")
}

func hasWarningContaining(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

// PeakBytes/OOMKills are omitempty, so only the Observed bits tell an unread
// value from a genuine zero.
func TestRuntimeMemoryUsageJSONDistinguishesUnreadableFromZero(t *testing.T) {
	unread, err := json.Marshal(config.RuntimeMemoryUsage{CurrentBytes: 100})
	if err != nil {
		t.Fatalf("marshal unread: %v", err)
	}
	if strings.Contains(string(unread), "peakObserved") || strings.Contains(string(unread), "peakBytes") {
		t.Errorf("an unread peak must omit both peakBytes and peakObserved, got %s", unread)
	}

	genuineZero, err := json.Marshal(config.RuntimeMemoryUsage{CurrentBytes: 100, PeakObserved: true})
	if err != nil {
		t.Fatalf("marshal genuine zero: %v", err)
	}
	if !strings.Contains(string(genuineZero), `"peakObserved":true`) {
		t.Errorf("a genuinely-zero, observed peak must carry peakObserved:true, got %s", genuineZero)
	}
	if strings.Contains(string(genuineZero), "peakBytes") {
		t.Errorf("a genuine zero peak still omits the zero-valued peakBytes key, got %s", genuineZero)
	}

	var roundTripped config.RuntimeMemoryUsage
	if err := json.Unmarshal(genuineZero, &roundTripped); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !roundTripped.PeakObserved || roundTripped.PeakBytes != 0 {
		t.Errorf("round trip = %+v, want PeakObserved=true, PeakBytes=0", roundTripped)
	}
}

// An unreachable sidecar must not cost the runtime-container reading.
func TestRunRuntimeUsageDindExecFailureFailsSoft(t *testing.T) {
	idleRuntimeReading := "cgroup_type=cgroup2fs\nmemory_current=100\nmemory_max=200\nmemory_peak=100\nmemory_oom_kill=0\ncpu_max=100000 100000\ncpu_usage_before=0\ncpu_usage_after=0\ncpu_time_before_ns=1000000000\ncpu_time_after_ns=2000000000\ndisk_workspace=overlay 100 50 50 50% /home/erun"
	req := config.ShellLaunchParams{Tenant: "erun", Environment: "build", Type: config.EnvironmentTypeLocalAgent}
	runner := func(_ config.ShellLaunchParams, container, _ string) (kube.RemoteCommandResult, error) {
		if container == build.RuntimeDindContainerName {
			return kube.RemoteCommandResult{}, errors.New("container not found")
		}
		return kube.RemoteCommandResult{Stdout: idleRuntimeReading}, nil
	}

	usage, err := RunRuntimeUsage(core.Context{}, runner, nil, req, RuntimeUsageParams{Interval: time.Second})
	if err != nil {
		t.Fatalf("a failed dind exec must not fail the whole call, got: %v", err)
	}
	if usage.Dind != nil {
		t.Fatalf("expected Dind=nil when the sidecar exec fails, got %+v", usage.Dind)
	}
	if !usage.ExcludesBuilds {
		t.Fatalf("expected ExcludesBuilds=true regardless of whether the sidecar could be read, got %+v", usage)
	}
	if usage.Memory.CurrentBytes != 100 {
		t.Errorf("expected the runtime container's own reading to still come through, got %+v", usage.Memory)
	}
}

// The runtime container's CPU is idle, so only the sidecar can raise a
// throttling warning.
func runtimeUsageRuntimeContainerReading() string {
	return strings.Join([]string{
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
	}, "\n")
}

func runtimeUsageDindReading(cpuMax string, periods, throttled int64) string {
	return strings.Join([]string{
		"cgroup_type=cgroup2fs",
		"memory_current=2040109465",
		"memory_max=15032385536",
		"memory_peak=3435973836",
		"memory_oom_kill=0",
		"cpu_max=" + cpuMax,
		"cpu_usage_before=1000000",
		"cpu_usage_after=2900000",
		"cpu_time_before_ns=1000000000",
		"cpu_time_after_ns=2000000000",
		fmt.Sprintf("cpu_periods=%d", periods),
		fmt.Sprintf("cpu_throttled_periods=%d", throttled),
		"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
	}, "\n")
}

// An empty want asserts the throttling warning's words are absent, not that
// the slice is empty.
func assertRuntimeUsageThrottleWarning(t *testing.T, dindReading, want string) {
	t.Helper()
	req := config.ShellLaunchParams{Tenant: "erun", Environment: "build", Type: config.EnvironmentTypeLocalAgent}
	runner := func(_ config.ShellLaunchParams, container, _ string) (kube.RemoteCommandResult, error) {
		if container == build.RuntimeDindContainerName {
			return kube.RemoteCommandResult{Stdout: dindReading}, nil
		}
		return kube.RemoteCommandResult{Stdout: runtimeUsageRuntimeContainerReading()}, nil
	}
	usage, err := RunRuntimeUsage(core.Context{}, runner, nil, req, RuntimeUsageParams{Interval: time.Second})
	if err != nil {
		t.Fatalf("RunRuntimeUsage: %v", err)
	}
	if want == "" {
		for _, warning := range usage.Warnings {
			if strings.Contains(warning, "throttled in") {
				t.Fatalf("expected no build-throttling warning, got %q", warning)
			}
		}
		return
	}
	for _, warning := range usage.Warnings {
		if warning == want {
			return
		}
	}
	t.Fatalf("expected the build-throttling warning\n  %q\ngot warnings %q", want, usage.Warnings)
}

// Busy-at-cap and starved-by-cap show the same utilisation; only
// nr_throttled/nr_periods separates them.
func TestRunRuntimeUsageWarnsWhenTheBuildIsThrottledByItsCap(t *testing.T) {
	const throttleWarning = "the build was throttled in 20000 of 20000 cgroup periods -- it is CPU-starved by its own cap, which reads as a running build making no progress, not an idle environment"
	const youngSidecarThrottleWarning = "the build was throttled in 9999 of 9999 cgroup periods -- it is CPU-starved by its own cap, which reads as a running build making no progress, not an idle environment"

	cases := []struct {
		name        string
		dindReading string
		wantWarning string
	}{
		{
			name:        "a build throttled in every period",
			dindReading: runtimeUsageDindReading("400000 100000", 20000, 20000),
			wantWarning: throttleWarning,
		},
		{
			// The ratio is lifetime-total: a rebuild pinned at its cap looks
			// like this for its first ~17 minutes, so the sizing floor must not
			// silence it.
			name:        "a build throttled in every period under the sizing floor",
			dindReading: runtimeUsageDindReading("400000 100000", 9999, 9999),
			wantWarning: youngSidecarThrottleWarning,
		},
		{
			name:        "a build working at its cap without being throttled",
			dindReading: runtimeUsageDindReading("400000 100000", 20000, 0),
			wantWarning: "",
		},
		{
			name:        "no scheduling periods observed",
			dindReading: runtimeUsageDindReading("400000 100000", 0, 0),
			wantWarning: "",
		},
		{
			name:        "an unreadable quota with throttled periods behind it",
			dindReading: runtimeUsageDindReading("max 100000", 200, 200),
			wantWarning: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertRuntimeUsageThrottleWarning(t, tc.dindReading, tc.wantWarning)
		})
	}
}

// nr_periods and nr_throttled are lifetime-cumulative, so a tiny throttle ratio
// is residue, not starvation.
func TestRunRuntimeUsageStaysSilentWhenTheSidecarsThrottlingIsImmaterial(t *testing.T) {
	cases := []struct {
		name        string
		dindReading string
	}{
		{
			name:        "an immaterial throttle ratio on a second environment",
			dindReading: runtimeUsageDindReading("400000 100000", 17479, 6),
		},
		{
			name:        "a build throttled just under the material ratio",
			dindReading: runtimeUsageDindReading("400000 100000", 20000, 998),
		},
		{
			name:        "a total ratio one period under the build floor",
			dindReading: runtimeUsageDindReading("400000 100000", 599, 599),
		},
		{
			// A sidecar seconds old is pinned because it is still starting up.
			name:        "every period throttled but too few periods to read",
			dindReading: runtimeUsageDindReading("400000 100000", 200, 200),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertRuntimeUsageThrottleWarning(t, tc.dindReading, "")
		})
	}
}

func TestClampRuntimeUsageInterval(t *testing.T) {
	cases := []struct {
		name  string
		input time.Duration
		want  time.Duration
	}{
		{"zero uses default", 0, DefaultRuntimeUsageInterval},
		{"negative uses default", -time.Second, DefaultRuntimeUsageInterval},
		{"below floor clamps up", 10 * time.Millisecond, minRuntimeUsageInterval},
		{"above ceiling clamps down", time.Minute, maxRuntimeUsageInterval},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampRuntimeUsageInterval(tc.input); got != tc.want {
				t.Errorf("clampRuntimeUsageInterval(%v) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// memory.peak and oom_kill reset on container restart, so the warning must come
// from retained history.
func TestRetainedMemoryWarningsSurviveAContainerRestart(t *testing.T) {
	const limit = int64(6442450944) // 6144Mi, the runtimepod in the observed report

	restarted := config.RuntimeMemoryUsage{
		CurrentBytes: 1503238554, PeakBytes: 2254857830, PeakObserved: true,
		LimitBytes: limit, PercentOfLimit: 100 * float64(1503238554) / float64(limit),
		OOMKills: 0, OOMKillsObserved: true,
	}
	unrestarted := config.RuntimeMemoryUsage{
		CurrentBytes: 6442450943, PeakBytes: 6443237376, PeakObserved: true,
		LimitBytes: limit, PercentOfLimit: 100 * float64(6442450943) / float64(limit),
		OOMKills: 1, OOMKillsObserved: true,
	}

	t.Run("a retained kill and near-limit peak keep warning after a restart", func(t *testing.T) {
		history := deploy.RuntimeUsageHistory{Restarts: 25, ObservedPeakMemoryBytes: 6443237376, ObservedOOMKills: 1}

		warnings := retainedMemoryUsageWarnings(restarted, history)
		if !hasWarningContaining(warnings, "retained history recorded 1 OOM kill") {
			t.Errorf("a kill the retained history records must survive the restart, got %v", warnings)
		}
		if !hasWarningContaining(warnings, "retained memory peak reached") {
			t.Errorf("a retained near-limit peak must survive the restart, got %v", warnings)
		}
	})

	t.Run("a genuinely idle environment is still healthy", func(t *testing.T) {
		idle := config.RuntimeMemoryUsage{
			CurrentBytes: 52428800, PeakBytes: 104857600, PeakObserved: true,
			LimitBytes: limit, PercentOfLimit: 100 * float64(52428800) / float64(limit),
			OOMKillsObserved: true,
		}
		for _, history := range []deploy.RuntimeUsageHistory{
			{},
			{Restarts: 3, ObservedPeakMemoryBytes: 104857600},
		} {
			if warnings := retainedMemoryUsageWarnings(idle, history); len(warnings) != 0 {
				t.Errorf("an environment that never approached its limit must not warn, got %v", warnings)
			}
		}
	})

	t.Run("raising the limit clears the retained peak warning", func(t *testing.T) {
		// The signal is re-derived every read, so acting on it clears it.
		raised := restarted
		raised.LimitBytes = 9663676416 // 9216Mi
		history := deploy.RuntimeUsageHistory{Restarts: 25, ObservedPeakMemoryBytes: 6443237376, ObservedOOMKills: 1}

		warnings := retainedMemoryUsageWarnings(raised, history)
		if hasWarningContaining(warnings, "retained memory peak reached") {
			t.Errorf("a raised limit must clear the peak warning, got %v", warnings)
		}
		if !hasWarningContaining(warnings, "retained history recorded 1 OOM kill") {
			t.Errorf("a kill that already happened stays reported, got %v", warnings)
		}
	})

	t.Run("an uninterrupted container is not warned twice", func(t *testing.T) {
		history := deploy.RuntimeUsageHistory{Restarts: 0, ObservedPeakMemoryBytes: 6443237376, ObservedOOMKills: 1}

		if warnings := retainedMemoryUsageWarnings(unrestarted, history); len(warnings) != 0 {
			t.Errorf("live counters already cover this container, got %v", warnings)
		}
	})
}

func TestApplyRetainedUsageWarningsReadsTheEnvironmentHistory(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)

	reading := parseRuntimeUsage(config.ShellLaunchParams{Tenant: "erun", Environment: "code3"}, strings.Join([]string{
		"cgroup_type=cgroup2fs",
		"memory_current=1503238554",
		"memory_max=6442450944",
		"memory_peak=2254857830",
		"memory_oom_kill=0",
	}, "\n"), time.Second)
	if len(reading.Warnings) != 0 {
		t.Fatalf("expected the live reading alone to be silent, got %v", reading.Warnings)
	}
	if warnings := applyRetainedUsageWarnings(reading).Warnings; len(warnings) != 0 {
		t.Fatalf("no retained history must not manufacture a warning, got %v", warnings)
	}

	history := AppendRuntimeUsageSample(deploy.RuntimeUsageHistory{}, config.RuntimeUsage{
		Memory: config.RuntimeMemoryUsage{LimitBytes: 6442450944, PeakBytes: 6443237376, PeakObserved: true, OOMKills: 1, OOMKillsObserved: true},
	}, time.Now())
	if err := SaveRuntimeUsageHistory("erun", "code3", history); err != nil {
		t.Fatalf("save history: %v", err)
	}

	warnings := applyRetainedUsageWarnings(reading).Warnings
	if !hasWarningContaining(warnings, "retained memory peak reached") {
		t.Errorf("the pre-restart peak must be picked up, got %v", warnings)
	}
	if !hasWarningContaining(warnings, "retained history recorded 1 OOM kill") {
		t.Errorf("the pre-restart kill must be picked up, got %v", warnings)
	}
	if len(warnings) != 2 {
		t.Errorf("expected exactly the two retained warnings, got %v", warnings)
	}
}
