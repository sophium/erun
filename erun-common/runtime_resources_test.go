package eruncommon

import "testing"

func TestNormalizeRuntimePodResourcesUsesDefaults(t *testing.T) {
	got := NormalizeRuntimePodResources(RuntimePodResources{})
	if got.CPU != DefaultRuntimePodCPU || got.Memory != DefaultRuntimePodMemory {
		t.Fatalf("unexpected defaults: %+v", got)
	}
}

func TestParseKubernetesResourceQuantities(t *testing.T) {
	cpu, err := ParseKubernetesCPUToMilli("250m")
	if err != nil || cpu != 250 {
		t.Fatalf("unexpected CPU parse: cpu=%d err=%v", cpu, err)
	}
	memory, err := ParseKubernetesMemoryToMi("2Gi")
	if err != nil || memory != 2048 {
		t.Fatalf("unexpected memory parse: memory=%d err=%v", memory, err)
	}
}
