package main

import (
	"strings"
	"testing"
)

func TestRuntimeResourceStatusUsesBestAvailableNode(t *testing.T) {
	var nodeA kubernetesNode
	nodeA.Metadata.Name = "node-a"
	nodeA.Status.Allocatable.CPU = "8"
	nodeA.Status.Allocatable.Memory = "16Gi"
	var nodeB kubernetesNode
	nodeB.Metadata.Name = "node-b"
	nodeB.Status.Allocatable.CPU = "4"
	nodeB.Status.Allocatable.Memory = "8Gi"

	var pod kubernetesPod
	pod.Spec.NodeName = "node-a"
	pod.Spec.Containers = []kubernetesContainer{{}}
	pod.Spec.Containers[0].Resources.Limits.CPU = "2"
	pod.Spec.Containers[0].Resources.Limits.Memory = "4Gi"

	status := runtimeResourceStatusFromKubernetes(uiRuntimeResourceInput{KubernetesContext: "cluster"}, kubernetesNodeList{Items: []kubernetesNode{nodeA, nodeB}}, kubernetesPodList{Items: []kubernetesPod{pod}}, nil)
	if !status.Available {
		t.Fatalf("expected status to be available: %+v", status)
	}
	if status.CPU.Free != 6 || status.Memory.Free != 12 {
		t.Fatalf("unexpected best free capacity: %+v", status)
	}
}

func TestRuntimeResourceStatusExcludesCurrentRuntimePodAllocation(t *testing.T) {
	var node kubernetesNode
	node.Metadata.Name = "node-a"
	node.Status.Allocatable.CPU = "8"
	node.Status.Allocatable.Memory = "16Gi"

	var runtimePod kubernetesPod
	runtimePod.Metadata.Namespace = "team-dev"
	runtimePod.Spec.NodeName = "node-a"
	// The runtime container name is fixed regardless of tenant, so pairing it
	// with a non-erun tenant guards the matcher against a release-name regression.
	runtimePod.Spec.Containers = []kubernetesContainer{{Name: "erun-devops"}}
	runtimePod.Spec.Containers[0].Resources.Limits.CPU = "4"
	runtimePod.Spec.Containers[0].Resources.Limits.Memory = "8Gi"

	var otherPod kubernetesPod
	otherPod.Metadata.Namespace = "other"
	otherPod.Spec.NodeName = "node-a"
	otherPod.Spec.Containers = []kubernetesContainer{{Name: "other"}}
	otherPod.Spec.Containers[0].Resources.Limits.CPU = "2"
	otherPod.Spec.Containers[0].Resources.Limits.Memory = "4Gi"

	status := runtimeResourceStatusFromKubernetes(
		uiRuntimeResourceInput{KubernetesContext: "cluster", Tenant: "team", Environment: "dev"},
		kubernetesNodeList{Items: []kubernetesNode{node}},
		kubernetesPodList{Items: []kubernetesPod{runtimePod, otherPod}},
		nil,
	)
	if status.CPU.Free != 6 || status.Memory.Free != 12 {
		t.Fatalf("expected current runtime allocation to be reusable, got %+v", status)
	}
}

func TestRuntimeResourceStatusKeepsCurrentRuntimeAllocationAsMinimumCapacity(t *testing.T) {
	var node kubernetesNode
	node.Metadata.Name = "node-a"
	node.Status.Allocatable.CPU = "4"
	node.Status.Allocatable.Memory = "16Gi"

	var runtimePod kubernetesPod
	runtimePod.Metadata.Namespace = "team-dev"
	runtimePod.Spec.NodeName = "node-a"
	// The runtime container name is fixed regardless of tenant, so pairing it
	// with a non-erun tenant guards the matcher against a release-name regression.
	runtimePod.Spec.Containers = []kubernetesContainer{{Name: "erun-devops"}}
	runtimePod.Spec.Containers[0].Resources.Limits.CPU = "4"
	runtimePod.Spec.Containers[0].Resources.Limits.Memory = "8Gi"

	var otherPod kubernetesPod
	otherPod.Metadata.Namespace = "other"
	otherPod.Spec.NodeName = "node-a"
	otherPod.Spec.Containers = []kubernetesContainer{{Name: "other"}}
	otherPod.Spec.Containers[0].Resources.Limits.CPU = "4"
	otherPod.Spec.Containers[0].Resources.Limits.Memory = "12Gi"

	status := runtimeResourceStatusFromKubernetes(
		uiRuntimeResourceInput{KubernetesContext: "cluster", Tenant: "team", Environment: "dev"},
		kubernetesNodeList{Items: []kubernetesNode{node}},
		kubernetesPodList{Items: []kubernetesPod{runtimePod, otherPod}},
		nil,
	)
	if status.CPU.Free != 4 || status.Memory.Free != 8 {
		t.Fatalf("expected current runtime allocation to remain selectable when node is overcommitted, got %+v", status)
	}
	// The arithmetic above is correct but renders as a hard ceiling equal to
	// what the environment already has, which reads as "this is the maximum
	// this environment supports". The reading must say what actually happened
	// and name the remedy this PR added.
	if !status.Floored || !status.CPU.Floored || !status.Memory.Floored {
		t.Fatalf("a free value clamped up to the env's own limit must be marked floored: %+v", status)
	}
	if !strings.Contains(status.Notice, "fully committed") {
		t.Fatalf("floored reading must explain that the node is full, got %q", status.Notice)
	}
	if !strings.Contains(status.Notice, "Stopping an environment nobody is using") {
		t.Fatalf("floored reading must name stopping an idle environment as the remedy, got %q", status.Notice)
	}
	if !strings.HasPrefix(status.Message, "Right now on node-a:") {
		t.Fatalf("the figure must read as a live snapshot of a named node, got %q", status.Message)
	}
}

