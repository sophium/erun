package eruncommon

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestParseRuntimeUsageReadings covers the fixture shapes #1233 measured
// live: cgroup v2 present, an unlimited memory.max, cgroup v1 (or no cgroup
// fs at all), and a script that could not read a file. None of these are
// reachable from the dry-run integration subprocess (dry-run never runs the
// script), and the real-run path needs a live cgroup v2 container the
// harness does not have, so the fixture matrix belongs here rather than in
// erun-integration. Table-driven with the comparisons factored into
// top-level assert helpers, so the number of fixtures does not grow this
// function's own cyclomatic complexity.
func TestParseRuntimeUsageReadings(t *testing.T) {
	req := ShellLaunchParams{Tenant: "frs", Environment: "local"}
	interval := time.Second

	for _, tc := range runtimeUsageReadingCases() {
		t.Run(tc.name, func(t *testing.T) {
			usage := parseRuntimeUsage(req, tc.output, interval)
			if usage.Tenant != req.Tenant || usage.Environment != req.Environment {
				t.Errorf("tenant/environment not carried through: %+v", usage)
			}
			assertRuntimeCPU(t, usage.CPU, tc.wantCPU)
			assertRuntimeMemory(t, usage.Memory, tc.wantMemory)
			if len(usage.Disk) != 1 {
				t.Fatalf("expected exactly one disk reading, got %d", len(usage.Disk))
			}
			assertRuntimeDisk(t, usage.Disk[0], tc.wantDisk)
			if len(usage.Warnings) != 0 {
				t.Errorf("expected no warnings for a reading well under every threshold, got %v", usage.Warnings)
			}
		})
	}
}

type runtimeUsageReadingCase struct {
	name       string
	output     string
	wantCPU    RuntimeCPUUsage
	wantMemory RuntimeMemoryUsage
	wantDisk   RuntimeDiskUsage
}

func runtimeUsageReadingCases() []runtimeUsageReadingCase {
	cases := runtimeUsageBaseReadingCases()
	return append(cases, runtimeUsageMemoryObservationReadingCases()...)
}

