package runtimeview

import (
	"fmt"
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

	status := runtimeResourceStatusFromKubernetes(ResourceInput{KubernetesContext: "cluster"}, kubernetesNodeList{Items: []kubernetesNode{nodeA, nodeB}}, kubernetesPodList{Items: []kubernetesPod{pod}}, nil)
	if !status.Available {
		t.Fatalf("expected status to be available: %+v", status)
	}
	if status.WorstCase.CPU.Free != 6 || status.WorstCase.Memory.Free != 12 {
		t.Fatalf("unexpected best worst-case free capacity: %+v", status)
	}
	// No requests declared, so the whole allocatable capacity is free.
	if status.Schedulable.CPU.Free != 8 || status.Schedulable.Memory.Free != 16 {
		t.Fatalf("unexpected best schedulable free capacity: %+v", status)
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
	// The container name is tenant-independent; a non-erun tenant guards the matcher.
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
		ResourceInput{KubernetesContext: "cluster", Tenant: "team", Environment: "dev"},
		kubernetesNodeList{Items: []kubernetesNode{node}},
		kubernetesPodList{Items: []kubernetesPod{runtimePod, otherPod}},
		nil,
	)
	if status.WorstCase.CPU.Free != 6 || status.WorstCase.Memory.Free != 12 {
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
		ResourceInput{KubernetesContext: "cluster", Tenant: "team", Environment: "dev"},
		kubernetesNodeList{Items: []kubernetesNode{node}},
		kubernetesPodList{Items: []kubernetesPod{runtimePod, otherPod}},
		nil,
	)
	if status.WorstCase.CPU.Free != 4 || status.WorstCase.Memory.Free != 8 {
		t.Fatalf("expected current runtime allocation to remain selectable when node is overcommitted, got %+v", status)
	}
	if !status.Floored || !status.WorstCase.CPU.Floored || !status.WorstCase.Memory.Floored {
		t.Fatalf("a free value clamped up to the env's own limit must be marked floored: %+v", status)
	}
	if !strings.Contains(status.WorstCase.Notice, "fully committed") {
		t.Fatalf("floored reading must explain that the node is full, got %q", status.WorstCase.Notice)
	}
	if !strings.Contains(status.WorstCase.Notice, "namespace quota") {
		t.Fatalf("floored reading must name the levers that move it, got %q", status.WorstCase.Notice)
	}
	if !strings.HasPrefix(status.Schedulable.Message, "Right now on node-a:") {
		t.Fatalf("the figure must read as a live snapshot of a named node, got %q", status.Schedulable.Message)
	}
}

