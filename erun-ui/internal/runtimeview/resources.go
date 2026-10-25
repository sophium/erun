package runtimeview

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/podruntime"
	"github.com/sophium/erun/erun-common/command/setup"
)

func (s *Service) LoadRuntimeResourceStatus(input ResourceInput) (ResourceStatus, error) {
	ctx := s.d.Context()
	return s.d.LoadResourceStatus(ctx, normalizeRuntimeResourceInput(input))
}

// LoadClusterRegistry reports whether the context has an in-cluster erun-registry Service.
func (s *Service) LoadClusterRegistry(input ResourceInput) (ClusterRegistryStatus, error) {
	ctx := s.d.Context()
	return s.d.LoadClusterRegistry(ctx, normalizeRuntimeResourceInput(input))
}

// LoadHostedRegistry reports whether erun's hosted container registry is reachable.
func (s *Service) LoadHostedRegistry() (HostedRegistryStatus, error) {
	ctx := s.d.Context()
	return s.d.LoadHostedRegistry(ctx), nil
}

// ProbeHostedRegistry is the production LoadHostedRegistry seam.
func ProbeHostedRegistry(ctx context.Context) HostedRegistryStatus {
	status := setup.ProbeHostedRegistry(ctx, nil)
	return HostedRegistryStatus{
		Host:      status.Host,
		Available: status.Available,
		Reason:    status.Reason,
		Recovery:  status.Recovery,
	}
}

// ReadClusterRegistry is the production LoadClusterRegistry seam.
func ReadClusterRegistry(ctx context.Context, input ResourceInput) (ClusterRegistryStatus, error) {
	input = normalizeRuntimeResourceInput(input)
	if input.KubernetesContext == "" {
		return ClusterRegistryStatus{}, nil
	}
	service := config.DefaultClusterRegistryService
	namespace := config.DefaultClusterRegistryNamespace
	// A missing Service makes kubectl exit non-zero; that means "not deployed", not an error.
	if !clusterRegistryServiceDeployed(ctx, input.KubernetesContext, service, namespace) {
		return ClusterRegistryStatus{KubernetesContext: input.KubernetesContext, Deployed: false}, nil
	}
	port := config.DefaultClusterRegistryPort
	return ClusterRegistryStatus{
		KubernetesContext: input.KubernetesContext,
		Deployed:          true,
		Service:           service,
		Namespace:         namespace,
		Port:              port,
		// The erun-registry is plain HTTP.
		Insecure: true,
		Message:  fmt.Sprintf("In-cluster registry %s.%s:%d", service, namespace, port),
	}, nil
}

func clusterRegistryServiceDeployed(ctx context.Context, kubernetesContext, service, namespace string) bool {
	output, err := kubectlJSON(ctx, kubernetesContext,
		"get", "svc", service, "-n", namespace, "-o", "jsonpath={.spec.clusterIP}")
	clusterIP := strings.TrimSpace(string(output))
	return err == nil && clusterIP != "" && clusterIP != "None"
}

// ReadResourceStatus is the production LoadResourceStatus seam.
func ReadResourceStatus(ctx context.Context, input ResourceInput) (ResourceStatus, error) {
	input = normalizeRuntimeResourceInput(input)
	if input.KubernetesContext == "" {
		return unavailableRuntimeResourceStatus("", "Choose a Kubernetes context to inspect node capacity."), nil
	}
	nodes, err := loadKubernetesNodes(ctx, input.KubernetesContext)
	if err != nil {
		return unavailableRuntimeResourceStatus(input.KubernetesContext, fmt.Sprintf("Node capacity is unavailable: %v", err)), nil
	}
	pods, err := loadKubernetesPods(ctx, input.KubernetesContext)
	if err != nil {
		return unavailableRuntimeResourceStatus(input.KubernetesContext, fmt.Sprintf("Current pod allocation is unavailable: %v", err)), nil
	}
	// Measured usage is best-effort: without metrics-server the limits-based reading stands.
	return runtimeResourceStatusFromKubernetes(input, nodes, pods, loadKubernetesContainerUsage(ctx, input.KubernetesContext)), nil
}

func normalizeRuntimeResourceInput(input ResourceInput) ResourceInput {
	return ResourceInput{
		KubernetesContext: strings.TrimSpace(input.KubernetesContext),
		Tenant:            strings.TrimSpace(input.Tenant),
		Environment:       strings.TrimSpace(input.Environment),
	}
}

