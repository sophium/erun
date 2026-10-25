package podruntime

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/deploy/observe"
)

// RuntimePodRequestsRunnerFunc lets a transport inject a runner with its own deadline.
type RuntimePodRequestsRunnerFunc func(args []string) ([]byte, error)

// RuntimePodRequestsArgs is exported so an injected runner runs the identical command.
func RuntimePodRequestsArgs(req config.ShellLaunchParams) []string {
	args := config.KubectlTargetArgs(req)
	args = append(args, "get", "pods", "-l", "app="+config.RuntimeReleaseName(req.Tenant), "-o", "json")
	return args
}

// readRuntimeUsageRequests never fails its caller: losing requests must not cost the cgroup reading.
func readRuntimeUsageRequests(ctx core.Context, runner RuntimePodRequestsRunnerFunc, req config.ShellLaunchParams) *model.RuntimeUsageRequests {
	ctx.TraceCommand("", "kubectl", RuntimePodRequestsArgs(req)...)
	if ctx.DryRun {
		// A zero reading would claim a reservation of nothing.
		return nil
	}
	if runner == nil {
		runner = runPodRequestsKubectl
	}
	raw, err := runner(RuntimePodRequestsArgs(req))
	if err != nil {
		return &model.RuntimeUsageRequests{Unavailable: err.Error()}
	}
	requests, err := parseRuntimePodRequests(raw)
	if err != nil {
		return &model.RuntimeUsageRequests{Unavailable: err.Error()}
	}
	return requests
}

func runPodRequestsKubectl(args []string) ([]byte, error) {
	raw, stderr, err := observe.RunObserveKubectl(args)
	if err != nil {
		return nil, fmt.Errorf("kubectl get pods: %w", observe.KubectlErrorMessage(err, stderr))
	}
	return raw, nil
}

func parseRuntimePodRequests(raw []byte) (*model.RuntimeUsageRequests, error) {
	if strings.TrimSpace(string(raw)) == "" {
		return nil, fmt.Errorf("kubectl returned no pod list for this environment's release")
	}
	list, ok := observe.ParsePodStatusList(raw)
	if !ok {
		return nil, fmt.Errorf("unrecognized kubectl output")
	}
	pod, ok := selectRuntimePodForRequests(list.Items)
	if !ok {
		return nil, fmt.Errorf("no runtime pod found for this environment's release")
	}
	containers, err := declaredContainerRequests(pod)
	if err != nil {
		return nil, err
	}
	podRequests, err := effectivePodRequests(pod)
	if err != nil {
		return nil, err
	}
	return &model.RuntimeUsageRequests{Containers: containers, Pod: podRequests}, nil
}

// During a rollout the Running pod wins; ties sort by name for a deterministic reading.
func selectRuntimePodForRequests(items []observe.PodStatusItem) (observe.PodStatusItem, bool) {
	running := make([]observe.PodStatusItem, 0, len(items))
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Status.Phase), "Running") {
			running = append(running, item)
		}
	}
	preferred := running
	if len(preferred) == 0 {
		preferred = items
	}
	if len(preferred) == 0 {
		return observe.PodStatusItem{}, false
	}
	selected := preferred[0]
	for _, item := range preferred[1:] {
		if item.Metadata.Name < selected.Metadata.Name {
			selected = item
		}
	}
	return selected, true
}

// An unparseable quantity is an error, not a zero: understating the reservation is the
// one wrong answer that matters.
func declaredContainerRequests(pod observe.PodStatusItem) (map[string]model.KubernetesRequests, error) {
	var containers map[string]model.KubernetesRequests
	for _, container := range pod.Spec.Containers {
		requests, err := kubernetesRequestsFromValues(container.Resources.Requests)
		if err != nil {
			return nil, fmt.Errorf("container %s: %w", container.Name, err)
		}
		if containers == nil {
			containers = make(map[string]model.KubernetesRequests, len(pod.Spec.Containers))
		}
		containers[container.Name] = requests
	}
	return containers, nil
}

func effectivePodRequests(pod observe.PodStatusItem) (model.KubernetesRequests, error) {
	return EffectiveKubernetesPodRequests(
		kubernetesContainerResources(pod.Spec.Containers),
		kubernetesContainerResources(pod.Spec.InitContainers),
	)
}

// KubernetesContainerResources is exported so callers apply the same admission rule
// instead of re-deriving it.
type KubernetesContainerResources struct {
	Name     string
	Requests map[string]string
}

func kubernetesContainerResources(containers []observe.SpecContainerEntry) []KubernetesContainerResources {
	out := make([]KubernetesContainerResources, 0, len(containers))
	for _, container := range containers {
		out = append(out, KubernetesContainerResources{
			Name:     container.Name,
			Requests: container.Resources.Requests,
		})
	}
	return out
}

// EffectiveKubernetesPodRequests is max(max over init containers, sum over containers),
// per resource, matching the scheduler.
func EffectiveKubernetesPodRequests(containers, initContainers []KubernetesContainerResources) (model.KubernetesRequests, error) {
	sum, err := sumContainerRequests(containers)
	if err != nil {
		return model.KubernetesRequests{}, err
	}
	initPeak, err := maxContainerRequests(initContainers)
	if err != nil {
		return model.KubernetesRequests{}, err
	}
	return kubectlRequestsMax(sum, initPeak), nil
}

func sumContainerRequests(containers []KubernetesContainerResources) (model.KubernetesRequests, error) {
	total := model.KubernetesRequests{}
	for _, container := range containers {
		requests, err := kubernetesRequestsFromValues(container.Requests)
		if err != nil {
			return model.KubernetesRequests{}, fmt.Errorf("container %s: %w", container.Name, err)
		}
		total.CPUMilli += requests.CPUMilli
		total.MemoryBytes += requests.MemoryBytes
	}
	return total, nil
}

func maxContainerRequests(containers []KubernetesContainerResources) (model.KubernetesRequests, error) {
	peak := model.KubernetesRequests{}
	for _, container := range containers {
		requests, err := kubernetesRequestsFromValues(container.Requests)
		if err != nil {
			return model.KubernetesRequests{}, fmt.Errorf("init container %s: %w", container.Name, err)
		}
		peak = kubectlRequestsMax(peak, requests)
	}
	return peak, nil
}

func kubectlRequestsMax(a, b model.KubernetesRequests) model.KubernetesRequests {
	return model.KubernetesRequests{
		CPUMilli:    max(a.CPUMilli, b.CPUMilli),
		MemoryBytes: max(a.MemoryBytes, b.MemoryBytes),
	}
}

func kubernetesRequestsFromValues(values map[string]string) (model.KubernetesRequests, error) {
	requests := model.KubernetesRequests{}
	if raw := strings.TrimSpace(values["cpu"]); raw != "" {
		milli, err := config.ParseKubernetesCPUToMilli(raw)
		if err != nil {
			return model.KubernetesRequests{}, fmt.Errorf("cpu request %q: %w", raw, err)
		}
		requests.CPUMilli = milli
	}
	if raw := strings.TrimSpace(values["memory"]); raw != "" {
		mib, err := config.ParseKubernetesMemoryToMi(raw)
		if err != nil {
			return model.KubernetesRequests{}, fmt.Errorf("memory request %q: %w", raw, err)
		}
		requests.MemoryBytes = mib * (1 << 20)
	}
	return requests, nil
}
