package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

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

// TestLoadRuntimeUsageReportsOwnTimeoutNotSignalKilled is the reported defect,
// reproduced through the real production wiring (loadRuntimeUsageViaKubectl is
// not mocked): with the app's own context already past its deadline, the
// probe's internal runtimeUsageTimeout bound reads as already exceeded before
// the kubectl exec even runs, so no kubectl binary is required for this test
// to reach the same classification a real timeout would. The memory panel's
// message must say so instead of "signal: killed" -- the exact phrase this
// panel would otherwise misread as an OOM kill.
func TestLoadRuntimeUsageReportsOwnTimeoutNotSignalKilled(t *testing.T) {
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"petios": {Name: "petios", DefaultEnvironment: "local"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"petios/local": {Name: "local", LocalRepoPath: t.TempDir(), KubernetesContext: "test-context"},
		},
	}
	app := NewApp(erunUIDeps{store: store})
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
	defer cancel()
	app.ctx = ctx

	usage, err := app.LoadRuntimeUsage(uiSelection{Tenant: "petios", Environment: "local"})
	if err != nil {
		t.Fatalf("LoadRuntimeUsage must not surface a probe failure as an error: %v", err)
	}
	if usage.Available {
		t.Fatalf("a timed-out probe must not be reported as an available reading: %+v", usage)
	}
	if !strings.Contains(usage.Message, "timed out") {
		t.Fatalf("expected the memory panel to name its own timeout, got %q", usage.Message)
	}
	if strings.Contains(usage.Message, "signal:") {
		t.Fatalf("the memory panel must never say anything that reads as an OOM kill on a timeout, got %q", usage.Message)
	}
}

// An environment actively building is the state the report described: its
// popover read "Busy — holding: release 1.0.302" beside a CPU of 0.2%, because
// the figure was the runtime container's and every image build actually runs in
// the erun-dind sidecar's own cgroup. The desktop's reader has always acquired
// that second reading — erun-common's RunRuntimeUsage execs the same script into
// the sidecar and hangs it on RuntimeUsage.Dind — and the UI mapping dropped it
// on the floor, so the number that would have answered "is my build working" was
// read and then thrown away.
//
// Asserted over the JSON the popover is handed rather than over the Go field:
// this mapping's whole job is to produce that payload, and the field a caller
// reads is the JSON key.
func TestRuntimeUsageCarriesTheDindSidecarSoAnActiveBuildIsNotAnIdleReading(t *testing.T) {
	usage := uiRuntimeUsageFromReading(eruncommon.RuntimeUsage{
		Tenant:      "erun",
		Environment: "build",
		// The runtime container during a release: near-idle by construction,
		// because the lane spends its time waiting on bounded job awaits.
		CPU:    eruncommon.RuntimeCPUUsage{QuotaCores: 12, UtilizationPercent: 0.2},
		Memory: eruncommon.RuntimeMemoryUsage{CurrentBytes: 1 << 30, LimitBytes: 23 << 30, PercentOfLimit: 2},
		// What the same moment looks like in the container the build is in.
		ExcludesBuilds: true,
		Dind: &eruncommon.RuntimeDindUsage{
			CPU:    eruncommon.RuntimeCPUUsage{QuotaCores: 8, UtilizationPercent: 91.5},
			Memory: eruncommon.RuntimeMemoryUsage{CurrentBytes: 19 << 30, LimitBytes: 20 << 30, PercentOfLimit: 97},
		},
	})

	payload, err := json.Marshal(usage)
	if err != nil {
		t.Fatalf("marshal uiRuntimeUsage: %v", err)
	}
	var wire struct {
		ExcludesBuilds bool `json:"excludesBuilds"`
		Dind           *struct {
			CPU    uiRuntimeCPUUsage    `json:"cpu"`
			Memory uiRuntimeMemoryUsage `json:"memory"`
		} `json:"dind"`
	}
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatalf("unmarshal uiRuntimeUsage payload: %v", err)
	}
	if !wire.ExcludesBuilds {
		t.Fatalf("the payload must disclose that CPU/memory exclude builds, got %s", payload)
	}
	if wire.Dind == nil {
		t.Fatalf("the sidecar reading was dropped from the payload the popover renders: %s", payload)
	}
	if wire.Dind.CPU.UtilizationPercent != 91.5 {
		t.Fatalf("the sidecar CPU must survive the mapping, got %+v", wire.Dind.CPU)
	}
	if wire.Dind.Memory.PercentOfLimit != 97 {
		t.Fatalf("the sidecar memory must survive the mapping, got %+v", wire.Dind.Memory)
	}
}

// A sidecar with no cpu.max quota has no percentage to report, and the reader
// carries its cumulative counter on the same unavailable reading precisely so a
// caller can still state something. Losing it would leave a build environment
// whose only CPU figure is the runtime container's near-zero — the reported
// state — so the cumulative figure has to cross the mapping too.
func TestRuntimeUsageCarriesTheSidecarCumulativeCPUWithNoQuota(t *testing.T) {
	const noQuota = "cpu.max reports no quota (unlimited or not readable); utilisation needs a quota to measure against"
	usage := uiRuntimeUsageFromReading(eruncommon.RuntimeUsage{
		Tenant:         "erun",
		Environment:    "build",
		CPU:            eruncommon.RuntimeCPUUsage{QuotaCores: 12, UtilizationPercent: 0.6},
		Memory:         eruncommon.RuntimeMemoryUsage{CurrentBytes: 1 << 30, LimitBytes: 23 << 30, PercentOfLimit: 2},
		ExcludesBuilds: true,
		Dind: &eruncommon.RuntimeDindUsage{
			CPU:    eruncommon.RuntimeCPUUsage{Unavailable: noQuota, UsageUsec: 385_919_164},
			Memory: eruncommon.RuntimeMemoryUsage{CurrentBytes: 512 << 20, Unlimited: true},
		},
	})

	if usage.Dind == nil {
		t.Fatal("the sidecar reading must survive even when its CPU had no quota")
	}
	if usage.Dind.CPU.Available {
		t.Fatalf("no cpu.max quota means no utilisation, got %+v", usage.Dind.CPU)
	}
	if usage.Dind.CPU.Unavailable != noQuota {
		t.Fatalf("the reader's own reason must be carried, got %q", usage.Dind.CPU.Unavailable)
	}
	if usage.Dind.CPU.UsageUsec != 385_919_164 {
		t.Fatalf("the cumulative counter is the only CPU figure this reading has, and it was dropped: %+v", usage.Dind.CPU)
	}

	// No sidecar read at all stays nil rather than becoming a zero reading: an
	// older runtime image, a sidecar mid-restart, and a genuinely idle sidecar
	// must not arrive looking the same.
	withoutSidecar := uiRuntimeUsageFromReading(eruncommon.RuntimeUsage{
		Tenant:      "erun",
		Environment: "remote",
		CPU:         eruncommon.RuntimeCPUUsage{QuotaCores: 12, UtilizationPercent: 0.6},
		Memory:      eruncommon.RuntimeMemoryUsage{CurrentBytes: 1 << 30, LimitBytes: 23 << 30},
	})
	if withoutSidecar.Dind != nil {
		t.Fatalf("an environment with no sidecar reading must not carry one, got %+v", withoutSidecar.Dind)
	}
}
