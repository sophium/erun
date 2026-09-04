package eruncommon

import (
	"encoding/json"
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
	cases = append(cases, runtimeUsageMemoryObservationReadingCases()...)
	return append(cases, runtimeUsageDiskOwnUsageReadingCases()...)
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
				"disk_own_used_kb=54000000",
			}, "\n"),
			// 100000 usec of CPU burned over 1 elapsed second, against a 1-core quota.
			wantCPU: RuntimeCPUUsage{QuotaCores: 1, UtilizationPercent: 10, IntervalSeconds: 1},
			wantMemory: RuntimeMemoryUsage{
				CurrentBytes: 413589504, PeakBytes: 1027301376, PeakObserved: true,
				LimitBytes: 2147483648, PercentOfLimit: 100 * float64(413589504) / float64(2147483648),
				OOMKillsObserved: true,
			},
			wantDisk: RuntimeDiskUsage{
				Mount: runtimeUsageWatchedMount, NodeShared: true,
				TotalBytes: 198234112 * 1024, UsedBytes: 99117056 * 1024, PercentUsed: 100 * float64(99117056) / float64(198234112),
				OwnUsedBytes: 54000000 * 1024, OwnUsageObserved: true,
			},
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
			wantDisk: RuntimeDiskUsage{Mount: runtimeUsageWatchedMount, NodeShared: true, Unavailable: "an empty df line should report unavailable"},
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
				"disk_own_used_kb=50",
			}, "\n"),
			wantCPU:    RuntimeCPUUsage{IntervalSeconds: 1, Unavailable: "cgroup v1 should report CPU unavailable"},
			wantMemory: RuntimeMemoryUsage{Unavailable: "cgroup v1 should report memory unavailable"},
			// Disk uses statfs via df, not cgroup, so it stays readable regardless.
			wantDisk: RuntimeDiskUsage{
				Mount: runtimeUsageWatchedMount, NodeShared: true,
				TotalBytes: 100 * 1024, UsedBytes: 50 * 1024, PercentUsed: 50,
				OwnUsedBytes: 50 * 1024, OwnUsageObserved: true,
			},
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
			wantDisk:   RuntimeDiskUsage{Mount: runtimeUsageWatchedMount, NodeShared: true, Unavailable: "missing df line should report unavailable"},
		},
		{
			// A long filesystem identifier pushes df's data row onto its own
			// line with the Filesystem column dropped, leaving only the 5
			// remaining columns -- the shape parseRuntimeDFUsage's
			// neighbor-based lookup exists to survive; the script's
			// `tail -n1` already selects this line.
			name:       "wrapped filesystem name still parses by column, not fixed index",
			output:     "disk_workspace=1000 500 500 50% /home/erun\ndisk_own_used_kb=300",
			wantCPU:    RuntimeCPUUsage{IntervalSeconds: 1, Unavailable: "no cgroup_type key should report unavailable"},
			wantMemory: RuntimeMemoryUsage{Unavailable: "no cgroup_type key should report unavailable"},
			wantDisk: RuntimeDiskUsage{
				Mount: runtimeUsageWatchedMount, NodeShared: true,
				TotalBytes: 1000 * 1024, UsedBytes: 500 * 1024, PercentUsed: 50,
				OwnUsedBytes: 300 * 1024, OwnUsageObserved: true,
			},
		},
	}
}

// runtimeUsageDiskOwnUsageReadingCases is split out from
// runtimeUsageBaseReadingCases the same way runtimeUsageMemoryObservationReadingCases
// is: a dedicated table for a reading facet that must stay independent of the
// rest of the disk reading, kept in its own function purely to bound the
// parent table's length.
func runtimeUsageDiskOwnUsageReadingCases() []runtimeUsageReadingCase {
	return []runtimeUsageReadingCase{
		{
			// du and df read independent sources (a filesystem walk vs a
			// statfs), so one being unreadable must not suppress the other --
			// an operator can still learn their own footprint even when the
			// node-wide figure is unavailable, and vice versa.
			name: "own usage is observed independently of df availability",
			output: strings.Join([]string{
				"cgroup_type=cgroup2fs",
				"memory_current=1048576",
				"memory_max=2097152",
				"memory_peak=1048576",
				"memory_oom_kill=0",
				"cpu_max=100000 100000",
				"cpu_usage_before=0",
				"cpu_usage_after=0",
				"cpu_time_before_ns=1000000000",
				"cpu_time_after_ns=2000000000",
				"disk_workspace=",
				"disk_own_used_kb=12345",
			}, "\n"),
			wantCPU: RuntimeCPUUsage{QuotaCores: 1, UtilizationPercent: 0, IntervalSeconds: 1},
			wantMemory: RuntimeMemoryUsage{
				CurrentBytes: 1048576, PeakBytes: 1048576, PeakObserved: true,
				LimitBytes: 2097152, PercentOfLimit: 50, OOMKillsObserved: true,
			},
			wantDisk: RuntimeDiskUsage{
				Mount: runtimeUsageWatchedMount, NodeShared: true,
				Unavailable:      "an empty df line should report unavailable even though own usage was read",
				OwnUsedBytes:     12345 * 1024,
				OwnUsageObserved: true,
			},
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
			wantDisk: RuntimeDiskUsage{Mount: runtimeUsageWatchedMount, NodeShared: true, Unavailable: "missing df line should report unavailable"},
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
			wantDisk: RuntimeDiskUsage{Mount: runtimeUsageWatchedMount, NodeShared: true, Unavailable: "missing df line should report unavailable"},
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
	if got.NodeShared != want.NodeShared {
		t.Errorf("Disk.NodeShared = %t, want %t", got.NodeShared, want.NodeShared)
	}
	// Own usage comes from an independent read (du, not df/statfs), so it is
	// checked before the Unavailable early-return below -- one being
	// unreadable must not hide the other.
	assertRuntimeDiskOwnUsage(t, got, want)
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

func assertRuntimeDiskOwnUsage(t *testing.T, got, want RuntimeDiskUsage) {
	t.Helper()
	if got.OwnUsageObserved != want.OwnUsageObserved {
		t.Errorf("Disk.OwnUsageObserved = %t, want %t", got.OwnUsageObserved, want.OwnUsageObserved)
	}
	if want.OwnUsageObserved && got.OwnUsedBytes != want.OwnUsedBytes {
		t.Errorf("Disk.OwnUsedBytes = %d, want %d", got.OwnUsedBytes, want.OwnUsedBytes)
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

	t.Run("disk warning fires at the threshold and names the node-shared scope", func(t *testing.T) {
		output := "disk_workspace=/dev/sda1 1000 900 100 90% /home/erun"
		usage := parseRuntimeUsage(req, output, interval)
		assertDiskWarningNamesNodeSharedScope(t, usage.Warnings)
	})
}

// assertDiskWarningNamesNodeSharedScope checks both halves of the disk
// warning's wording, factored out of TestParseRuntimeUsageWarnings so the
// two conditions don't count against that function's own complexity budget.
func assertDiskWarningNamesNodeSharedScope(t *testing.T, warnings []string) {
	t.Helper()
	if !hasWarningContaining(warnings, "node disk is at 90%") {
		t.Errorf("90%% disk usage should warn, got %v", warnings)
	}
	if !hasWarningContaining(warnings, "shared with every environment on this node") {
		t.Errorf("the disk warning must name the node-shared scope so an operator does not clean up the wrong environment, got %v", warnings)
	}
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
