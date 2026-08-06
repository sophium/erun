package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"sort"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

func (a *App) LoadRuntimeResourceStatus(input uiRuntimeResourceInput) (uiRuntimeResourceStatus, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return a.deps.loadResourceStatus(ctx, normalizeRuntimeResourceInput(input))
}

// LoadClusterRegistry reports whether the given Kubernetes context has an
// in-cluster erun-registry Service, so the new-environment dialog can default to
// a resolvable cluster: registry entry (addresses derived from the context) for
// that env instead of a hardcoded host.
func (a *App) LoadClusterRegistry(input uiRuntimeResourceInput) (uiClusterRegistryStatus, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return a.deps.loadClusterRegistry(ctx, normalizeRuntimeResourceInput(input))
}

func loadClusterRegistry(ctx context.Context, input uiRuntimeResourceInput) (uiClusterRegistryStatus, error) {
	input = normalizeRuntimeResourceInput(input)
	if input.KubernetesContext == "" {
		return uiClusterRegistryStatus{}, nil
	}
	service := eruncommon.DefaultClusterRegistryService
	namespace := eruncommon.DefaultClusterRegistryNamespace
	// A ClusterIP means the registry Service exists in this context. A missing
	// Service makes kubectl exit non-zero — treated as "not deployed", not an
	// error, so the dialog simply falls back to its normal registry default.
	output, err := kubectlJSON(ctx, input.KubernetesContext,
		"get", "svc", service, "-n", namespace, "-o", "jsonpath={.spec.clusterIP}")
	clusterIP := strings.TrimSpace(string(output))
	if err != nil || clusterIP == "" || clusterIP == "None" {
		return uiClusterRegistryStatus{KubernetesContext: input.KubernetesContext, Deployed: false}, nil
	}
	port := eruncommon.DefaultClusterRegistryPort
	return uiClusterRegistryStatus{
		KubernetesContext: input.KubernetesContext,
		Deployed:          true,
		Service:           service,
		Namespace:         namespace,
		Port:              port,
		// The erun-registry is plain HTTP, so mark it insecure for the in-pod dind.
		Insecure: true,
		Message:  fmt.Sprintf("In-cluster registry %s.%s:%d", service, namespace, port),
	}, nil
}

func loadRuntimeResourceStatus(ctx context.Context, input uiRuntimeResourceInput) (uiRuntimeResourceStatus, error) {
	input = normalizeRuntimeResourceInput(input)
	if input.KubernetesContext == "" {
		return unavailableRuntimeResourceStatus("", "Choose a Kubernetes context to inspect node capacity."), nil
	}
	nodes, err := loadKubernetesNodes(ctx, input.KubernetesContext)
	if err != nil {
		return unavailableRuntimeResourceStatus(input.KubernetesContext, "Node capacity is unavailable: "+err.Error()), nil
	}
	pods, err := loadKubernetesPods(ctx, input.KubernetesContext)
	if err != nil {
		return unavailableRuntimeResourceStatus(input.KubernetesContext, "Current pod allocation is unavailable: "+err.Error()), nil
	}
	// Measured usage is a best-effort refinement, not a requirement: clusters
	// without metrics-server still get the limits-based reading, and say so.
	return runtimeResourceStatusFromKubernetes(input, nodes, pods, loadKubernetesContainerUsage(ctx, input.KubernetesContext)), nil
}

func normalizeRuntimeResourceInput(input uiRuntimeResourceInput) uiRuntimeResourceInput {
	return uiRuntimeResourceInput{
		KubernetesContext: strings.TrimSpace(input.KubernetesContext),
		Tenant:            strings.TrimSpace(input.Tenant),
		Environment:       strings.TrimSpace(input.Environment),
	}
}

