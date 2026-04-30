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
	return runtimeResourceStatusFromKubernetes(input, nodes, pods), nil
}

func normalizeRuntimeResourceInput(input uiRuntimeResourceInput) uiRuntimeResourceInput {
	return uiRuntimeResourceInput{
		KubernetesContext: strings.TrimSpace(input.KubernetesContext),
		Tenant:            strings.TrimSpace(input.Tenant),
		Environment:       strings.TrimSpace(input.Environment),
	}
}

func runtimeResourceStatusFromKubernetes(input uiRuntimeResourceInput, nodes kubernetesNodeList, pods kubernetesPodList) uiRuntimeResourceStatus {
	input = normalizeRuntimeResourceInput(input)
	target := runtimeResourceTarget(input)
	usage := make(map[string]runtimeResourceTotals)
	targetUsage := make(map[string]runtimeResourceTotals)
	targetNode := ""
	for _, pod := range pods.Items {
		nodeName := strings.TrimSpace(pod.Spec.NodeName)
		if nodeName == "" {
			continue
		}
		totals := usage[nodeName]
		for _, container := range pod.Spec.Containers {
			if target.matches(pod, container) {
				targetNode = nodeName
				targetTotals := targetUsage[nodeName]
				if cpu, err := eruncommon.ParseKubernetesCPUToMilli(container.Resources.Limits.CPU); err == nil {
					targetTotals.CPUMilli += cpu
				}
				if memory, err := eruncommon.ParseKubernetesMemoryToMi(container.Resources.Limits.Memory); err == nil {
					targetTotals.MemoryMi += memory
				}
				targetUsage[nodeName] = targetTotals
				continue
			}
			if cpu, err := eruncommon.ParseKubernetesCPUToMilli(container.Resources.Limits.CPU); err == nil {
				totals.CPUMilli += cpu
			}
			if memory, err := eruncommon.ParseKubernetesMemoryToMi(container.Resources.Limits.Memory); err == nil {
				totals.MemoryMi += memory
			}
		}
		usage[nodeName] = totals
	}

	result := uiRuntimeResourceStatus{
		KubernetesContext: input.KubernetesContext,
		Available:         true,
	}
	for _, node := range nodes.Items {
		name := strings.TrimSpace(node.Metadata.Name)
		if name == "" {
			continue
		}
		cpuTotal, _ := eruncommon.ParseKubernetesCPUToMilli(node.Status.Allocatable.CPU)
		memoryTotal, _ := eruncommon.ParseKubernetesMemoryToMi(node.Status.Allocatable.Memory)
		used := usage[name]
		targetUsed := targetUsage[name]
		item := uiRuntimeResourceNode{
			Name:   name,
			CPU:    cpuMetricWithMinimumFree(cpuTotal, used.CPUMilli, targetUsed.CPUMilli),
			Memory: memoryMetricWithMinimumFree(memoryTotal, used.MemoryMi, targetUsed.MemoryMi),
		}
		result.Nodes = append(result.Nodes, item)
		if shouldUseRuntimeResourceNode(name, targetNode, item, result) {
			result.CPU = item.CPU
			result.Memory = item.Memory
		}
	}
	sort.Slice(result.Nodes, func(i, j int) bool {
		return result.Nodes[i].Name < result.Nodes[j].Name
	})
	if len(result.Nodes) == 0 {
		return unavailableRuntimeResourceStatus(input.KubernetesContext, "No Kubernetes nodes reported allocatable capacity.")
	}
	if targetNode != "" {
		result.Message = fmt.Sprintf("Available for this runtime: %s CPU and %s memory.", result.CPU.Formatted, result.Memory.Formatted)
		return result
	}
	result.Message = fmt.Sprintf("Available on best node: %s CPU and %s memory.", result.CPU.Formatted, result.Memory.Formatted)
	return result
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
		container: eruncommon.RuntimeReleaseName(input.Tenant),
	}
}

func (t runtimeResourceTargetSpec) matches(pod kubernetesPod, container kubernetesContainer) bool {
	return t.namespace != "" &&
		t.container != "" &&
		strings.TrimSpace(pod.Metadata.Namespace) == t.namespace &&
		strings.TrimSpace(container.Name) == t.container
}

type runtimeResourceTotals struct {
	CPUMilli int64
	MemoryMi int64
}

func cpuMetric(totalMilli, usedMilli int64) uiRuntimeResourceMetric {
	return cpuMetricWithMinimumFree(totalMilli, usedMilli, 0)
}

func cpuMetricWithMinimumFree(totalMilli, usedMilli, minimumFreeMilli int64) uiRuntimeResourceMetric {
	freeMilli := totalMilli - usedMilli
	if freeMilli < 0 {
		freeMilli = 0
	}
	if freeMilli < minimumFreeMilli {
		freeMilli = minimumFreeMilli
	}
	return uiRuntimeResourceMetric{
		Total:     round1(float64(totalMilli) / 1000),
		Used:      round1(float64(usedMilli) / 1000),
		Free:      round1(float64(freeMilli) / 1000),
		Unit:      "cores",
		Formatted: formatRuntimeResourceCPU(freeMilli),
	}
}

func formatRuntimeResourceCPU(milli int64) string {
	if milli <= 0 {
		return "0"
	}
	return eruncommon.FormatKubernetesCPUFromMilli(milli)
}

func memoryMetric(totalMi, usedMi int64) uiRuntimeResourceMetric {
	return memoryMetricWithMinimumFree(totalMi, usedMi, 0)
}

func memoryMetricWithMinimumFree(totalMi, usedMi, minimumFreeMi int64) uiRuntimeResourceMetric {
	freeMi := totalMi - usedMi
	if freeMi < 0 {
		freeMi = 0
	}
	if freeMi < minimumFreeMi {
		freeMi = minimumFreeMi
	}
	return uiRuntimeResourceMetric{
		Total:     round1(float64(totalMi) / 1024),
		Used:      round1(float64(usedMi) / 1024),
		Free:      round1(float64(freeMi) / 1024),
		Unit:      "GiB",
		Formatted: fmt.Sprintf("%.1f GiB", round1(float64(freeMi)/1024)),
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
	output, err := exec.CommandContext(ctx, "kubectl", args...).CombinedOutput()
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
		Namespace string `json:"namespace"`
	} `json:"metadata"`
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