// erun-dind declares no limits, so only measurement sees its usage.
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

	measured := kubernetesContainerUsage{
		"other/other-devops-1/erun-dind": {CPUMilli: 1000, MemoryMi: 6144},
	}

	status := runtimeResourceStatusFromKubernetes(
		ResourceInput{KubernetesContext: "cluster"},
		kubernetesNodeList{Items: []kubernetesNode{node}},
		kubernetesPodList{Items: []kubernetesPod{neighbour}},
		measured,
	)
	if status.WorstCase.CPU.Free != 5 || status.WorstCase.Memory.Free != 6 {
		t.Fatalf("limitless container must be counted at its measured usage, got %+v", status)
	}
	if !status.MeasuredUsage {
		t.Fatalf("a reading backed by a metrics source must say so: %+v", status)
	}
	if status.UnmeasuredContainers != 0 {
		t.Fatalf("a measured container is accounted for, not unaccounted: %+v", status)
	}
	if status.WorstCase.Notice != "" || status.Schedulable.Notice != "" {
		t.Fatalf("a fully accounted, unfloored reading needs no notice, got %q / %q",
			status.Schedulable.Notice, status.WorstCase.Notice)
	}
}

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
		ResourceInput{KubernetesContext: "cluster"},
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
	if !strings.Contains(status.WorstCase.Notice, "1 container on this node declare") {
		t.Fatalf("the reading must name how much it cannot see, got %q", status.WorstCase.Notice)
	}
	if !strings.Contains(status.WorstCase.Notice, "real usage is higher than shown") {
		t.Fatalf("the reading must not present itself as exact, got %q", status.WorstCase.Notice)
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
		ResourceInput{KubernetesContext: "cluster", Tenant: "team", Environment: "dev"},
		kubernetesNodeList{Items: []kubernetesNode{node}},
		kubernetesPodList{Items: []kubernetesPod{pod}},
		nil,
	)
	if status.WorstCase.CPU.Free != 0 || status.WorstCase.CPU.Formatted != "0" {
		t.Fatalf("expected zero CPU to be visible, got %+v", status.WorstCase.CPU)
	}
	if !strings.Contains(status.WorstCase.Message, "0 CPU") {
		t.Fatalf("expected message to include zero CPU, got %q", status.WorstCase.Message)
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
		ResourceInput{KubernetesContext: "cluster", Tenant: "team", Environment: "dev"},
		kubernetesNodeList{Items: []kubernetesNode{node}},
		kubernetesPodList{Items: []kubernetesPod{completedInstallPod, failedInstallPod}},
		nil,
	)
	if !status.Available {
		t.Fatalf("expected status to be available: %+v", status)
	}
	if status.WorstCase.CPU.Free != 4 || status.WorstCase.Memory.Free != 15.4 {
		t.Fatalf("expected terminal pod limits to be ignored, got %+v", status)
	}
}

// reportingNodeRuntimePod: limits are the operator's sizes; requests are the chart's fixed 250m / 1024Mi.
func reportingNodeRuntimePod(namespace, nodeName string) kubernetesPod {
	var pod kubernetesPod
	pod.Metadata.Namespace = namespace
	pod.Spec.NodeName = nodeName
	pod.Spec.Containers = []kubernetesContainer{{Name: "erun-devops"}, {Name: "erun-dind"}}
	for i, limits := range [][2]string{{"4", "16Gi"}, {"12", "20Gi"}} {
		pod.Spec.Containers[i].Resources.Limits.CPU = limits[0]
		pod.Spec.Containers[i].Resources.Limits.Memory = limits[1]
		pod.Spec.Containers[i].Resources.Requests = map[string]string{"cpu": "250m", "memory": "1024Mi"}
	}
	return pod
}

// Limits sum past allocatable while requests leave most of it free; the schedulable reading must follow requests.
func TestRuntimeResourceStatusReportsSchedulableCapacityFromRequests(t *testing.T) {
	var node kubernetesNode
	node.Metadata.Name = "erun-node1"
	node.Status.Allocatable.CPU = "16"
	node.Status.Allocatable.Memory = "32Gi"

	pods := kubernetesPodList{}
	for i := 0; i < 5; i++ {
		pods.Items = append(pods.Items, reportingNodeRuntimePod(fmt.Sprintf("team%d-dev", i), "erun-node1"))
	}

	status := runtimeResourceStatusFromKubernetes(
		ResourceInput{KubernetesContext: "cluster"},
		kubernetesNodeList{Items: []kubernetesNode{node}},
		pods,
		nil,
	)
	if !status.Available {
		t.Fatalf("expected an available reading: %+v", status)
	}

	// 5 x (250m + 250m) of 16 cores; 5 x 2Gi of 32Gi.
	if status.Schedulable.CPU.Free != 13.5 {
		t.Fatalf("schedulable CPU free = %v, want 13.5 (16 allocatable minus 2.5 requested)", status.Schedulable.CPU.Free)
	}
	if status.Schedulable.Memory.Free != 22 {
		t.Fatalf("schedulable memory free = %v, want 22 (32Gi allocatable minus 10Gi requested)", status.Schedulable.Memory.Free)
	}

	if status.WorstCase.CPU.Free != 0 || status.WorstCase.Memory.Free != 0 {
		t.Fatalf("worst-case free = %v CPU / %v GiB, want the limits sum to exhaust the node",
			status.WorstCase.CPU.Free, status.WorstCase.Memory.Free)
	}

	if !strings.Contains(status.Schedulable.Message, "scheduler can admit 13.5 CPU") {
		t.Fatalf("schedulable message does not state the scheduling figure: %q", status.Schedulable.Message)
	}
	if !strings.Contains(status.WorstCase.Message, "declared limit") {
		t.Fatalf("worst-case message does not label itself as the bursting reading: %q", status.WorstCase.Message)
	}

	assertCapacityRemedyIsNotASmallerLimit(t, status.WorstCase.Notice)
}