func runtimeResourceStatusFromKubernetes(input ResourceInput, nodes kubernetesNodeList, pods kubernetesPodList, measured kubernetesContainerUsage) ResourceStatus {
	input = normalizeRuntimeResourceInput(input)
	target := runtimeResourceTarget(input)
	accounting := accumulateRuntimePodUsage(pods, target, measured)

	result := ResourceStatus{
		KubernetesContext: input.KubernetesContext,
		Available:         true,
		MeasuredUsage:     len(measured) > 0,
	}
	for _, node := range nodes.Items {
		name := strings.TrimSpace(node.Metadata.Name)
		if name == "" {
			continue
		}
		cpuTotal, _ := config.ParseKubernetesCPUToMilli(node.Status.Allocatable.CPU)
		memoryTotal, _ := config.ParseKubernetesMemoryToMi(node.Status.Allocatable.Memory)
		bursting := accounting.usage[name]
		requests := accounting.requests[name]
		targetHeld := accounting.targetUsage[name]
		item := ResourceNode{
			Name: name,
			// Allocatable minus requests: what a deploy's outcome follows. Floored at what an
			// environment already on the node holds, since its pod is already admitted.
			Schedulable: ResourceReading{
				CPU:    cpuMetricWithMinimumFree(cpuTotal, requests.CPUMilli, targetHeld.CPUMilli),
				Memory: memoryMetricWithMinimumFree(memoryTotal, requests.MemoryMi, targetHeld.MemoryMi),
			},
			SchedulableComplete: accounting.unreadable[name] == 0,
			// Every container at its limit at once, floored at what this environment holds.
			WorstCase: ResourceReading{
				CPU:    cpuMetricWithMinimumFree(cpuTotal, bursting.CPUMilli, targetHeld.CPUMilli),
				Memory: memoryMetricWithMinimumFree(memoryTotal, bursting.MemoryMi, targetHeld.MemoryMi),
			},
		}
		result.Nodes = append(result.Nodes, item)
		if shouldUseRuntimeResourceNode(name, accounting.targetNode, item, result) {
			result.Node = name
			result.Schedulable = item.Schedulable
			result.SchedulableComplete = item.SchedulableComplete
			result.WorstCase = item.WorstCase
			result.UnmeasuredContainers = accounting.unaccounted[name]
			result.UnreadableRequests = accounting.unreadable[name]
		}
	}
	sort.Slice(result.Nodes, func(i, j int) bool {
		return result.Nodes[i].Name < result.Nodes[j].Name
	})
	if len(result.Nodes) == 0 {
		return unavailableRuntimeResourceStatus(input.KubernetesContext, "No Kubernetes nodes reported allocatable capacity.")
	}
	result.Floored = result.WorstCase.CPU.Floored || result.WorstCase.Memory.Floored
	result.Schedulable.Message = runtimeSchedulableMessage(result, accounting.targetNode != "")
	result.Schedulable.Notice = runtimeSchedulableNotice(result)
	result.WorstCase.Message = runtimeWorstCaseMessage(result)
	result.WorstCase.Notice = runtimeWorstCaseNotice(result)
	return result
}

func runtimeResourceNodeLabel(status ResourceStatus) string {
	if node := strings.TrimSpace(status.Node); node != "" {
		return node
	}
	return "the selected node"
}

// The scheduler admits on requests; the dialog's CPU/memory are limits, which reserve nothing.
func runtimeSchedulableMessage(status ResourceStatus, targeted bool) string {
	node := runtimeResourceNodeLabel(status)
	if !status.SchedulableComplete {
		return fmt.Sprintf("Right now on %s: at most %s CPU and %s memory free for the scheduler to admit a pod with -- an upper bound, "+
			"because a pod on this node declares a request this reading could not read and so holds back more of the node than is shown here.",
			node, status.Schedulable.CPU.Formatted, status.Schedulable.Memory.Formatted)
	}
	if targeted {
		return fmt.Sprintf("Right now on %s: the scheduler can admit %s CPU and %s memory more for this environment. "+
			"It places by what each pod requests, which is what a deploy depends on; the limits set here reserve none of it.",
			node, status.Schedulable.CPU.Formatted, status.Schedulable.Memory.Formatted)
	}
	return fmt.Sprintf("Right now on %s (the emptiest node): the scheduler can admit %s CPU and %s memory more. "+
		"It places by what each pod requests, which is what a deploy depends on -- not by limits, which reserve none of it.",
		node, status.Schedulable.CPU.Formatted, status.Schedulable.Memory.Formatted)
}

func runtimeWorstCaseMessage(status ResourceStatus) string {
	return fmt.Sprintf("Worst case, with every container on %s at its declared limit at once: %s CPU and %s memory left. "+
		"That is oversubscription headroom, not scheduling capacity.",
		runtimeResourceNodeLabel(status), status.WorstCase.CPU.Formatted, status.WorstCase.Memory.Formatted)
}

