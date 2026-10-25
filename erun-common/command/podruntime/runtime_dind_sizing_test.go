package podruntime

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config/runtimeresources"
)

func TestRuntimeDindCPULimitSizesFromNodeCapacityAndCoTenants(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		nodeCPU   int64
		coTenants int
		wantCPU   string
	}{
		{name: "one build at a time on a 24-CPU node gets the node", nodeCPU: 24000, coTenants: 1, wantCPU: "24"},
		{name: "the reference node and co-tenancy", nodeCPU: 24000, coTenants: 2, wantCPU: "12"},
		{name: "three co-tenants", nodeCPU: 24000, coTenants: 3, wantCPU: "8"},
		{name: "four co-tenants", nodeCPU: 24000, coTenants: 4, wantCPU: "6"},
		{name: "six co-tenants land exactly on the floor", nodeCPU: 24000, coTenants: 6, wantCPU: "4"},
		{
			name: "many co-tenants clamp to the floor rather than to a fraction of a core",
			// 24000/12 is 2 cores, under the floor.
			nodeCPU: 24000, coTenants: 12, wantCPU: "4",
		},
		{
			name:    "a small node divided across several co-tenants still floors",
			nodeCPU: 8000, coTenants: 4, wantCPU: "4",
		},
		{
			name:      "a fractional result rounds up so a build always gets whole cores",
			nodeCPU:   24000,
			coTenants: 5,
			wantCPU:   "5",
		},
		{
			name:    "an unestablished node capacity is the floor, not an invented size",
			nodeCPU: 0, coTenants: 2, wantCPU: runtimeresources.MinimumRuntimeDindCPU,
		},
		{
			name:    "a missing co-tenant count is one, not zero",
			nodeCPU: 24000, coTenants: 0, wantCPU: "24",
		},
		{
			name:    "a negative co-tenant count is one too",
			nodeCPU: 24000, coTenants: -3, wantCPU: "24",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := runtimeresources.RuntimeDindCPULimit(tc.nodeCPU, tc.coTenants); got != tc.wantCPU {
				t.Fatalf("RuntimeDindCPULimit(%d, %d) = %q, want %q", tc.nodeCPU, tc.coTenants, got, tc.wantCPU)
			}
		})
	}
}

func TestRuntimeDindCPULimitNeverSizesBelowTheFloor(t *testing.T) {
	t.Parallel()
	floor, err := runtimeresources.ParseKubernetesCPUToMilli(runtimeresources.MinimumRuntimeDindCPU)
	if err != nil {
		t.Fatalf("MinimumRuntimeDindCPU %q does not parse as a CPU quantity: %v", runtimeresources.MinimumRuntimeDindCPU, err)
	}
	for _, nodeCPU := range []int64{0, 1, 500, 1000, 4000, 8000, 24000, 96000} {
		for _, coTenants := range []int{-1, 0, 1, 2, 4, 8, 64, 1000} {
			got := runtimeresources.RuntimeDindCPULimit(nodeCPU, coTenants)
			milli, err := runtimeresources.ParseKubernetesCPUToMilli(got)
			if err != nil {
				t.Fatalf("RuntimeDindCPULimit(%d, %d) = %q, which is not a CPU quantity: %v", nodeCPU, coTenants, got, err)
			}
			if milli < floor {
				t.Errorf("RuntimeDindCPULimit(%d, %d) = %q, below the %s floor", nodeCPU, coTenants, got, runtimeresources.MinimumRuntimeDindCPU)
			}
			if milli%1000 != 0 {
				t.Errorf("RuntimeDindCPULimit(%d, %d) = %q, not a whole core count", nodeCPU, coTenants, got)
			}
		}
	}
}

// A namespace ResourceQuota counts every container, so the floor must admit the sized dind default.
func TestMinimumRuntimeNamespaceQuotaAdmitsTheDefaultPod(t *testing.T) {
	t.Parallel()
	cpu, _, _ := runtimeresources.MinimumRuntimeNamespaceQuota()
	runtimeCPU, err := runtimeresources.ParseKubernetesCPUToMilli(runtimeresources.DefaultRuntimePodCPU)
	if err != nil {
		t.Fatal(err)
	}
	dindCPU, err := runtimeresources.ParseKubernetesCPUToMilli(runtimeresources.DefaultRuntimeDindCPU)
	if err != nil {
		t.Fatal(err)
	}
	if cpu != runtimeCPU+dindCPU {
		t.Fatalf("namespace quota floor is %dm, want %dm (runtime %s + dind %s)", cpu, runtimeCPU+dindCPU, runtimeresources.DefaultRuntimePodCPU, runtimeresources.DefaultRuntimeDindCPU)
	}
}
