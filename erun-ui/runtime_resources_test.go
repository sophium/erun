package main

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

	status := runtimeResourceStatusFromKubernetes(uiRuntimeResourceInput{KubernetesContext: "cluster"}, kubernetesNodeList{Items: []kubernetesNode{nodeA, nodeB}}, kubernetesPodList{Items: []kubernetesPod{pod}}, nil)
	if !status.Available {
		t.Fatalf("expected status to be available: %+v", status)
	}
	if status.WorstCase.CPU.Free != 6 || status.WorstCase.Memory.Free != 12 {
		t.Fatalf("unexpected best worst-case free capacity: %+v", status)
	}
	// The pod declares no requests, so the scheduler has admitted nothing on the
	// node it sits on and that node's whole allocatable capacity is free to it.
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
	if status.WorstCase.CPU.Free != 4 || status.WorstCase.Memory.Free != 8 {
		t.Fatalf("expected current runtime allocation to remain selectable when node is overcommitted, got %+v", status)
	}
	// The arithmetic above is correct but renders as a hard ceiling equal to
	// what the environment already has, which reads as "this is the maximum
	// this environment supports". The reading must say what actually happened
	// and name the remedy that moves it.
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
		uiRuntimeResourceInput{KubernetesContext: "cluster", Tenant: "team", Environment: "dev"},
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
		uiRuntimeResourceInput{KubernetesContext: "cluster", Tenant: "team", Environment: "dev"},
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

// reportingNodeRuntimePod is one environment's runtime pod as the chart shapes
// it on the cluster the report came from: a runtime container and an erun-dind
// sidecar whose declared limits are the sizes the operator chose, and whose
// declared requests are the chart's fixed 250m / 1024Mi each. The gap between
// the two is the whole point -- a limit reserves nothing, so a node can be
// "fully committed" by limits and still have room to place a pod.
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

// TestRuntimeResourceStatusReportsSchedulableCapacityFromRequests is the
// reproduction of the reported defect. The node hosts five environments whose
// container limits sum past its allocatable capacity while their requests leave
// most of it free -- the normal shape of an erun cluster, since the chart sizes
// requests for scheduling and limits for the work -- and the panel reported
// "0 CPU and 0.0 GiB memory free" and refused the values the operator had
// entered. Both statements came from the limits sum, which answers a different
// question than the one the refusal claimed to answer.
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
		uiRuntimeResourceInput{KubernetesContext: "cluster"},
		kubernetesNodeList{Items: []kubernetesNode{node}},
		pods,
		nil,
	)
	if !status.Available {
		t.Fatalf("expected an available reading: %+v", status)
	}

	// Five pods x (250m + 250m) of requests against 16 allocatable cores, and
	// 5 x 2Gi against 32Gi: the scheduler can admit another pod, comfortably.
	if status.Schedulable.CPU.Free != 13.5 {
		t.Fatalf("schedulable CPU free = %v, want 13.5 (16 allocatable minus 2.5 requested)", status.Schedulable.CPU.Free)
	}
	if status.Schedulable.Memory.Free != 22 {
		t.Fatalf("schedulable memory free = %v, want 22 (32Gi allocatable minus 10Gi requested)", status.Schedulable.Memory.Free)
	}

	// The same node's worst case is genuinely zero: the limits the operator
	// chose sum past allocatable capacity. That reading stays, labelled, so the
	// capacity-planning question keeps its answer.
	if status.WorstCase.CPU.Free != 0 || status.WorstCase.Memory.Free != 0 {
		t.Fatalf("worst-case free = %v CPU / %v GiB, want the limits sum to exhaust the node",
			status.WorstCase.CPU.Free, status.WorstCase.Memory.Free)
	}

	// Each reading says which question it answers; the two headline figures
	// alone are indistinguishable.
	if !strings.Contains(status.Schedulable.Message, "scheduler can admit 13.5 CPU") {
		t.Fatalf("schedulable message does not state the scheduling figure: %q", status.Schedulable.Message)
	}
	if !strings.Contains(status.WorstCase.Message, "declared limit") {
		t.Fatalf("worst-case message does not label itself as the bursting reading: %q", status.WorstCase.Message)
	}

	assertCapacityRemedyIsNotASmallerLimit(t, status.WorstCase.Notice)
}