func runtimeResourceStatusFromKubernetes(input uiRuntimeResourceInput, nodes kubernetesNodeList, pods kubernetesPodList, measured kubernetesContainerUsage) uiRuntimeResourceStatus {
	input = normalizeRuntimeResourceInput(input)
	target := runtimeResourceTarget(input)
	accounting := accumulateRuntimePodUsage(pods, target, measured)

	result := uiRuntimeResourceStatus{
		KubernetesContext: input.KubernetesContext,
		Available:         true,
		MeasuredUsage:     len(measured) > 0,
	}
	for _, node := range nodes.Items {
		name := strings.TrimSpace(node.Metadata.Name)
		if name == "" {
			continue
		}
		cpuTotal, _ := eruncommon.ParseKubernetesCPUToMilli(node.Status.Allocatable.CPU)
		memoryTotal, _ := eruncommon.ParseKubernetesMemoryToMi(node.Status.Allocatable.Memory)
		used := accounting.usage[name]
		targetUsed := accounting.targetUsage[name]
		item := uiRuntimeResourceNode{
			Name:   name,
			CPU:    cpuMetricWithMinimumFree(cpuTotal, used.CPUMilli, targetUsed.CPUMilli),
			Memory: memoryMetricWithMinimumFree(memoryTotal, used.MemoryMi, targetUsed.MemoryMi),
		}
		result.Nodes = append(result.Nodes, item)
		if shouldUseRuntimeResourceNode(name, accounting.targetNode, item, result) {
			result.Node = name
			result.CPU = item.CPU
			result.Memory = item.Memory
			result.UnmeasuredContainers = accounting.unaccounted[name]
		}
	}
	sort.Slice(result.Nodes, func(i, j int) bool {
		return result.Nodes[i].Name < result.Nodes[j].Name
	})
	if len(result.Nodes) == 0 {
		return unavailableRuntimeResourceStatus(input.KubernetesContext, "No Kubernetes nodes reported allocatable capacity.")
	}
	result.Floored = result.CPU.Floored || result.Memory.Floored
	result.Message = runtimeResourceMessage(result, accounting.targetNode != "")
	result.Notice = runtimeResourceNotice(result)
	return result
}

// runtimeResourceMessage states what the figure is: one reading of a node whose
// allocatable capacity and neighbouring pods both move on their own. The old
// wording ("Available for this runtime") read as a fixed product ceiling, which
// is how an operator ends up believing their environment cannot be given more
// memory than it already has.
func runtimeResourceMessage(status uiRuntimeResourceStatus, targeted bool) string {
	node := strings.TrimSpace(status.Node)
	if node == "" {
		node = "the selected node"
	}
	if targeted {
		return fmt.Sprintf("Right now on %s: %s CPU and %s memory free for this runtime.", node, status.CPU.Formatted, status.Memory.Formatted)
	}
	return fmt.Sprintf("Right now on %s (the emptiest node): %s CPU and %s memory free.", node, status.CPU.Formatted, status.Memory.Formatted)
}

// runtimeResourceNotice carries what the number alone cannot say: that the cap
// is the node being full rather than a limit on this environment, and that part
// of the node's real consumption is invisible to the reading. Both are the
// difference between an operator who is stuck and one who knows to stop an
// environment nobody is using.
func runtimeResourceNotice(status uiRuntimeResourceStatus) string {
	var notices []string
	if status.Floored {
		notices = append(notices, "The node is fully committed, so this is the amount this environment already holds, not spare capacity. "+
			"Stopping an environment nobody is using on this node returns its CPU and memory and raises this figure.")
	}
	if status.UnmeasuredContainers > 0 {
		notices = append(notices, fmt.Sprintf("%s on this node declare no limits, so the real usage is higher than shown.",
			pluralizeContainers(status.UnmeasuredContainers)))
	}
	return strings.Join(notices, " ")
}

func pluralizeContainers(count int) string {
	if count == 1 {
		return "1 container"
	}
	return fmt.Sprintf("%d containers", count)
}

// runtimeResourceAccounting is the per-node picture the status is built from:
// what every other pod holds, what this environment already holds, and how many
// containers the reading could not account for at all.
type runtimeResourceAccounting struct {
	usage       map[string]runtimeResourceTotals
	targetUsage map[string]runtimeResourceTotals
	unaccounted map[string]int
	targetNode  string
}