func runtimeSchedulableNotice(status ResourceStatus) string {
	if !status.SchedulableComplete && status.UnreadableRequests > 0 {
		return fmt.Sprintf("%s on this node declares a request this reading could not read, so the free figure above is an upper bound.",
			pluralizePods(status.UnreadableRequests))
	}
	floored := status.Schedulable.CPU.Floored || status.Schedulable.Memory.Floored
	exhausted := status.Schedulable.CPU.Free <= 0 || status.Schedulable.Memory.Free <= 0
	if !floored && !exhausted {
		return ""
	}
	notice := "The scheduler has nothing left on this node to admit another pod with."
	if floored {
		// A floored figure would otherwise read as a product ceiling.
		notice += " What is shown is the size this environment already holds, not spare capacity."
	}
	return notice + " Stopping an environment nobody is using on it returns its reservation."
}

// Never suggests a smaller limit: the limit is sized for the agent's cold full
// build, and lowering it brings back OOM kills.
func runtimeWorstCaseNotice(status ResourceStatus) string {
	var notices []string
	if status.Floored {
		notices = append(notices, "The node is fully committed at those limits, so this figure is what this environment already holds rather than spare capacity.")
	}
	if status.UnmeasuredContainers > 0 {
		notices = append(notices, fmt.Sprintf("%s on this node declare no limits, so the real usage is higher than shown.",
			pluralizeContainers(status.UnmeasuredContainers)))
	}
	if status.Floored || status.WorstCase.CPU.Free <= 0 || status.WorstCase.Memory.Free <= 0 {
		notices = append(notices, "Oversubscription headroom is managed with a namespace quota or by running fewer environments on this node. "+
			"Shrinking a limit sized for the work is not the move: it re-creates the out-of-memory kills that destroy an in-pod agent run and its unpushed work.")
	}
	return strings.Join(notices, " ")
}

func pluralizePods(count int) string {
	if count == 1 {
		return "1 pod"
	}
	return fmt.Sprintf("%d pods", count)
}

func pluralizeContainers(count int) string {
	if count == 1 {
		return "1 container"
	}
	return fmt.Sprintf("%d containers", count)
}

type runtimeResourceAccounting struct {
	usage map[string]runtimeResourceTotals
	// requests come from pod specs: a limit reserves nothing and no cgroup carries a request.
	requests map[string]runtimeResourceTotals
	// targetUsage floors the worst-case reading.
	targetUsage map[string]runtimeResourceTotals
	// Unaccounted and unreadable entries are never counted as zero.
	unaccounted map[string]int
	unreadable  map[string]int
	targetNode  string
}

// A limitless container counts at measured usage, else as unaccounted, never as
// zero: every erun-dind sidecar declares no limits. Requests use erun-common's
// EffectiveKubernetesPodRequests so the admission rule has one spelling.
func accumulateRuntimePodUsage(pods kubernetesPodList, target runtimeResourceTargetSpec, measured kubernetesContainerUsage) runtimeResourceAccounting {
	accounting := runtimeResourceAccounting{
		usage:       make(map[string]runtimeResourceTotals),
		requests:    make(map[string]runtimeResourceTotals),
		targetUsage: make(map[string]runtimeResourceTotals),
		unaccounted: make(map[string]int),
		unreadable:  make(map[string]int),
	}
	for _, pod := range pods.Items {
		if isTerminalKubernetesPodPhase(pod.Status.Phase) {
			continue
		}
		nodeName := strings.TrimSpace(pod.Spec.NodeName)
		if nodeName == "" {
			continue
		}
		totals := accounting.usage[nodeName]
		for _, container := range pod.Spec.Containers {
			consumed, complete := containerConsumption(pod, container, measured)
			if !complete {
				accounting.unaccounted[nodeName]++
			}
			if target.matches(pod, container) {
				accounting.targetNode = nodeName
				accounting.targetUsage[nodeName] = addTotals(accounting.targetUsage[nodeName], consumed)
				continue
			}
			totals = addTotals(totals, consumed)
		}
		accounting.usage[nodeName] = totals

		podRequests, err := podruntime.EffectiveKubernetesPodRequests(
			kubernetesContainerResources(pod.Spec.Containers),
			kubernetesContainerResources(pod.Spec.InitContainers),
		)
		if err != nil {
			// An unreadable pod makes the free figure an upper bound.
			accounting.unreadable[nodeName]++
			continue
		}
		accounting.requests[nodeName] = addTotals(accounting.requests[nodeName], runtimeResourceTotals{
			CPUMilli: podRequests.CPUMilli,
			MemoryMi: podRequests.MemoryBytes / (1 << 20),
		})
	}
	return accounting
}

func kubernetesContainerResources(containers []kubernetesContainer) []podruntime.KubernetesContainerResources {
	out := make([]podruntime.KubernetesContainerResources, 0, len(containers))
	for _, container := range containers {
		out = append(out, podruntime.KubernetesContainerResources{
			Name:     container.Name,
			Requests: container.Resources.Requests,
		})
	}
	return out
}

