package runtimeview

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestRuntimeUsageCPUUnavailableRendersAsStatedUnavailable(t *testing.T) {
	cpu := uiRuntimeCPUUsageFromReading(model.RuntimeCPUUsage{
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

func TestRuntimeUsageMemoryUnlimitedRendersAsUnlimitedNotZeroPercent(t *testing.T) {
	memory := uiRuntimeMemoryUsageFromReading(model.RuntimeMemoryUsage{
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

func TestRuntimeUsageMemoryUnavailableRendersAsStatedUnavailable(t *testing.T) {
	memory := uiRuntimeMemoryUsageFromReading(model.RuntimeMemoryUsage{
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

func TestRuntimeUsageDiskUnavailableRendersAsStatedUnavailable(t *testing.T) {
	disk := uiRuntimeDiskUsageFromReading(model.RuntimeDiskUsage{
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

func TestRuntimeUsageFromReadingMixedAvailability(t *testing.T) {
	usage := uiRuntimeUsageFromReading(model.RuntimeUsage{
		Tenant:      "petios",
		Environment: "local",
		CPU:         model.RuntimeCPUUsage{Unavailable: "cpu.max reports no quota (unlimited or not readable); utilisation needs a quota to measure against"},
		Memory: model.RuntimeMemoryUsage{
			CurrentBytes:   1024 * 1024 * 1024,
			LimitBytes:     2048 * 1024 * 1024,
			PercentOfLimit: 50,
			OOMKills:       2,
		},
		Disk: []model.RuntimeDiskUsage{{
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

// Uses the real wiring: the expired parent ctx triggers the timeout before kubectl runs.
func TestLoadRuntimeUsageReportsOwnTimeoutNotSignalKilled(t *testing.T) {
	store := fakeStore{
		tenants: map[string]model.TenantConfig{
			"petios": {Name: "petios", DefaultEnvironment: "local"},
		},
		envs: map[string]config.EnvConfig{
			"petios/local": {Name: "local", LocalRepoPath: t.TempDir(), KubernetesContext: "test-context"},
		},
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
	defer cancel()
	service := newTestService(Deps{
		Store:   store,
		Context: func() context.Context { return ctx },
		LoadUsage: func(ctx context.Context, selection uimodel.Selection) (Usage, error) {
			return ReadUsageViaKubectl(ctx, store, selection)
		},
	})

	usage, err := service.LoadRuntimeUsage(uimodel.Selection{Tenant: "petios", Environment: "local"})
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

// Image builds run in the dind sidecar's cgroup, so the runtime container alone
// reads idle during a build. Asserted on the JSON the popover receives.
func TestRuntimeUsageCarriesTheDindSidecarSoAnActiveBuildIsNotAnIdleReading(t *testing.T) {
	usage := uiRuntimeUsageFromReading(model.RuntimeUsage{
		Tenant:         "erun",
		Environment:    "build",
		CPU:            model.RuntimeCPUUsage{QuotaCores: 12, UtilizationPercent: 0.2},
		Memory:         model.RuntimeMemoryUsage{CurrentBytes: 1 << 30, LimitBytes: 23 << 30, PercentOfLimit: 2},
		ExcludesBuilds: true,
		Dind: &model.RuntimeDindUsage{
			CPU:    model.RuntimeCPUUsage{QuotaCores: 8, UtilizationPercent: 91.5},
			Memory: model.RuntimeMemoryUsage{CurrentBytes: 19 << 30, LimitBytes: 20 << 30, PercentOfLimit: 97},
		},
	})

	payload, err := json.Marshal(usage)
	if err != nil {
		t.Fatalf("marshal Usage: %v", err)
	}
	var wire struct {
		ExcludesBuilds bool `json:"excludesBuilds"`
		Dind           *struct {
			CPU    CPUUsage    `json:"cpu"`
			Memory MemoryUsage `json:"memory"`
		} `json:"dind"`
	}
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatalf("unmarshal Usage payload: %v", err)
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

func TestRuntimeUsageCarriesTheSidecarCumulativeCPUWithNoQuota(t *testing.T) {
	const noQuota = "cpu.max reports no quota (unlimited or not readable); utilisation needs a quota to measure against"
	usage := uiRuntimeUsageFromReading(model.RuntimeUsage{
		Tenant:         "erun",
		Environment:    "build",
		CPU:            model.RuntimeCPUUsage{QuotaCores: 12, UtilizationPercent: 0.6},
		Memory:         model.RuntimeMemoryUsage{CurrentBytes: 1 << 30, LimitBytes: 23 << 30, PercentOfLimit: 2},
		ExcludesBuilds: true,
		Dind: &model.RuntimeDindUsage{
			CPU:    model.RuntimeCPUUsage{Unavailable: noQuota, UsageUsec: 385_919_164},
			Memory: model.RuntimeMemoryUsage{CurrentBytes: 512 << 20, Unlimited: true},
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

	// No sidecar reading stays nil, not a zero reading.
	withoutSidecar := uiRuntimeUsageFromReading(model.RuntimeUsage{
		Tenant:      "erun",
		Environment: "remote",
		CPU:         model.RuntimeCPUUsage{QuotaCores: 12, UtilizationPercent: 0.6},
		Memory:      model.RuntimeMemoryUsage{CurrentBytes: 1 << 30, LimitBytes: 23 << 30},
	})
	if withoutSidecar.Dind != nil {
		t.Fatalf("an environment with no sidecar reading must not carry one, got %+v", withoutSidecar.Dind)
	}
}

func TestRuntimeUsageCarriesTheRuntimeContainersOwnRequest(t *testing.T) {
	usage := uiRuntimeUsageFromReading(model.RuntimeUsage{
		Tenant:      "erun",
		Environment: "code1",
		Requests: &model.RuntimeUsageRequests{
			Containers: map[string]model.KubernetesRequests{
				runtimetarget.DevopsComponentName: {CPUMilli: 250, MemoryBytes: 1024 * (1 << 20)},
				"erun-dind":                       {CPUMilli: 250, MemoryBytes: 1024 * (1 << 20)},
			},
			Pod: model.KubernetesRequests{CPUMilli: 500, MemoryBytes: 2048 * (1 << 20)},
		},
	})
	if usage.Requests == nil {
		t.Fatalf("expected the reservation reading to reach the UI model, got nil")
	}
	if usage.Requests.Runtime.CPU != "0.25 CPU" || usage.Requests.Runtime.Memory == "" {
		t.Fatalf("expected the runtime container's own rendered request, got %+v", usage.Requests.Runtime)
	}
	if usage.Requests.Runtime.MemoryBytes != 1024*(1<<20) {
		t.Fatalf("expected the runtime container's own 1GiB request, got %+v", usage.Requests.Runtime)
	}
}

func TestRuntimeUsageDistinguishesAnUnreadReservationFromNone(t *testing.T) {
	absent := uiRuntimeUsageFromReading(model.RuntimeUsage{Tenant: "erun", Environment: "code1"})
	if absent.Requests != nil {
		t.Fatalf("expected no Requests when the shared reading carries none, got %+v", absent.Requests)
	}
	unread := uiRuntimeUsageFromReading(model.RuntimeUsage{
		Tenant:      "erun",
		Environment: "code1",
		Requests:    &model.RuntimeUsageRequests{Unavailable: "kubectl get pods: connection refused"},
	})
	if unread.Requests == nil || unread.Requests.Unavailable == "" {
		t.Fatalf("expected the unread reservation to state its reason, got %+v", unread.Requests)
	}
	if unread.Requests.Runtime.CPU != "" || unread.Requests.Runtime.Memory != "" {
		t.Fatalf("an unread reservation must not render a zero request, got %+v", unread.Requests.Runtime)
	}
}
