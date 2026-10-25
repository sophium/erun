package runtimeview

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

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

// kubernetesContainerUsage is empty when the cluster has no metrics source.
type kubernetesContainerUsage map[string]runtimeResourceTotals

func containerUsageKey(pod kubernetesPod, container kubernetesContainer) string {
	return strings.TrimSpace(pod.Metadata.Namespace) + "/" + strings.TrimSpace(pod.Metadata.Name) + "/" + strings.TrimSpace(container.Name)
}

func loadKubernetesContainerUsage(ctx context.Context, kubernetesContext string) kubernetesContainerUsage {
	output, err := kubectlJSON(ctx, kubernetesContext, "top", "pod", "--all-namespaces", "--containers", "--no-headers")
	if err != nil {
		return nil
	}
	return parseKubernetesContainerUsage(string(output))
}

// Parses `kubectl top pod --containers` rows: NAMESPACE POD CONTAINER CPU MEMORY.
func parseKubernetesContainerUsage(output string) kubernetesContainerUsage {
	usage := kubernetesContainerUsage{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 5 {
			continue
		}
		cpu, cpuErr := config.ParseKubernetesCPUToMilli(fields[3])
		memory, memoryErr := config.ParseKubernetesMemoryToMi(fields[4])
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
	core.HideConsoleWindow(cmd)
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
		// Admission takes max(init containers, sum of containers), so init containers count.
		InitContainers []kubernetesContainer `json:"initContainers"`
	} `json:"spec"`
}

type kubernetesContainer struct {
	Name      string `json:"name"`
	Resources struct {
		Limits   kubernetesResourceQuantity `json:"limits"`
		Requests map[string]string          `json:"requests"`
	} `json:"resources"`
}

type kubernetesResourceQuantity struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}