func runtimeUsageBaseReadingCases() []runtimeUsageReadingCase {
	return []runtimeUsageReadingCase{
		{
			name: "cgroup v2 present reports quota, usage, and disk",
			output: strings.Join([]string{
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
				"disk_workspace=/dev/sda1        198234112  99117056   89006592  53% /home/erun",
			}, "\n"),
			// 100000 usec of CPU burned over 1 elapsed second, against a 1-core quota.
			wantCPU: RuntimeCPUUsage{QuotaCores: 1, UtilizationPercent: 10, IntervalSeconds: 1},
			wantMemory: RuntimeMemoryUsage{
				CurrentBytes: 413589504, PeakBytes: 1027301376, PeakObserved: true,
				LimitBytes: 2147483648, PercentOfLimit: 100 * float64(413589504) / float64(2147483648),
				OOMKillsObserved: true,
			},
			wantDisk: RuntimeDiskUsage{Mount: runtimeUsageWatchedMount, TotalBytes: 198234112 * 1024, UsedBytes: 99117056 * 1024, PercentUsed: 100 * float64(99117056) / float64(198234112)},
		},
		{
			name: "unlimited memory.max reports Unlimited, not a fabricated percentage",
			output: strings.Join([]string{
				"cgroup_type=cgroup2fs",
				"memory_current=52428800",
				"memory_max=max",
				"memory_peak=104857600",
				"memory_oom_kill=0",
				"cpu_max=max 100000",
				"cpu_usage_before=100",
				"cpu_usage_after=200",
				"cpu_time_before_ns=1000000000",
				"cpu_time_after_ns=2000000000",
				"disk_workspace=",
			}, "\n"),
			wantCPU: RuntimeCPUUsage{IntervalSeconds: 1, Unavailable: "an unlimited cpu.max quota should report unavailable"},
			wantMemory: RuntimeMemoryUsage{
				CurrentBytes: 52428800, PeakBytes: 104857600, PeakObserved: true, Unlimited: true, OOMKillsObserved: true,
			},
			wantDisk: RuntimeDiskUsage{Mount: runtimeUsageWatchedMount, Unavailable: "an empty df line should report unavailable"},
		},
		{
			name: "cgroup v1 (or no cgroup fs) reports memory and CPU unavailable, not an error",
			output: strings.Join([]string{
				"cgroup_type=tmpfs",
				"memory_current=",
				"memory_max=",
				"memory_peak=",
				"memory_oom_kill=",
				"cpu_max=",
				"cpu_usage_before=",
				"cpu_usage_after=",
				"cpu_time_before_ns=",
				"cpu_time_after_ns=",
				"disk_workspace=/dev/sda1 100 50 40 55% /home/erun",
			}, "\n"),
			wantCPU:    RuntimeCPUUsage{IntervalSeconds: 1, Unavailable: "cgroup v1 should report CPU unavailable"},
			wantMemory: RuntimeMemoryUsage{Unavailable: "cgroup v1 should report memory unavailable"},
			// Disk uses statfs via df, not cgroup, so it stays readable regardless.
			wantDisk: RuntimeDiskUsage{Mount: runtimeUsageWatchedMount, TotalBytes: 100 * 1024, UsedBytes: 50 * 1024, PercentUsed: 50},
		},
		{
			name: "memory.current missing reports unavailable without a fabricated zero",
			output: strings.Join([]string{
				"cgroup_type=cgroup2fs",
				"memory_current=",
				"memory_max=2147483648",
			}, "\n"),
			wantCPU:    RuntimeCPUUsage{IntervalSeconds: 1, Unavailable: "cpu.max missing should report unavailable"},
			wantMemory: RuntimeMemoryUsage{Unavailable: "memory.current missing should report unavailable"},
			wantDisk:   RuntimeDiskUsage{Mount: runtimeUsageWatchedMount, Unavailable: "missing df line should report unavailable"},
		},
		{
			// A long filesystem identifier pushes df's data row onto its own
			// line with the Filesystem column dropped, leaving only the 5
			// remaining columns -- the shape parseRuntimeDFUsage's
			// neighbor-based lookup exists to survive; the script's
			// `tail -n1` already selects this line.
			name:       "wrapped filesystem name still parses by column, not fixed index",
			output:     "disk_workspace=1000 500 500 50% /home/erun",
			wantCPU:    RuntimeCPUUsage{IntervalSeconds: 1, Unavailable: "no cgroup_type key should report unavailable"},
			wantMemory: RuntimeMemoryUsage{Unavailable: "no cgroup_type key should report unavailable"},
			wantDisk:   RuntimeDiskUsage{Mount: runtimeUsageWatchedMount, TotalBytes: 1000 * 1024, UsedBytes: 500 * 1024, PercentUsed: 50},
		},
	}
}

// runtimeUsageMemoryObservationReadingCases covers memory.peak and
// memory.events' oom_kill going missing independently of the rest of the
// memory reading -- each must stay unobserved rather than reporting a
// fabricated zero, without the struct-level Unavailable firing, since the
// rest of the reading is genuinely available.
func runtimeUsageMemoryObservationReadingCases() []runtimeUsageReadingCase {
	return []runtimeUsageReadingCase{
		{
			name: "memory.peak missing reports unobserved, not a fabricated zero",
			output: strings.Join([]string{
				"cgroup_type=cgroup2fs",
				"memory_current=413589504",
				"memory_max=2147483648",
				"memory_peak=",
				"memory_oom_kill=0",
			}, "\n"),
			wantCPU: RuntimeCPUUsage{IntervalSeconds: 1, Unavailable: "cpu.max missing should report unavailable"},
			wantMemory: RuntimeMemoryUsage{
				CurrentBytes: 413589504, LimitBytes: 2147483648,
				PercentOfLimit: 100 * float64(413589504) / float64(2147483648), OOMKillsObserved: true,
			},
			wantDisk: RuntimeDiskUsage{Mount: runtimeUsageWatchedMount, Unavailable: "missing df line should report unavailable"},
		},
		{
			// memory.events' oom_kill counter can be as unreadable as
			// memory.peak; OOMKillsObserved must stay false rather than
			// reporting a confident "no kills".
			name: "memory.events oom_kill missing reports unobserved, not a fabricated zero",
			output: strings.Join([]string{
				"cgroup_type=cgroup2fs",
				"memory_current=413589504",
				"memory_max=2147483648",
				"memory_peak=1027301376",
				"memory_oom_kill=",
			}, "\n"),
			wantCPU: RuntimeCPUUsage{IntervalSeconds: 1, Unavailable: "cpu.max missing should report unavailable"},
			wantMemory: RuntimeMemoryUsage{
				CurrentBytes: 413589504, PeakBytes: 1027301376, PeakObserved: true,
				LimitBytes: 2147483648, PercentOfLimit: 100 * float64(413589504) / float64(2147483648),
			},
			wantDisk: RuntimeDiskUsage{Mount: runtimeUsageWatchedMount, Unavailable: "missing df line should report unavailable"},
		},
	}
}