// assertCapacityRemedyIsNotASmallerLimit pins the one thing the report's
// operator was told to do and must not be told again. The runtime container's
// limit is sized for the cold `make check-gate` an agent runs inside it, so
// shrinking it re-creates the out-of-memory kills that destroy the run and its
// unpushed work; the levers that actually move a fully-committed node are a
// namespace quota and fewer environments on it.
func assertCapacityRemedyIsNotASmallerLimit(t *testing.T, notice string) {
	t.Helper()
	if !strings.Contains(notice, "namespace quota") {
		t.Fatalf("notice does not name the lever that moves it: %q", notice)
	}
	if strings.Contains(notice, "lower your request") {
		t.Fatalf("notice offers the harmful remedy: %q", notice)
	}
}

// TestRuntimeResourceStatusNeverReadsAnUnreadableRequestAsZero covers the
// discipline the shared request reading already enforces: a pod that declares a
// request this parser cannot size has not reserved nothing. Counting it as zero
// would report free capacity the scheduler does not have, so the reading states
// its own incompleteness instead.
func TestRuntimeResourceStatusNeverReadsAnUnreadableRequestAsZero(t *testing.T) {
	var node kubernetesNode
	node.Metadata.Name = "node-a"
	node.Status.Allocatable.CPU = "16"
	node.Status.Allocatable.Memory = "32Gi"

	pod := reportingNodeRuntimePod("team-dev", "node-a")
	pod.Spec.Containers[0].Resources.Requests = map[string]string{"cpu": "plenty", "memory": "1024Mi"}

	status := runtimeResourceStatusFromKubernetes(
		uiRuntimeResourceInput{KubernetesContext: "cluster"},
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

// TestRuntimeResourceStatusCountsTheInitPhaseInThePodRequest covers the
// admission rule the desktop consumes rather than re-derives: Kubernetes admits
// a pod on max(max over the init containers, sum over the containers), so a
// node-capacity reading that summed only the containers would report free
// capacity its own init containers have already taken.
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
		uiRuntimeResourceInput{KubernetesContext: "cluster"},
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

// TestRuntimeResourceStatusKeepsAnExistingEnvironmentResizableOnARequestFullNode
// covers the floor on the scheduling reading, which is where it matters most.
// A node whose pods' requests have consumed everything the scheduler has is a
// node that cannot admit another pod -- but the environment already on it is
// admitted, and resizing it re-creates it against a request the scheduler has
// already placed. Without that floor the schedulable figure reads zero, the
// control's maximum goes to zero, and the environment's own size becomes
// uneditable on a reading that does not govern it.
func TestRuntimeResourceStatusKeepsAnExistingEnvironmentResizableOnARequestFullNode(t *testing.T) {
	var node kubernetesNode
	node.Metadata.Name = "node-a"
	node.Status.Allocatable.CPU = "8"
	node.Status.Allocatable.Memory = "16Gi"

	// This environment's own pod, holding 4 CPU / 8Gi of limits and the chart's
	// 250m / 1024Mi of requests.
	var target kubernetesPod
	target.Metadata.Namespace = "team-dev"
	target.Spec.NodeName = "node-a"
	target.Spec.Containers = []kubernetesContainer{{Name: "erun-devops"}}
	target.Spec.Containers[0].Resources.Limits.CPU = "4"
	target.Spec.Containers[0].Resources.Limits.Memory = "8Gi"
	target.Spec.Containers[0].Resources.Requests = map[string]string{"cpu": "250m", "memory": "1024Mi"}

	// A neighbour whose requests have taken the rest of the node.
	var neighbour kubernetesPod
	neighbour.Metadata.Namespace = "other"
	neighbour.Spec.NodeName = "node-a"
	neighbour.Spec.Containers = []kubernetesContainer{{Name: "other"}}
	neighbour.Spec.Containers[0].Resources.Requests = map[string]string{"cpu": "8", "memory": "15Gi"}

	status := runtimeResourceStatusFromKubernetes(
		uiRuntimeResourceInput{KubernetesContext: "cluster", Tenant: "team", Environment: "dev"},
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

// TestRuntimeResourceStatusReportsNoSchedulingRoomForANewEnvironment is the
// other half of that floor: an environment not yet on the node holds nothing,
// so there is nothing to floor at, and a node with no request headroom says so
// rather than borrowing a size the environment does not have.
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
		uiRuntimeResourceInput{KubernetesContext: "cluster"},
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