// TestRuntimeResourceStatusCountsLimitlessContainersAtMeasuredUsage covers the
// erun-dind case: the sidecar declares no limits at all, so a limits-only sum
// counted its Testcontainers and buildkit cache as zero and reported capacity
// the node did not have.
func TestRuntimeResourceStatusCountsLimitlessContainersAtMeasuredUsage(t *testing.T) {
	var node kubernetesNode
	node.Metadata.Name = "node-a"
	node.Status.Allocatable.CPU = "8"
	node.Status.Allocatable.Memory = "16Gi"

	var neighbour kubernetesPod
	neighbour.Metadata.Name = "other-devops-1"
	neighbour.Metadata.Namespace = "other"
	neighbour.Spec.NodeName = "node-a"
	neighbour.Spec.Containers = []kubernetesContainer{{Name: "erun-devops"}, {Name: "erun-dind"}}
	neighbour.Spec.Containers[0].Resources.Limits.CPU = "2"
	neighbour.Spec.Containers[0].Resources.Limits.Memory = "4Gi"
	// erun-dind declares nothing; only measurement can see what it holds.

	measured := kubernetesContainerUsage{
		"other/other-devops-1/erun-dind": {CPUMilli: 1000, MemoryMi: 6144},
	}

	status := runtimeResourceStatusFromKubernetes(
		uiRuntimeResourceInput{KubernetesContext: "cluster"},
		kubernetesNodeList{Items: []kubernetesNode{node}},
		kubernetesPodList{Items: []kubernetesPod{neighbour}},
		measured,
	)
	if status.CPU.Free != 5 || status.Memory.Free != 6 {
		t.Fatalf("limitless container must be counted at its measured usage, got %+v", status)
	}
	if !status.MeasuredUsage {
		t.Fatalf("a reading backed by a metrics source must say so: %+v", status)
	}
	if status.UnmeasuredContainers != 0 {
		t.Fatalf("a measured container is accounted for, not unaccounted: %+v", status)
	}
	if status.Notice != "" {
		t.Fatalf("a fully accounted, unfloored reading needs no notice, got %q", status.Notice)
	}
}

// TestRuntimeResourceStatusSurfacesUnaccountedContainers is the same case with
// no metrics source: the figure cannot be exact, and must say so rather than
// silently treating the limitless container as consuming nothing.
func TestRuntimeResourceStatusSurfacesUnaccountedContainers(t *testing.T) {
	var node kubernetesNode
	node.Metadata.Name = "node-a"
	node.Status.Allocatable.CPU = "8"
	node.Status.Allocatable.Memory = "16Gi"

	var neighbour kubernetesPod
	neighbour.Metadata.Name = "other-devops-1"
	neighbour.Metadata.Namespace = "other"
	neighbour.Spec.NodeName = "node-a"
	neighbour.Spec.Containers = []kubernetesContainer{{Name: "erun-devops"}, {Name: "erun-dind"}}
	neighbour.Spec.Containers[0].Resources.Limits.CPU = "2"
	neighbour.Spec.Containers[0].Resources.Limits.Memory = "4Gi"

	status := runtimeResourceStatusFromKubernetes(
		uiRuntimeResourceInput{KubernetesContext: "cluster"},
		kubernetesNodeList{Items: []kubernetesNode{node}},
		kubernetesPodList{Items: []kubernetesPod{neighbour}},
		nil,
	)
	if status.MeasuredUsage {
		t.Fatalf("no metrics source must not be reported as measured: %+v", status)
	}
	if status.UnmeasuredContainers != 1 {
		t.Fatalf("expected the limitless container to be counted as unaccounted, got %+v", status)
	}
	if !strings.Contains(status.Notice, "1 container on this node declare") {
		t.Fatalf("the reading must name how much it cannot see, got %q", status.Notice)
	}
	if !strings.Contains(status.Notice, "real usage is higher than shown") {
		t.Fatalf("the reading must not present itself as exact, got %q", status.Notice)
	}
}

