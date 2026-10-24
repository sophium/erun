package repository

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
)

// parseKubernetesQuantityOrFatal is a t.Fatal-on-error wrapper so the test
// below reads as a flat list of quantities instead of a repeated error check
// per constant.
func parseKubernetesQuantityOrFatal(t *testing.T, parse func(string) (int64, error), value string) int64 {
	t.Helper()
	amount, err := parse(value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return amount
}

// TestDefaultTenantQuotaAdmitsTheStockRuntimePod pins that an unconfigured tenant's default quota must be enough to admit the
// erun-devops chart's own runtime pod, summed across BOTH its containers
// (a Kubernetes ResourceQuota counts every container in the pod, not just the
// first), plus its PVCs. It recomputes the expected floor independently from
// the individual config constants — not by calling
// config.MinimumRuntimeNamespaceQuota again, which DefaultMaxCPUMillicores
// et al. already derive from — so a bug in that derivation (e.g. forgetting
// the dind sidecar) fails this test instead of passing by construction.
func TestDefaultTenantQuotaAdmitsTheStockRuntimePod(t *testing.T) {
	devopsCPU := parseKubernetesQuantityOrFatal(t, config.ParseKubernetesCPUToMilli, config.DefaultRuntimePodCPU)
	dindCPU := parseKubernetesQuantityOrFatal(t, config.ParseKubernetesCPUToMilli, config.DefaultRuntimeDindCPU)
	devopsMemory := parseKubernetesQuantityOrFatal(t, config.ParseKubernetesMemoryToMi, config.DefaultRuntimePodMemory)
	dindMemory := parseKubernetesQuantityOrFatal(t, config.ParseKubernetesMemoryToMi, config.DefaultRuntimeDindMemory)

	wantCPU := int(devopsCPU + dindCPU)
	wantMemory := int(devopsMemory + dindMemory)
	wantStorage := config.DefaultRuntimeHomePVCGi + config.DefaultRuntimeDockerPVCGi + config.DefaultRuntimeWorktreePVCGi

	got := [3]int{DefaultMaxCPUMillicores, DefaultMaxMemoryMB, DefaultMaxStorageGB}
	want := [3]int{wantCPU, wantMemory, wantStorage}
	names := [3]string{"DefaultMaxCPUMillicores", "DefaultMaxMemoryMB", "DefaultMaxStorageGB"}
	for i := range got {
		if got[i] < want[i] {
			t.Fatalf("%s = %d, want >= %d (erun-devops %d/%d + erun-dind %d/%d, or the PVCs)", names[i], got[i], want[i], devopsCPU, devopsMemory, dindCPU, dindMemory)
		}
	}

	// Pinned literals: today's actual chart shape, so a change to either
	// container's defaults or the PVC sizes is visible here.
	if got != [3]int{16000, 36864, 72} {
		t.Fatalf("default tenant quota = %v, want [16000 36864 72]", got)
	}
}

// TestDefaultTenantQuotaAggregateBudgetAccommodatesTheDefaultEnvironmentCount
// pins the derivation: a tenant with no quota row set can still reach its
// default environment-count cap (DefaultMaxEnvironments) at the default
// per-environment size without also hitting the aggregate ceiling first —
// the aggregate defaults are DefaultMaxEnvironments * the per-environment
// defaults, not an independently chosen number that could drift out of sync.
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