// A smaller limit is never the remedy: it brings back OOM kills of the agent's build.
func assertCapacityRemedyIsNotASmallerLimit(t *testing.T, notice string) {
	t.Helper()
	if !strings.Contains(notice, "namespace quota") {
		t.Fatalf("notice does not name the lever that moves it: %q", notice)
	}
	if strings.Contains(notice, "lower your request") {
		t.Fatalf("notice offers the harmful remedy: %q", notice)
	}
}

func TestRuntimeResourceStatusNeverReadsAnUnreadableRequestAsZero(t *testing.T) {
	var node kubernetesNode
	node.Metadata.Name = "node-a"
	node.Status.Allocatable.CPU = "16"
	node.Status.Allocatable.Memory = "32Gi"

	pod := reportingNodeRuntimePod("team-dev", "node-a")
	pod.Spec.Containers[0].Resources.Requests = map[string]string{"cpu": "plenty", "memory": "1024Mi"}

	status := runtimeResourceStatusFromKubernetes(
		ResourceInput{KubernetesContext: "cluster"},
		kubernetesNodeList{Items: []kubernetesNode{node}},
		kubernetesPodList{Items: []kubernetesPod{pod}},
		nil,
	)
	if status.SchedulableComplete {
		t.Fatalf("expected the request reading to report itself incomplete: %+v", status)
	}
	if status.UnreadableRequests != 1 {
		t.Fatalf("unreadable requests = %d, want 1", status.UnreadableRequests)
	}
	if !strings.Contains(status.Schedulable.Message, "an upper bound") {
		t.Fatalf("schedulable message does not bound its own figure: %q", status.Schedulable.Message)
	}
	if !strings.Contains(status.Schedulable.Notice, "1 pod on this node declares") {
		t.Fatalf("schedulable notice does not say what it could not read: %q", status.Schedulable.Notice)
	}
}

// Admission is max(init containers, sum of containers).
func TestRuntimeResourceStatusCountsTheInitPhaseInThePodRequest(t *testing.T) {
	var node kubernetesNode
	node.Metadata.Name = "node-a"
	node.Status.Allocatable.CPU = "8"
	node.Status.Allocatable.Memory = "16Gi"

	var pod kubernetesPod
	pod.Spec.NodeName = "node-a"
	pod.Spec.Containers = []kubernetesContainer{{Name: "erun-devops"}}
	pod.Spec.Containers[0].Resources.Requests = map[string]string{"cpu": "250m", "memory": "1024Mi"}
	pod.Spec.InitContainers = []kubernetesContainer{{Name: "prepare-volumes"}}
	pod.Spec.InitContainers[0].Resources.Requests = map[string]string{"cpu": "2", "memory": "4Gi"}

	status := runtimeResourceStatusFromKubernetes(
		ResourceInput{KubernetesContext: "cluster"},
		kubernetesNodeList{Items: []kubernetesNode{node}},
		kubernetesPodList{Items: []kubernetesPod{pod}},
		nil,
	)
	if status.Schedulable.CPU.Free != 6 {
		t.Fatalf("schedulable CPU free = %v, want 6 (8 allocatable minus the init phase's 2)", status.Schedulable.CPU.Free)
	}
	if status.Schedulable.Memory.Free != 12 {
		t.Fatalf("schedulable memory free = %v, want 12 (16Gi minus the init phase's 4Gi)", status.Schedulable.Memory.Free)
	}
}

