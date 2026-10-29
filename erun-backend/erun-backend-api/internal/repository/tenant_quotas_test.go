package repository

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config/runtimeresources"
)

func parseKubernetesQuantityOrFatal(t *testing.T, parse func(string) (int64, error), value string) int64 {
	t.Helper()
	amount, err := parse(value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return amount
}

// Recomputes the floor from config constants rather than
// config.MinimumRuntimeNamespaceQuota, so a derivation bug fails here.
func TestDefaultTenantQuotaAdmitsTheStockRuntimePod(t *testing.T) {
	devopsCPU := parseKubernetesQuantityOrFatal(t, runtimeresources.ParseKubernetesCPUToMilli, runtimeresources.DefaultRuntimePodCPU)
	dindCPU := parseKubernetesQuantityOrFatal(t, runtimeresources.ParseKubernetesCPUToMilli, runtimeresources.DefaultRuntimeDindCPU)
	devopsMemory := parseKubernetesQuantityOrFatal(t, runtimeresources.ParseKubernetesMemoryToMi, runtimeresources.DefaultRuntimePodMemory)
	dindMemory := parseKubernetesQuantityOrFatal(t, runtimeresources.ParseKubernetesMemoryToMi, runtimeresources.DefaultRuntimeDindMemory)

	wantCPU := int(devopsCPU + dindCPU)
	wantMemory := int(devopsMemory + dindMemory)
	wantStorage := runtimeresources.DefaultRuntimeHomePVCGi + runtimeresources.DefaultRuntimeDockerPVCGi + runtimeresources.DefaultRuntimeWorktreePVCGi

	got := [3]int{DefaultMaxCPUMillicores, DefaultMaxMemoryMB, DefaultMaxStorageGB}
	want := [3]int{wantCPU, wantMemory, wantStorage}
	names := [3]string{"DefaultMaxCPUMillicores", "DefaultMaxMemoryMB", "DefaultMaxStorageGB"}
	for i := range got {
		if got[i] < want[i] {
			t.Fatalf("%s = %d, want >= %d (erun-devops %d/%d + erun-dind %d/%d, or the PVCs)", names[i], got[i], want[i], devopsCPU, devopsMemory, dindCPU, dindMemory)
		}
	}

	// Pinned to today's chart shape.
	if got != [3]int{16000, 36864, 72} {
		t.Fatalf("default tenant quota = %v, want [16000 36864 72]", got)
	}
}

func TestDefaultTenantQuotaAggregateBudgetAccommodatesTheDefaultEnvironmentCount(t *testing.T) {
	cases := []struct {
		name        string
		got         int
		perEnv      int
		description string
	}{
		{"DefaultMaxTotalCPUMillicores", DefaultMaxTotalCPUMillicores, DefaultMaxCPUMillicores, "CPU"},
		{"DefaultMaxTotalMemoryMB", DefaultMaxTotalMemoryMB, DefaultMaxMemoryMB, "memory"},
		{"DefaultMaxTotalStorageGB", DefaultMaxTotalStorageGB, DefaultMaxStorageGB, "storage"},
	}
	for _, tc := range cases {
		want := DefaultMaxEnvironments * tc.perEnv
		if tc.got != want {
			t.Fatalf("%s = %d, want %d (DefaultMaxEnvironments=%d * the default per-environment %s cap %d)", tc.name, tc.got, want, DefaultMaxEnvironments, tc.description, tc.perEnv)
		}
		if tc.got < tc.perEnv {
			t.Fatalf("%s = %d is below a single environment's own cap %d; the default budget could not even admit one", tc.name, tc.got, tc.perEnv)
		}
	}
}