func containerConsumption(pod kubernetesPod, container kubernetesContainer, measured kubernetesContainerUsage) (runtimeResourceTotals, bool) {
	var totals runtimeResourceTotals
	usage, hasUsage := measured[containerUsageKey(pod, container)]
	complete := true

	if cpu, err := config.ParseKubernetesCPUToMilli(container.Resources.Limits.CPU); err == nil {
		totals.CPUMilli = cpu
	} else if hasUsage {
		totals.CPUMilli = usage.CPUMilli
	} else {
		complete = false
	}

	if memory, err := config.ParseKubernetesMemoryToMi(container.Resources.Limits.Memory); err == nil {
		totals.MemoryMi = memory
	} else if hasUsage {
		totals.MemoryMi = usage.MemoryMi
	} else {
		complete = false
	}
	return totals, complete
}

func addTotals(totals, add runtimeResourceTotals) runtimeResourceTotals {
	totals.CPUMilli += add.CPUMilli
	totals.MemoryMi += add.MemoryMi
	return totals
}

// The scheduler places by requests, so the schedulable reading picks the node.
func shouldUseRuntimeResourceNode(name, targetNode string, item ResourceNode, result ResourceStatus) bool {
	if targetNode != "" {
		return name == targetNode
	}
	if result.Schedulable.CPU.Unit == "" {
		return true
	}
	return item.Schedulable.CPU.Free*item.Schedulable.Memory.Free >
		result.Schedulable.CPU.Free*result.Schedulable.Memory.Free
}

type runtimeResourceTargetSpec struct {
	namespace string
	container string
}

func runtimeResourceTarget(input ResourceInput) runtimeResourceTargetSpec {
	if input.Tenant == "" || input.Environment == "" {
		return runtimeResourceTargetSpec{}
	}
	return runtimeResourceTargetSpec{
		namespace: config.KubernetesNamespaceName(input.Tenant, input.Environment),
		// The runtime container is named for the component in every tenant; the release is
		// <tenant>-devops, so match on the container name.
		container: config.DevopsComponentName,
	}
}

func (t runtimeResourceTargetSpec) matches(pod kubernetesPod, container kubernetesContainer) bool {
	return t.namespace != "" &&
		t.container != "" &&
		strings.TrimSpace(pod.Metadata.Namespace) == t.namespace &&
		strings.TrimSpace(container.Name) == t.container
}

func isTerminalKubernetesPodPhase(phase string) bool {
	switch strings.ToLower(strings.TrimSpace(phase)) {
	case "succeeded", "failed":
		return true
	default:
		return false
	}
}

type runtimeResourceTotals struct {
	CPUMilli int64
	MemoryMi int64
}

// Floored records when the floor produced the number, so it isn't read as a product limit.
func cpuMetricWithMinimumFree(totalMilli, usedMilli, minimumFreeMilli int64) ResourceMetric {
	freeMilli := totalMilli - usedMilli
	if freeMilli < 0 {
		freeMilli = 0
	}
	floored := false
	if freeMilli < minimumFreeMilli {
		freeMilli = minimumFreeMilli
		floored = minimumFreeMilli > 0
	}
	return ResourceMetric{
		Total:     round1(float64(totalMilli) / 1000),
		Used:      round1(float64(usedMilli) / 1000),
		Free:      round1(float64(freeMilli) / 1000),
		Unit:      "cores",
		Formatted: formatRuntimeResourceCPU(freeMilli),
		Floored:   floored,
	}
}

func formatRuntimeResourceCPU(milli int64) string {
	if milli <= 0 {
		return "0"
	}
	return config.FormatKubernetesCPUFromMilli(milli)
}

func memoryMetricWithMinimumFree(totalMi, usedMi, minimumFreeMi int64) ResourceMetric {
	freeMi := totalMi - usedMi
	if freeMi < 0 {
		freeMi = 0
	}
	floored := false
	if freeMi < minimumFreeMi {
		freeMi = minimumFreeMi
		floored = minimumFreeMi > 0
	}
	return ResourceMetric{
		Total:     round1(float64(totalMi) / 1024),
		Used:      round1(float64(usedMi) / 1024),
		Free:      round1(float64(freeMi) / 1024),
		Unit:      "GiB",
		Formatted: fmt.Sprintf("%.1f GiB", round1(float64(freeMi)/1024)),
		Floored:   floored,
	}
}

func round1(value float64) float64 {
	return math.Round(value*10) / 10
}

func unavailableRuntimeResourceStatus(kubernetesContext, message string) ResourceStatus {
	return ResourceStatus{
		KubernetesContext: strings.TrimSpace(kubernetesContext),
		Available:         false,
		Message:           strings.TrimSpace(message),
	}
}
