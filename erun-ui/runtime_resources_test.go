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

	status := runtimeResourceStatusFromKubernetes(uiRuntimeResourceInput{KubernetesContext: "cluster"}, kubernetesNodeList{Items: []kubernetesNode{nodeA, nodeB}}, kubernetesPodList{Items: []kubernetesPod{pod}})
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
	runtimePod.Spec.Containers = []kubernetesContainer{{Name: "team-devops"}}
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
	runtimePod.Spec.Containers = []kubernetesContainer{{Name: "team-devops"}}
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
	)
	if status.CPU.Free != 4 || status.Memory.Free != 8 {
		t.Fatalf("expected current runtime allocation to remain selectable when node is overcommitted, got %+v", status)
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
	)
	if status.CPU.Free != 0 || status.CPU.Formatted != "0" {
		t.Fatalf("expected zero CPU to be visible, got %+v", status.CPU)
	}
	if !strings.Contains(status.Message, "0 CPU") {
		t.Fatalf("expected message to include zero CPU, got %q", status.Message)
	}
}