func TestRuntimeResourceStatusKeepsAnExistingEnvironmentResizableOnARequestFullNode(t *testing.T) {
	var node kubernetesNode
	node.Metadata.Name = "node-a"
	node.Status.Allocatable.CPU = "8"
	node.Status.Allocatable.Memory = "16Gi"

	var target kubernetesPod
	target.Metadata.Namespace = "team-dev"
	target.Spec.NodeName = "node-a"
	target.Spec.Containers = []kubernetesContainer{{Name: "erun-devops"}}
	target.Spec.Containers[0].Resources.Limits.CPU = "4"
	target.Spec.Containers[0].Resources.Limits.Memory = "8Gi"
	target.Spec.Containers[0].Resources.Requests = map[string]string{"cpu": "250m", "memory": "1024Mi"}

	var neighbour kubernetesPod
	neighbour.Metadata.Namespace = "other"
	neighbour.Spec.NodeName = "node-a"
	neighbour.Spec.Containers = []kubernetesContainer{{Name: "other"}}
	neighbour.Spec.Containers[0].Resources.Requests = map[string]string{"cpu": "8", "memory": "15Gi"}

	status := runtimeResourceStatusFromKubernetes(
		ResourceInput{KubernetesContext: "cluster", Tenant: "team", Environment: "dev"},
		kubernetesNodeList{Items: []kubernetesNode{node}},
		kubernetesPodList{Items: []kubernetesPod{target, neighbour}},
		nil,
	)
	if status.Schedulable.CPU.Free != 4 || status.Schedulable.Memory.Free != 8 {
		t.Fatalf("expected the environment's own size to stay selectable, got %+v", status.Schedulable)
	}
	if !status.Schedulable.CPU.Floored || !status.Schedulable.Memory.Floored {
		t.Fatalf("a figure clamped up to the environment's own size must be marked floored: %+v", status.Schedulable)
	}
	if !strings.Contains(status.Schedulable.Notice, "not spare capacity") {
		t.Fatalf("a floored scheduling figure must say what it is, got %q", status.Schedulable.Notice)
	}
	if !strings.Contains(status.Schedulable.Notice, "Stopping an environment nobody is using") {
		t.Fatalf("a floored scheduling figure must name the remedy that returns capacity, got %q", status.Schedulable.Notice)
	}
}

func TestRuntimeResourceStatusReportsNoSchedulingRoomForANewEnvironment(t *testing.T) {
	var node kubernetesNode
	node.Metadata.Name = "node-a"
	node.Status.Allocatable.CPU = "8"
	node.Status.Allocatable.Memory = "16Gi"

	var neighbour kubernetesPod
	neighbour.Metadata.Namespace = "other"
	neighbour.Spec.NodeName = "node-a"
	neighbour.Spec.Containers = []kubernetesContainer{{Name: "other"}}
	neighbour.Spec.Containers[0].Resources.Requests = map[string]string{"cpu": "8", "memory": "16Gi"}

	status := runtimeResourceStatusFromKubernetes(
		ResourceInput{KubernetesContext: "cluster"},
		kubernetesNodeList{Items: []kubernetesNode{node}},
		kubernetesPodList{Items: []kubernetesPod{neighbour}},
		nil,
	)
	if status.Schedulable.CPU.Free != 0 || status.Schedulable.Memory.Free != 0 {
		t.Fatalf("expected a request-full node to report no room, got %+v", status.Schedulable)
	}
	if status.Schedulable.CPU.Floored || status.Schedulable.Memory.Floored {
		t.Fatalf("nothing is held for an environment not on the node, so nothing may be floored: %+v", status.Schedulable)
	}
	if !strings.Contains(status.Schedulable.Notice, "nothing left on this node") {
		t.Fatalf("expected the reading to name the shortfall, got %q", status.Schedulable.Notice)
	}
}
