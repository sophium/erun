package eruncommon

import "testing"

// The derivation is the whole point of the fix it belongs to: a build is
// capped by the erun-dind sidecar's CPU limit, so a fixed default throttles
// every build to that number whatever the node can give. These pin the two
// properties that make a derived value safe to ship — it scales with the
// machine, and it is never larger than the machine.
func TestDeriveRuntimeDindCPU(t *testing.T) {
	tests := []struct {
		name     string
		hostCPUs int
		want     string
	}{
		{"reference build node", 24, "22"},
		{"small node", 8, "6"},
		{"four cores", 4, "2"},
		{"two cores floors at one", 2, "1"},
		{"single core floors at one", 1, "1"},
		{"unreadable host floors at one", 0, "1"},
		{"large node", 128, "126"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := DeriveRuntimeDindCPU(test.hostCPUs); got != test.want {
				t.Errorf("DeriveRuntimeDindCPU(%d) = %q, want %q", test.hostCPUs, got, test.want)
			}
		})
	}
}

// A derived value must never exceed the node it was derived from: the limit is
// a ceiling, and a ceiling above the machine is a promise the machine cannot
// keep.
func TestDeriveRuntimeDindCPUNeverExceedsTheHost(t *testing.T) {
	for hostCPUs := 1; hostCPUs <= 256; hostCPUs++ {
		milli, err := ParseKubernetesCPUToMilli(DeriveRuntimeDindCPU(hostCPUs))
		if err != nil {
			t.Fatalf("hostCPUs %d: %v", hostCPUs, err)
		}
		if milli > int64(hostCPUs)*1000 {
			t.Fatalf("hostCPUs %d derived %d milli, above the host", hostCPUs, milli)
		}
	}
}

func TestResolveRuntimeDindPodResources(t *testing.T) {
	quotaWith := func(cpu string) NamespaceResourceQuota {
		return NamespaceResourceQuota{CPU: cpu, Memory: "32Gi", Storage: "80Gi"}
	}

	tests := []struct {
		name       string
		configured RuntimePodResources
		runtimePod RuntimePodResources
		ceiling    NamespaceResourceQuota
		hostCPUs   int
		want       RuntimePodResources
	}{
		{
			name:     "unset CPU is derived from the machine",
			hostCPUs: 24,
			want:     RuntimePodResources{CPU: "22", Memory: DefaultRuntimeDindMemory},
		},
		{
			name:       "a recorded CPU always wins",
			configured: RuntimePodResources{CPU: "16"},
			hostCPUs:   24,
			want:       RuntimePodResources{CPU: "16", Memory: DefaultRuntimeDindMemory},
		},
		{
			name:       "a recorded CPU below the derivation still wins",
			configured: RuntimePodResources{CPU: "4"},
			hostCPUs:   24,
			want:       RuntimePodResources{CPU: "4", Memory: DefaultRuntimeDindMemory},
		},
		{
			name:       "recorded memory does not suppress a derived CPU",
			configured: RuntimePodResources{Memory: "24Gi"},
			hostCPUs:   24,
			want:       RuntimePodResources{CPU: "22", Memory: "24Gi"},
		},
		{
			name:     "small machine derives a small limit",
			hostCPUs: 4,
			want:     RuntimePodResources{CPU: "2", Memory: DefaultRuntimeDindMemory},
		},
		{
			name:       "the derived value is clamped to the namespace quota",
			runtimePod: RuntimePodResources{CPU: "4"},
			ceiling:    quotaWith("10"),
			hostCPUs:   24,
			want:       RuntimePodResources{CPU: "6", Memory: DefaultRuntimeDindMemory},
		},
		{
			name:       "a namespace with no room still derives a positive CPU",
			runtimePod: RuntimePodResources{CPU: "4"},
			ceiling:    quotaWith("2"),
			hostCPUs:   24,
			want:       RuntimePodResources{CPU: "1", Memory: DefaultRuntimeDindMemory},
		},
		{
			name:       "an explicit value is not clamped to the quota",
			configured: RuntimePodResources{CPU: "16"},
			runtimePod: RuntimePodResources{CPU: "4"},
			ceiling:    quotaWith("10"),
			hostCPUs:   24,
			want:       RuntimePodResources{CPU: "16", Memory: DefaultRuntimeDindMemory},
		},
		{
			name:       "a quota large enough leaves the derivation alone",
			runtimePod: RuntimePodResources{CPU: "4"},
			ceiling:    quotaWith("64"),
			hostCPUs:   24,
			want:       RuntimePodResources{CPU: "22", Memory: DefaultRuntimeDindMemory},
		},
		{
			name:     "an unset runtime pod is counted at its own default",
			ceiling:  quotaWith("10"),
			hostCPUs: 24,
			want:     RuntimePodResources{CPU: "6", Memory: DefaultRuntimeDindMemory},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ResolveRuntimeDindPodResources(test.configured, test.runtimePod, test.ceiling, test.hostCPUs)
			if got != test.want {
				t.Errorf("ResolveRuntimeDindPodResources() = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestHostCPUCountPrefersTheOverride(t *testing.T) {
	t.Setenv(HostCPUCountEnvVar, "24")
	if got := HostCPUCount(); got != 24 {
		t.Errorf("HostCPUCount() = %d, want the pinned 24", got)
	}
	t.Setenv(HostCPUCountEnvVar, "not-a-number")
	if got := HostCPUCount(); got <= 0 {
		t.Errorf("HostCPUCount() = %d, want the machine's own count when the override is unreadable", got)
	}
}