func TestParseKubernetesContainerUsage(t *testing.T) {
	usage := parseKubernetesContainerUsage(strings.Join([]string{
		"erun-local   erun-devops-7c9   erun-devops   250m   3891Mi",
		"erun-local   erun-devops-7c9   erun-dind     1500m  6144Mi",
		"malformed row",
	}, "\n"))
	if len(usage) != 2 {
		t.Fatalf("expected two measured containers, got %+v", usage)
	}
	if got := usage["erun-local/erun-devops-7c9/erun-dind"]; got.CPUMilli != 1500 || got.MemoryMi != 6144 {
		t.Fatalf("unexpected dind measurement: %+v", got)
	}
	if parseKubernetesContainerUsage("") != nil {
		t.Fatalf("an empty metrics read must yield no usage map at all")
	}
}

func TestRuntimeResourceStatusFormatsZeroCPUCapacity(t *testing.T) {
	var node kubernetesNode
	node.Metadata.Name = "node-a"
	node.Status.Allocatable.CPU = "4"
	node.Status.Allocatable.Memory = "8Gi"

	var pod kubernetesPod
	pod.Metadata.Namespace = "other"
	pod.Spec.NodeName = "node-a"
	pod.Spec.Containers = []kubernetesContainer{{Name: "other"}}
	pod.Spec.Containers[0].Resources.Limits.CPU = "4"
	pod.Spec.Containers[0].Resources.Limits.Memory = "1Gi"

	status := runtimeResourceStatusFromKubernetes(
		uiRuntimeResourceInput{KubernetesContext: "cluster", Tenant: "team", Environment: "dev"},
		kubernetesNodeList{Items: []kubernetesNode{node}},
		kubernetesPodList{Items: []kubernetesPod{pod}},
		nil,
	)
	if status.CPU.Free != 0 || status.CPU.Formatted != "0" {
		t.Fatalf("expected zero CPU to be visible, got %+v", status.CPU)
	}
	if !strings.Contains(status.Message, "0 CPU") {
		t.Fatalf("expected message to include zero CPU, got %q", status.Message)
	}
}

func TestRuntimeResourceStatusIgnoresTerminalPodAllocation(t *testing.T) {
	var node kubernetesNode
	node.Metadata.Name = "node-a"
	node.Status.Allocatable.CPU = "4"
	node.Status.Allocatable.Memory = "16140084Ki"

	var completedInstallPod kubernetesPod
	completedInstallPod.Metadata.Namespace = "kube-system"
	completedInstallPod.Status.Phase = "Succeeded"
	completedInstallPod.Spec.NodeName = "node-a"
	completedInstallPod.Spec.Containers = []kubernetesContainer{{Name: "helm"}}
	completedInstallPod.Spec.Containers[0].Resources.Limits.CPU = "32"
	completedInstallPod.Spec.Containers[0].Resources.Limits.Memory = "32G"

	var failedInstallPod kubernetesPod
	failedInstallPod.Metadata.Namespace = "kube-system"
	failedInstallPod.Status.Phase = "Failed"
	failedInstallPod.Spec.NodeName = "node-a"
	failedInstallPod.Spec.Containers = []kubernetesContainer{{Name: "helm"}}
	failedInstallPod.Spec.Containers[0].Resources.Limits.CPU = "32"
	failedInstallPod.Spec.Containers[0].Resources.Limits.Memory = "32G"

	status := runtimeResourceStatusFromKubernetes(
		uiRuntimeResourceInput{KubernetesContext: "cluster", Tenant: "team", Environment: "dev"},
		kubernetesNodeList{Items: []kubernetesNode{node}},
		kubernetesPodList{Items: []kubernetesPod{completedInstallPod, failedInstallPod}},
		nil,
	)
	if !status.Available {
		t.Fatalf("expected status to be available: %+v", status)
	}
	if status.CPU.Free != 4 || status.Memory.Free != 15.4 {
		t.Fatalf("expected terminal pod limits to be ignored, got %+v", status)
	}
}
