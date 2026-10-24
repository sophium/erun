package eruncommon

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// RuntimePodRequestsRunnerFunc runs the one read-only kubectl invocation this
// reading needs (RuntimePodRequestsArgs) and returns its stdout. The nil
// default shells out on this process; a transport that must bound the call --
// the desktop's on-demand probe, whose whole budget is a few seconds -- injects
// one carrying its own deadline, so a `kubectl get pods` that hangs cannot
// outlive the reading it belongs to.
type RuntimePodRequestsRunnerFunc func(args []string) ([]byte, error)

// RuntimePodRequestsArgs is the read-only invocation the request reading runs:
// the same `get pods -o json` shape observe's own pod read uses, scoped to this
// release's pods by the label the chart sets. Exported so a transport that
// supplies its own runner builds the identical command rather than a second
// spelling of it.
func RuntimePodRequestsArgs(req config.ShellLaunchParams) []string {
	args := config.KubectlTargetArgs(req)
	args = append(args, "get", "pods", "-l", "app="+config.RuntimeReleaseName(req.Tenant), "-o", "json")
	return args
}

// readRuntimeUsageRequests traces and runs the pod-spec read. It never fails
// its caller: everything that can go wrong here is reported on the reading as
// Unavailable, because losing the reservation figures must not cost the cgroup
// reading beside them.
func readRuntimeUsageRequests(ctx core.Context, runner RuntimePodRequestsRunnerFunc, req config.ShellLaunchParams) *config.RuntimeUsageRequests {
	ctx.TraceCommand("", "kubectl", RuntimePodRequestsArgs(req)...)
	if ctx.DryRun {
		// Nothing was read, and a zero reading would be a reservation of
		// nothing: dry-run carries no Requests at all, matching the empty
		// RuntimeUsage the rest of a dry run returns.
		return nil
	}
	if runner == nil {
		runner = runPodRequestsKubectl
	}
	raw, err := runner(RuntimePodRequestsArgs(req))
	if err != nil {
		return &config.RuntimeUsageRequests{Unavailable: err.Error()}
	}
	requests, err := parseRuntimePodRequests(raw)
	if err != nil {
		return &config.RuntimeUsageRequests{Unavailable: err.Error()}
	}
	return requests
}

// runPodRequestsKubectl is the default runner: the same read-only kubectl get
// observe's pod read runs, with its stderr folded into the error so an
// unreachable API server is reported as what it is.
func runPodRequestsKubectl(args []string) ([]byte, error) {
	raw, stderr, err := runObserveKubectl(args)
	if err != nil {
		return nil, fmt.Errorf("kubectl get pods: %w", kubectlErrorMessage(err, stderr))
	}
	return raw, nil
}

// parseRuntimePodRequests turns one `kubectl get pods -o json` response into the
// request reading. Pure, so the pod selection and the quantity rules are
// testable without a cluster.
func parseRuntimePodRequests(raw []byte) (*config.RuntimeUsageRequests, error) {
	if strings.TrimSpace(string(raw)) == "" {
		return nil, fmt.Errorf("kubectl returned no pod list for this environment's release")
	}
	list, ok := parsePodStatusList(raw)
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
	return &config.RuntimeUsageRequests{Containers: containers, Pod: podRequests}, nil
}

// selectRuntimePodForRequests picks the pod whose spec the reading reports. A
// rollout leaves two pods carrying the same label for a moment, and the one
// actually serving is the Running one, so it wins over a Pending or terminating
// predecessor; a name-sorted first pod decides everything else, so the reading
// is deterministic rather than dependent on list order.
func selectRuntimePodForRequests(items []podStatusItem) (podStatusItem, bool) {
	running := make([]podStatusItem, 0, len(items))
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
		return podStatusItem{}, false
	}
	selected := preferred[0]
	for _, item := range preferred[1:] {
		if item.Metadata.Name < selected.Metadata.Name {
			selected = item
		}
	}
	return selected, true
}

// declaredContainerRequests reads each container's own declared request. A
// quantity this parser cannot read is an error rather than a zero: reporting a
// smaller reservation than the pod declares is the one wrong answer that
// matters here, since the whole reading exists to be compared against a limit.
func declaredContainerRequests(pod podStatusItem) (map[string]config.KubernetesRequests, error) {
	containers := make(map[string]config.KubernetesRequests, len(pod.Spec.Containers))
	for _, container := range pod.Spec.Containers {
		requests, err := kubernetesRequestsFromValues(container.Resources.Requests)
		if err != nil {
			return nil, fmt.Errorf("container %s: %w", container.Name, err)
		}
		containers[container.Name] = requests
	}
	if len(containers) == 0 {
		return nil, nil
	}
	return containers, nil
}