// assertRuntimeCPU compares an availability marker (want.Unavailable's
// presence, not its exact text -- the fixture table above uses the field to
// document *why* a case is unavailable) and, only when available, the
// numeric fields.
func assertRuntimeCPU(t *testing.T, got, want RuntimeCPUUsage) {
	t.Helper()
	if (got.Unavailable == "") != (want.Unavailable == "") {
		t.Errorf("CPU.Unavailable = %q, want unavailable=%t", got.Unavailable, want.Unavailable != "")
		return
	}
	if want.Unavailable != "" {
		return
	}
	if got.QuotaCores != want.QuotaCores {
		t.Errorf("CPU.QuotaCores = %v, want %v", got.QuotaCores, want.QuotaCores)
	}
	if got.UtilizationPercent != want.UtilizationPercent {
		t.Errorf("CPU.UtilizationPercent = %v, want %v", got.UtilizationPercent, want.UtilizationPercent)
	}
}

func assertRuntimeMemory(t *testing.T, got, want RuntimeMemoryUsage) {
	t.Helper()
	if (got.Unavailable == "") != (want.Unavailable == "") {
		t.Errorf("Memory.Unavailable = %q, want unavailable=%t", got.Unavailable, want.Unavailable != "")
		return
	}
	if want.Unavailable != "" {
		return
	}
	assertRuntimeMemoryPeakAndOOM(t, got, want)
	if got.Unlimited != want.Unlimited {
		t.Errorf("Memory.Unlimited = %t, want %t", got.Unlimited, want.Unlimited)
	}
	if !want.Unlimited && got.LimitBytes != want.LimitBytes {
		t.Errorf("Memory.LimitBytes = %d, want %d", got.LimitBytes, want.LimitBytes)
	}
	if !want.Unlimited && got.PercentOfLimit != want.PercentOfLimit {
		t.Errorf("Memory.PercentOfLimit = %v, want %v", got.PercentOfLimit, want.PercentOfLimit)
	}
}

// assertRuntimeMemoryPeakAndOOM checks CurrentBytes/PeakBytes/OOMKills
// alongside their own Observed bit: a value alone is never enough to say
// whether it means anything.
func assertRuntimeMemoryPeakAndOOM(t *testing.T, got, want RuntimeMemoryUsage) {
	t.Helper()
	if got.CurrentBytes != want.CurrentBytes {
		t.Errorf("Memory.CurrentBytes = %d, want %d", got.CurrentBytes, want.CurrentBytes)
	}
	if got.PeakBytes != want.PeakBytes {
		t.Errorf("Memory.PeakBytes = %d, want %d", got.PeakBytes, want.PeakBytes)
	}
	if got.PeakObserved != want.PeakObserved {
		t.Errorf("Memory.PeakObserved = %t, want %t", got.PeakObserved, want.PeakObserved)
	}
	if got.OOMKillsObserved != want.OOMKillsObserved {
		t.Errorf("Memory.OOMKillsObserved = %t, want %t", got.OOMKillsObserved, want.OOMKillsObserved)
	}
}

func assertRuntimeDisk(t *testing.T, got, want RuntimeDiskUsage) {
	t.Helper()
	if got.Mount != want.Mount {
		t.Errorf("Disk.Mount = %q, want %q", got.Mount, want.Mount)
	}
	if (got.Unavailable == "") != (want.Unavailable == "") {
		t.Errorf("Disk.Unavailable = %q, want unavailable=%t", got.Unavailable, want.Unavailable != "")
		return
	}
	if want.Unavailable != "" {
		return
	}
	if got.TotalBytes != want.TotalBytes {
		t.Errorf("Disk.TotalBytes = %d, want %d", got.TotalBytes, want.TotalBytes)
	}
	if got.UsedBytes != want.UsedBytes {
		t.Errorf("Disk.UsedBytes = %d, want %d", got.UsedBytes, want.UsedBytes)
	}
	if got.PercentUsed != want.PercentUsed {
		t.Errorf("Disk.PercentUsed = %v, want %v", got.PercentUsed, want.PercentUsed)
	}
}