// accumulateRuntimePodUsage sums what the node is committed to. A container's
// limit is the best answer when it declares one; when it does not, its measured
// usage is used instead, and when there is no measurement either the container
// is counted as unaccounted rather than as zero.
//
// Treating a limitless container as zero is what made the figure wrong in
// practice: every erun-dind sidecar declares no limits, so a node's real
// consumption — Testcontainers, the buildkit cache — was entirely invisible to
// a reading that summed limits alone.
func accumulateRuntimePodUsage(pods kubernetesPodList, target runtimeResourceTargetSpec, measured kubernetesContainerUsage) runtimeResourceAccounting {
	accounting := runtimeResourceAccounting{
		usage:       make(map[string]runtimeResourceTotals),
		targetUsage: make(map[string]runtimeResourceTotals),
		unaccounted: make(map[string]int),
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
	}
	return accounting
}

// containerConsumption resolves one container's contribution and reports
// whether the answer is complete — a container with neither a limit nor a
// measurement is consuming something the reading cannot see.
func containerConsumption(pod kubernetesPod, container kubernetesContainer, measured kubernetesContainerUsage) (runtimeResourceTotals, bool) {
	var totals runtimeResourceTotals
	usage, hasUsage := measured[containerUsageKey(pod, container)]
	complete := true

	if cpu, err := eruncommon.ParseKubernetesCPUToMilli(container.Resources.Limits.CPU); err == nil {
		totals.CPUMilli = cpu
	} else if hasUsage {
		totals.CPUMilli = usage.CPUMilli
	} else {
		complete = false
	}

	if memory, err := eruncommon.ParseKubernetesMemoryToMi(container.Resources.Limits.Memory); err == nil {
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

func shouldUseRuntimeResourceNode(name, targetNode string, item uiRuntimeResourceNode, result uiRuntimeResourceStatus) bool {
	if targetNode != "" {
		return name == targetNode
	}
	if result.CPU.Unit == "" {
		return true
	}
	return item.CPU.Free*item.Memory.Free > result.CPU.Free*result.Memory.Free
}

type runtimeResourceTargetSpec struct {
	namespace string
	container string
}

func runtimeResourceTarget(input uiRuntimeResourceInput) runtimeResourceTargetSpec {
	if input.Tenant == "" || input.Environment == "" {
		return runtimeResourceTargetSpec{}
	}
	return runtimeResourceTargetSpec{
		namespace: eruncommon.KubernetesNamespaceName(input.Tenant, input.Environment),
		// The runtime container is named for the component, identical across
		// tenants; only the Deployment/release is <tenant>-devops. Match on the
		// container name, not the release name, or the capacity check misses the
		// env's own pod for every non-erun tenant.
		container: eruncommon.DevopsComponentName,
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

// cpuMetricWithMinimumFree floors free capacity at what this environment
// already holds — it can always keep what it has. Floored records when that
// floor is what produced the number, because "your maximum equals your current
// value" is otherwise indistinguishable from a product limit.
func cpuMetricWithMinimumFree(totalMilli, usedMilli, minimumFreeMilli int64) uiRuntimeResourceMetric {
	freeMilli := totalMilli - usedMilli
	if freeMilli < 0 {
		freeMilli = 0
	}
	floored := false
	if freeMilli < minimumFreeMilli {
		freeMilli = minimumFreeMilli
		floored = minimumFreeMilli > 0
	}
	return uiRuntimeResourceMetric{
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
	return eruncommon.FormatKubernetesCPUFromMilli(milli)
}

func memoryMetricWithMinimumFree(totalMi, usedMi, minimumFreeMi int64) uiRuntimeResourceMetric {
	freeMi := totalMi - usedMi
	if freeMi < 0 {
		freeMi = 0
	}
	floored := false
	if freeMi < minimumFreeMi {
		freeMi = minimumFreeMi
		floored = minimumFreeMi > 0
	}
	return uiRuntimeResourceMetric{
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

func unavailableRuntimeResourceStatus(kubernetesContext, message string) uiRuntimeResourceStatus {
	return uiRuntimeResourceStatus{
		KubernetesContext: strings.TrimSpace(kubernetesContext),
		Available:         false,
		Message:           strings.TrimSpace(message),
	}
}

func loadKubernetesNodes(ctx context.Context, kubernetesContext string) (kubernetesNodeList, error) {
	output, err := kubectlJSON(ctx, kubernetesContext, "get", "nodes", "-o", "json")
	if err != nil {
		return kubernetesNodeList{}, err
	}
	var nodes kubernetesNodeList
	if err := json.Unmarshal(output, &nodes); err != nil {
		return kubernetesNodeList{}, fmt.Errorf("parse nodes: %w", err)
	}
	return nodes, nil
}

// kubernetesContainerUsage is measured per-container consumption, keyed by
// namespace/pod/container. Empty when the cluster has no metrics source.
type kubernetesContainerUsage map[string]runtimeResourceTotals

func containerUsageKey(pod kubernetesPod, container kubernetesContainer) string {
	return strings.TrimSpace(pod.Metadata.Namespace) + "/" + strings.TrimSpace(pod.Metadata.Name) + "/" + strings.TrimSpace(container.Name)
}

// loadKubernetesContainerUsage reads what containers are actually consuming, so
// a container that declares no limits is counted at its real usage instead of
// being treated as free. Deliberately fail-soft: a cluster without
// metrics-server still gets the limits-based reading, flagged as incomplete
// rather than presented as exact.
func loadKubernetesContainerUsage(ctx context.Context, kubernetesContext string) kubernetesContainerUsage {
	output, err := kubectlJSON(ctx, kubernetesContext, "top", "pod", "--all-namespaces", "--containers", "--no-headers")
	if err != nil {
		return nil
	}
	return parseKubernetesContainerUsage(string(output))
}

// parseKubernetesContainerUsage reads `kubectl top pod --containers` rows:
// NAMESPACE POD CONTAINER CPU MEMORY.
func parseKubernetesContainerUsage(output string) kubernetesContainerUsage {
	usage := kubernetesContainerUsage{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 5 {
			continue
		}
		cpu, cpuErr := eruncommon.ParseKubernetesCPUToMilli(fields[3])
		memory, memoryErr := eruncommon.ParseKubernetesMemoryToMi(fields[4])
		if cpuErr != nil && memoryErr != nil {
			continue
		}
		totals := runtimeResourceTotals{}
		if cpuErr == nil {
			totals.CPUMilli = cpu
		}
		if memoryErr == nil {
			totals.MemoryMi = memory
		}
		usage[fields[0]+"/"+fields[1]+"/"+fields[2]] = totals
	}
	if len(usage) == 0 {
		return nil
	}
	return usage
}

func loadKubernetesPods(ctx context.Context, kubernetesContext string) (kubernetesPodList, error) {
	output, err := kubectlJSON(ctx, kubernetesContext, "get", "pods", "--all-namespaces", "-o", "json")
	if err != nil {
		return kubernetesPodList{}, err
	}
	var pods kubernetesPodList
	if err := json.Unmarshal(output, &pods); err != nil {
		return kubernetesPodList{}, fmt.Errorf("parse pods: %w", err)
	}
	return pods, nil
}

func kubectlJSON(ctx context.Context, kubernetesContext string, args ...string) ([]byte, error) {
	kubernetesContext = strings.TrimSpace(kubernetesContext)
	if kubernetesContext != "" {
		args = append([]string{"--context", kubernetesContext}, args...)
	}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	eruncommon.HideConsoleWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail != "" {
			return nil, fmt.Errorf("%w: %s", err, detail)
		}
		return nil, err
	}
	return output, nil
}

type kubernetesNodeList struct {
	Items []kubernetesNode `json:"items"`
}

type kubernetesNode struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Status struct {
		Allocatable struct {
			CPU    string `json:"cpu"`
			Memory string `json:"memory"`
		} `json:"allocatable"`
	} `json:"status"`
}

type kubernetesPodList struct {
	Items []kubernetesPod `json:"items"`
}

type kubernetesPod struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Status struct {
		Phase string `json:"phase"`
	} `json:"status"`
	Spec struct {
		NodeName   string                `json:"nodeName"`
		Containers []kubernetesContainer `json:"containers"`
	} `json:"spec"`
}

type kubernetesContainer struct {
	Name      string `json:"name"`
	Resources struct {
		Limits struct {
			CPU    string `json:"cpu"`
			Memory string `json:"memory"`
		} `json:"limits"`
	} `json:"resources"`
}
