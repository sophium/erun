package eruncommon

import "testing"

// TestRuntimeDindCPULimitSizesFromNodeCapacityAndCoTenants is the sizing rule
// itself: the node's CPUs divided by the build-capable environments expected
// to be building on it at once, floored at MinimumRuntimeDindCPU and rounded
// up to whole cores. Both inputs are injected so the rule is exercised
// independently of whatever node the fleet currently runs on -- the rule is
// the contract, the reference node's own answer (DefaultRuntimeDindCPU) is
// only one point on it.
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
			// 24000/12 is 2 cores, which is under the floor. A cap below it
			// throttles every build on a node that is otherwise idle, which is
			// the shape this rule replaced; the floor is a ceiling that is
			// simply never reached when all twelve really do build, and the
			// proportional sharing that happens then is the kernel's job.
			nodeCPU: 24000, coTenants: 12, wantCPU: "4",
		},
		{
			name:    "a node too small for even one co-tenant still floors",
			nodeCPU: 8000, coTenants: 4, wantCPU: "4",
		},
		{
			name:      "a fractional result rounds up so a build always gets whole cores",
			nodeCPU:   24000,
			coTenants: 5,
			wantCPU:   "5",
		},
		{
			name: "an unestablished node capacity is the floor, not an invented size",
			// Nothing has measured this node; the honest answer is the
			// conservative constant, the same posture every other unresolved
			// dind value takes.
			nodeCPU: 0, coTenants: 2, wantCPU: MinimumRuntimeDindCPU,
		},
		{
			name: "a missing co-tenant count is one, not zero",
			// A zero divisor would either panic or resolve an enormous cap.
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
			if got := RuntimeDindCPULimit(tc.nodeCPU, tc.coTenants); got != tc.wantCPU {
				t.Fatalf("RuntimeDindCPULimit(%d, %d) = %q, want %q", tc.nodeCPU, tc.coTenants, got, tc.wantCPU)
			}
		})
	}
}

// TestRuntimeDindCPULimitNeverSizesBelowTheFloor pins the floor as a property
// rather than as one case above: no node capacity or co-tenant count may
// produce a cap under it, so a rule change cannot reintroduce a throttled
// default through an input the table does not happen to name.
func TestRuntimeDindCPULimitNeverSizesBelowTheFloor(t *testing.T) {
	t.Parallel()
	floor, err := ParseKubernetesCPUToMilli(MinimumRuntimeDindCPU)
	if err != nil {
		t.Fatalf("MinimumRuntimeDindCPU %q does not parse as a CPU quantity: %v", MinimumRuntimeDindCPU, err)
	}
	for _, nodeCPU := range []int64{0, 1, 500, 1000, 4000, 8000, 24000, 96000} {
		for _, coTenants := range []int{-1, 0, 1, 2, 4, 8, 64, 1000} {
			got := RuntimeDindCPULimit(nodeCPU, coTenants)
			milli, err := ParseKubernetesCPUToMilli(got)
			if err != nil {
				t.Fatalf("RuntimeDindCPULimit(%d, %d) = %q, which is not a CPU quantity: %v", nodeCPU, coTenants, got, err)
			}
			if milli < floor {
				t.Errorf("RuntimeDindCPULimit(%d, %d) = %q, below the %s floor", nodeCPU, coTenants, got, MinimumRuntimeDindCPU)
			}
			if milli%1000 != 0 {
				t.Errorf("RuntimeDindCPULimit(%d, %d) = %q, not a whole core count", nodeCPU, coTenants, got)
			}
		}
	}
}

// TestDefaultRuntimeDindCPUIsTheRulesAnswerForTheReferenceNode keeps the
// shipped default from drifting away from the rule that is supposed to
// produce it. The default is what every environment that has never been sized
// gets, so a default edited by hand would be a sizing decision nothing
// derives -- exactly what the flat "4" was.
func TestDefaultRuntimeDindCPUIsTheRulesAnswerForTheReferenceNode(t *testing.T) {
	t.Parallel()
	want := RuntimeDindCPULimit(DefaultRuntimeDindCPUNodeCPUMilli, DefaultRuntimeDindCPUCoTenants)
	if DefaultRuntimeDindCPU != want {
		t.Fatalf("DefaultRuntimeDindCPU = %q, but the rule gives %q for a %d milli-CPU node with %d co-tenants",
			DefaultRuntimeDindCPU, want, DefaultRuntimeDindCPUNodeCPUMilli, DefaultRuntimeDindCPUCoTenants)
	}
	if DefaultRuntimeDindCPU == MinimumRuntimeDindCPU {
		t.Fatalf("DefaultRuntimeDindCPU is still the floor (%q); the reference node is sized for a node larger than the "+
			"smallest one this rule will size, so the two cannot be the same value without the default being flat again",
			MinimumRuntimeDindCPU)
	}
}

// TestMinimumRuntimeNamespaceQuotaAdmitsTheDefaultPod is the coupling the
// default's own size moves: a namespace ResourceQuota counts every container
// in the pod, so a tenant quota at the floor has to admit the dind sidecar at
// the *sized* default rather than at the old constant.
func TestMinimumRuntimeNamespaceQuotaAdmitsTheDefaultPod(t *testing.T) {
	t.Parallel()
	cpu, _, _ := MinimumRuntimeNamespaceQuota()
	runtimeCPU, err := ParseKubernetesCPUToMilli(DefaultRuntimePodCPU)
	if err != nil {
		t.Fatal(err)
	}
	dindCPU, err := ParseKubernetesCPUToMilli(DefaultRuntimeDindCPU)
	if err != nil {
		t.Fatal(err)
	}
	if cpu != runtimeCPU+dindCPU {
		t.Fatalf("namespace quota floor is %dm, want %dm (runtime %s + dind %s)", cpu, runtimeCPU+dindCPU, DefaultRuntimePodCPU, DefaultRuntimeDindCPU)
	}
}
