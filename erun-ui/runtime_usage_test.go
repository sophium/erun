package main

import (
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// TestRuntimeUsageCPUUnavailableRendersAsStatedUnavailable covers the cgroup
// v1 case: the reader reports Unavailable rather than a quota, and the UI
// must not fold that into a confident "0.0%" utilisation figure.
func TestRuntimeUsageCPUUnavailableRendersAsStatedUnavailable(t *testing.T) {
	cpu := uiRuntimeCPUUsageFromReading(eruncommon.RuntimeCPUUsage{
		Unavailable: "cgroup v2 not detected under /sys/fs/cgroup; CPU usage needs cpu.max/cpu.stat",
	})
	if cpu.Available {
		t.Fatalf("cgroup v1 must render as unavailable, got Available=true: %+v", cpu)
	}
	if cpu.Unavailable == "" {
		t.Fatalf("the reader's unavailability reason must be carried through, got empty")
	}
	if cpu.Utilization != "" || cpu.UtilizationPercent != 0 || cpu.Quota != "" {
		t.Fatalf("an unavailable CPU reading must not carry a formatted utilisation/quota, got %+v", cpu)
	}
}

// TestRuntimeUsageMemoryUnlimitedRendersAsUnlimitedNotZeroPercent covers a
// container whose memory.max is "max": the reading is available, but there is
// no limit to divide by, so PercentOfLimit/Limit must stay empty rather than
// reading as "using 0% of a 0-byte limit".
func TestRuntimeUsageMemoryUnlimitedRendersAsUnlimitedNotZeroPercent(t *testing.T) {
	memory := uiRuntimeMemoryUsageFromReading(eruncommon.RuntimeMemoryUsage{
		CurrentBytes: 512 * 1024 * 1024,
		PeakBytes:    600 * 1024 * 1024,
		Unlimited:    true,
	})
	if !memory.Available {
		t.Fatalf("an unlimited container is a real, available reading, got Available=false: %+v", memory)
	}
	if !memory.Unlimited {
		t.Fatalf("Unlimited must be carried through, got %+v", memory)
	}
	if memory.Limit != "" || memory.LimitBytes != 0 || memory.PercentOfLimit != 0 {
		t.Fatalf("an unlimited reading must not synthesize a limit or percentage, got %+v", memory)
	}
	if memory.Current == "" || memory.Peak == "" {
		t.Fatalf("current and peak are real measurements and must still render, got %+v", memory)
	}
}

// TestRuntimeUsageMemoryUnavailableRendersAsStatedUnavailable covers the
// unreadable-file case (memory.current itself could not be read): distinct
// from Unlimited, this is an error state and every other memory field must
// stay empty rather than defaulting to zero.
func TestRuntimeUsageMemoryUnavailableRendersAsStatedUnavailable(t *testing.T) {
	memory := uiRuntimeMemoryUsageFromReading(eruncommon.RuntimeMemoryUsage{
		Unavailable: "memory.current was not readable",
	})
	if memory.Available {
		t.Fatalf("an unreadable memory.current must render as unavailable, got Available=true: %+v", memory)
	}
	if memory.Unavailable == "" {
		t.Fatalf("the reader's unavailability reason must be carried through, got empty")
	}
	if memory.Current != "" || memory.Peak != "" || memory.Limit != "" || memory.CurrentBytes != 0 {
		t.Fatalf("an unavailable reading must not carry any formatted or raw figures, got %+v", memory)
	}
}

// TestRuntimeUsageDiskUnavailableRendersAsStatedUnavailable covers df failing
// to report the watched mount: the panel must say so, not show "0%" used.
func TestRuntimeUsageDiskUnavailableRendersAsStatedUnavailable(t *testing.T) {
	disk := uiRuntimeDiskUsageFromReading(eruncommon.RuntimeDiskUsage{
		Mount:       "/home/erun",
		Unavailable: "df did not report usage for /home/erun",
	})
	if disk.Available {
		t.Fatalf("an unreadable disk mount must render as unavailable, got Available=true: %+v", disk)
	}
	if disk.Mount != "/home/erun" {
		t.Fatalf("the mount must still be named even when unavailable, got %q", disk.Mount)
	}
	if disk.Unavailable == "" {
		t.Fatalf("the reader's unavailability reason must be carried through, got empty")
	}
	if disk.Used != "" || disk.Total != "" || disk.Percent != "" || disk.PercentUsed != 0 {
		t.Fatalf("an unavailable disk reading must not carry any formatted or raw figures, got %+v", disk)
	}
}

// TestRuntimeUsageFromReadingMixedAvailability covers a realistic reading
// where CPU is unavailable but memory and disk are not, pinning that the
// top-level mapping keeps each field's own unavailability independent rather
// than collapsing the whole reading to one status.
func TestRuntimeUsageFromReadingMixedAvailability(t *testing.T) {
	usage := uiRuntimeUsageFromReading(eruncommon.RuntimeUsage{
		Tenant:      "petios",
		Environment: "local",
		CPU:         eruncommon.RuntimeCPUUsage{Unavailable: "cpu.max reports no quota (unlimited or not readable); utilisation needs a quota to measure against"},
		Memory: eruncommon.RuntimeMemoryUsage{
			CurrentBytes:   1024 * 1024 * 1024,
			LimitBytes:     2048 * 1024 * 1024,
			PercentOfLimit: 50,
			OOMKills:       2,
		},
		Disk: []eruncommon.RuntimeDiskUsage{{
			Mount:       "/home/erun",
			TotalBytes:  100 * 1024 * 1024 * 1024,
			UsedBytes:   90 * 1024 * 1024 * 1024,
			PercentUsed: 90,
		}},
		Warnings: []string{"the cgroup recorded 2 OOM kill(s)"},
	})
	if !usage.Available {
		t.Fatalf("a reachable probe must report Available=true even with a partially unavailable reading")
	}
	if usage.CPU.Available {
		t.Fatalf("CPU must stay unavailable independent of memory/disk, got %+v", usage.CPU)
	}
	if !usage.Memory.Available || usage.Memory.PercentOfLimit != 50 {
		t.Fatalf("memory must render its real reading, got %+v", usage.Memory)
	}
	if len(usage.Disk) != 1 || !usage.Disk[0].Available || usage.Disk[0].PercentUsed != 90 {
		t.Fatalf("disk must render its real reading, got %+v", usage.Disk)
	}
	if len(usage.Warnings) != 1 {
		t.Fatalf("warnings must be carried through verbatim, got %+v", usage.Warnings)
	}
}