// effectivePodRequests is the request the scheduler admits the pod on.
func effectivePodRequests(pod podStatusItem) (config.KubernetesRequests, error) {
	return EffectiveKubernetesPodRequests(
		kubernetesContainerResources(pod.Spec.Containers),
		kubernetesContainerResources(pod.Spec.InitContainers),
	)
}

// KubernetesContainerResources is one container's declared resources as a pod
// spec carries them: the resources.requests map the scheduler sums, and the
// name a quantity this parser cannot read is reported against.
//
// It is exported because the admission rule below is not a caller's to
// re-derive. The desktop's node-capacity reading applies it to every pod on a
// node, and a second spelling of "max over the init containers, sum over the
// containers" would disagree with this one the first time either changed --
// silently, since both would still return a plausible number.
type KubernetesContainerResources struct {
	Name     string
	Requests map[string]string
}

func kubernetesContainerResources(containers []specContainerEntry) []KubernetesContainerResources {
	out := make([]KubernetesContainerResources, 0, len(containers))
	for _, container := range containers {
		out = append(out, KubernetesContainerResources{
			Name:     container.Name,
			Requests: container.Resources.Requests,
		})
	}
	return out
}

// EffectiveKubernetesPodRequests is the request the scheduler admits a pod on:
// max(max over the init containers, sum over the containers), per resource.
// Summing the containers alone understates any pod whose init container asks
// for more than their total, and a node-capacity reading that understates what
// is committed reports free capacity the scheduler does not have.
//
// A container that declares nothing for a resource contributes zero, which is
// the scheduler's own reading of it; a declared quantity that cannot be parsed
// is an error, never a dropped zero.
func EffectiveKubernetesPodRequests(containers, initContainers []KubernetesContainerResources) (config.KubernetesRequests, error) {
	sum, err := sumContainerRequests(containers)
	if err != nil {
		return config.KubernetesRequests{}, err
	}
	initPeak, err := maxContainerRequests(initContainers)
	if err != nil {
		return config.KubernetesRequests{}, err
	}
	return kubectlRequestsMax(sum, initPeak), nil
}

func sumContainerRequests(containers []KubernetesContainerResources) (config.KubernetesRequests, error) {
	total := config.KubernetesRequests{}
	for _, container := range containers {
		requests, err := kubernetesRequestsFromValues(container.Requests)
		if err != nil {
			return config.KubernetesRequests{}, fmt.Errorf("container %s: %w", container.Name, err)
		}
		total.CPUMilli += requests.CPUMilli
		total.MemoryBytes += requests.MemoryBytes
	}
	return total, nil
}

func maxContainerRequests(containers []KubernetesContainerResources) (config.KubernetesRequests, error) {
	peak := config.KubernetesRequests{}
	for _, container := range containers {
		requests, err := kubernetesRequestsFromValues(container.Requests)
		if err != nil {
			return config.KubernetesRequests{}, fmt.Errorf("init container %s: %w", container.Name, err)
		}
		peak = kubectlRequestsMax(peak, requests)
	}
	return peak, nil
}

func kubectlRequestsMax(a, b config.KubernetesRequests) config.KubernetesRequests {
	return config.KubernetesRequests{
		CPUMilli:    max(a.CPUMilli, b.CPUMilli),
		MemoryBytes: max(a.MemoryBytes, b.MemoryBytes),
	}
}

// kubernetesRequestsFromValues reads the cpu/memory entries of one
// resources.requests map. An absent entry is no declaration; a present entry
// that cannot be read is an error, never a dropped zero.
func kubernetesRequestsFromValues(values map[string]string) (config.KubernetesRequests, error) {
	requests := config.KubernetesRequests{}
	if raw := strings.TrimSpace(values["cpu"]); raw != "" {
		milli, err := config.ParseKubernetesCPUToMilli(raw)
		if err != nil {
			return config.KubernetesRequests{}, fmt.Errorf("cpu request %q: %w", raw, err)
		}
		requests.CPUMilli = milli
	}
	if raw := strings.TrimSpace(values["memory"]); raw != "" {
		mib, err := config.ParseKubernetesMemoryToMi(raw)
		if err != nil {
			return config.KubernetesRequests{}, fmt.Errorf("memory request %q: %w", raw, err)
		}
		requests.MemoryBytes = mib * (1 << 20)
	}
	return requests, nil
}