// TestParseRuntimeUsageWarnings covers the named threshold constants: a
// reading nobody acts on is decoration, so each threshold must fire exactly
// at its boundary and stay silent below it.
func TestParseRuntimeUsageWarnings(t *testing.T) {
	req := ShellLaunchParams{Tenant: "frs", Environment: "local"}
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

	t.Run("memory.peak warning does not fire when the peak could not be read", func(t *testing.T) {
		output := strings.Join([]string{
			"cgroup_type=cgroup2fs",
			"memory_current=52428800",
			"memory_max=2147483648",
			"memory_peak=",
			"memory_oom_kill=0",
		}, "\n")

		usage := parseRuntimeUsage(req, output, interval)
		if usage.Memory.PeakObserved {
			t.Fatalf("expected PeakObserved=false when memory.peak is unreadable, got %+v", usage.Memory)
		}
		if hasWarningContaining(usage.Warnings, "memory.peak reached") {
			t.Errorf("an unread peak must not compute a percentage to warn on, got %v", usage.Warnings)
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

	t.Run("OOM kills always warn", func(t *testing.T) {
		output := strings.Join([]string{
			"cgroup_type=cgroup2fs",
			"memory_current=52428800",
			"memory_max=2147483648",
			"memory_peak=52428800",
			"memory_oom_kill=2",
		}, "\n")

		usage := parseRuntimeUsage(req, output, interval)
		if !hasWarningContaining(usage.Warnings, "recorded 2 OOM kill") {
			t.Errorf("expected an OOM kill warning, got %v", usage.Warnings)
		}
	})

	t.Run("disk warning fires at the threshold", func(t *testing.T) {
		output := "disk_workspace=/dev/sda1 1000 900 100 90% /home/erun"
		usage := parseRuntimeUsage(req, output, interval)
		if !hasWarningContaining(usage.Warnings, "disk usage") {
			t.Errorf("90%% disk usage should warn, got %v", usage.Warnings)
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

// TestRuntimeMemoryUsageJSONDistinguishesUnreadableFromZero pins the wire
// contract: PeakBytes/OOMKills are already omitempty, so a genuine zero and
// an unread value marshal identically unless their own Observed bit is
// carried alongside them.
func TestRuntimeMemoryUsageJSONDistinguishesUnreadableFromZero(t *testing.T) {
	unread, err := json.Marshal(RuntimeMemoryUsage{CurrentBytes: 100})
	if err != nil {
		t.Fatalf("marshal unread: %v", err)
	}
	if strings.Contains(string(unread), "peakObserved") || strings.Contains(string(unread), "peakBytes") {
		t.Errorf("an unread peak must omit both peakBytes and peakObserved, got %s", unread)
	}

	genuineZero, err := json.Marshal(RuntimeMemoryUsage{CurrentBytes: 100, PeakObserved: true})
	if err != nil {
		t.Fatalf("marshal genuine zero: %v", err)
	}
	if !strings.Contains(string(genuineZero), `"peakObserved":true`) {
		t.Errorf("a genuinely-zero, observed peak must carry peakObserved:true, got %s", genuineZero)
	}
	if strings.Contains(string(genuineZero), "peakBytes") {
		t.Errorf("a genuine zero peak still omits the zero-valued peakBytes key, got %s", genuineZero)
	}

	var roundTripped RuntimeMemoryUsage
	if err := json.Unmarshal(genuineZero, &roundTripped); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !roundTripped.PeakObserved || roundTripped.PeakBytes != 0 {
		t.Errorf("round trip = %+v, want PeakObserved=true, PeakBytes=0", roundTripped)
	}
}

// TestRunRuntimeUsageReportsTheDindSidecarSeparately is the regression guard
// for erun#2120: an environment mid-release can read 0.3% CPU / idle memory
// from the runtime container while the erun-dind sidecar -- where the actual
// build runs -- saturates its own cores, and before this fix nothing in the
// reading let an operator tell a genuinely idle environment apart from one
// whose build is grinding away in a container this reading could not see at
// all. The fake runner answers differently per container, exactly like a
// real busy-build/idle-runtime split, and asserts the busy sidecar reading
// surfaces on RuntimeUsage.Dind rather than being silently dropped.
func TestRunRuntimeUsageReportsTheDindSidecarSeparately(t *testing.T) {
	idleRuntimeReading := strings.Join([]string{
		"cgroup_type=cgroup2fs",
		"memory_current=104857600", // ~100Mi
		"memory_max=24696061952",   // ~23Gi
		"memory_peak=104857600",
		"memory_oom_kill=0",
		"cpu_max=1200000 100000", // 12-core quota
		"cpu_usage_before=1000000",
		"cpu_usage_after=1003000", // 3ms burned over 1s: reads as idle
		"cpu_time_before_ns=1000000000",
		"cpu_time_after_ns=2000000000",
		"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
	}, "\n")
	busyDindReading := strings.Join([]string{
		"cgroup_type=cgroup2fs",
		"memory_current=2040109465", // ~1.9Gi
		"memory_max=15032385536",    // 14Gi
		"memory_peak=3435973836",    // ~3.2Gi
		"memory_oom_kill=0",
		"cpu_max=400000 100000", // 4-core quota
		"cpu_usage_before=1000000",
		"cpu_usage_after=2900000", // 1.9 cores burned over 1s: a real build grinding
		"cpu_time_before_ns=1000000000",
		"cpu_time_after_ns=2000000000",
		"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
	}, "\n")

	req := ShellLaunchParams{Tenant: "erun", Environment: "build", Type: EnvironmentTypeLocalAgent}
	runner := func(_ ShellLaunchParams, container, _ string) (RemoteCommandResult, error) {
		if container == runtimeDindContainerName {
			return RemoteCommandResult{Stdout: busyDindReading}, nil
		}
		return RemoteCommandResult{Stdout: idleRuntimeReading}, nil
	}

	usage, err := RunRuntimeUsage(Context{}, runner, req, RuntimeUsageParams{Interval: time.Second})
	if err != nil {
		t.Fatalf("RunRuntimeUsage: %v", err)
	}
	if !usage.ExcludesBuilds {
		t.Fatalf("expected ExcludesBuilds=true for a local-agent env, got %+v", usage)
	}
	if usage.CPU.UtilizationPercent >= 5 {
		t.Fatalf("expected the runtime container's own reading to look idle, got %+v", usage.CPU)
	}
	if usage.Dind == nil {
		t.Fatalf("expected a Dind reading for a build-capable environment, got nil (the exact regression: the busy sidecar is invisible)")
	}
	if usage.Dind.CPU.UtilizationPercent < 40 {
		t.Errorf("expected the sidecar's own reading to show the real build load (~47%% of its 4-core quota), got %+v", usage.Dind.CPU)
	}
	if usage.Dind.Memory.CurrentBytes != 2040109465 {
		t.Errorf("expected the sidecar's own memory reading to carry through, got %+v", usage.Dind.Memory)
	}
}

// TestRunRuntimeUsageDindExecFailureFailsSoft covers the fail-soft contract:
// an environment whose sidecar cannot be reached (an older runtime image, a
// sidecar mid-restart) must still get a usable runtime-container reading
// instead of losing the whole call over a container this reading has always
// been unable to see anyway.
func TestRunRuntimeUsageDindExecFailureFailsSoft(t *testing.T) {
	idleRuntimeReading := "cgroup_type=cgroup2fs\nmemory_current=100\nmemory_max=200\nmemory_peak=100\nmemory_oom_kill=0\ncpu_max=100000 100000\ncpu_usage_before=0\ncpu_usage_after=0\ncpu_time_before_ns=1000000000\ncpu_time_after_ns=2000000000\ndisk_workspace=overlay 100 50 50 50% /home/erun"
	req := ShellLaunchParams{Tenant: "erun", Environment: "build", Type: EnvironmentTypeLocalAgent}
	runner := func(_ ShellLaunchParams, container, _ string) (RemoteCommandResult, error) {
		if container == runtimeDindContainerName {
			return RemoteCommandResult{}, errors.New("container not found")
		}
		return RemoteCommandResult{Stdout: idleRuntimeReading}, nil
	}

	usage, err := RunRuntimeUsage(Context{}, runner, req, RuntimeUsageParams{Interval: time.Second})
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
		{"within range passes through", 3 * time.Second, 3 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampRuntimeUsageInterval(tc.input); got != tc.want {
				t.Errorf("clampRuntimeUsageInterval(%v) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}
