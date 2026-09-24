package eruncommon

import (
	"fmt"
	"strings"
)

// The runtime pod's declared resource *requests* are the scheduler's own input:
// a limit decides what a container may grow to, a request decides whether the
// pod is admitted at all and how much of the node it reserves while it runs.
// Every sizing surface erun had reported limits alone -- the cgroup ceiling a
// usage reading divides against -- and `Mem 0% of 27.0 GiB` reads as
// provisioned when the same environment reserves 1 GiB and holds 27 GiB only as
// a ceiling. No cgroup file carries a request (memory.max is the limit,
// memory.current the usage), so this cannot come from the reading script the
// rest of runtime_usage.go runs: it is read from the pod spec, the one place a
// request exists.
//
// It reads the live pod rather than the deploy's recorded intent because the
// pod spec is what the scheduler actually acted on -- the chart's own request
// defaults, a LimitRange's defaultRequest, and anything set out of band all end
// up there and only there. Like every other reading in this package it reports
// its own unavailability rather than failing the call: a pod spec that cannot
// be read costs the request figures alone, never the usage reading beside them.

// KubernetesRequests is one container's declared resources.requests, or a pod's
// effective request, in the units the scheduler sums them in: millicores of CPU
// and bytes of memory.
//
// A zero field is a container that declares nothing for that resource, which is
// the scheduler's own reading of it -- an undeclared request reserves nothing --
// so no renderer may turn it into a "0" that reads as a measured reservation.
type KubernetesRequests struct {
	CPUMilli    int64 `json:"cpuMilli,omitempty"`
	MemoryBytes int64 `json:"memoryBytes,omitempty"`
}

// IsZero reports whether this pair carries no declaration at all, so a caller
// can omit it instead of rendering a pair of zeros.
func (r KubernetesRequests) IsZero() bool {
	return r.CPUMilli == 0 && r.MemoryBytes == 0
}

// RuntimeUsageRequests is what the scheduler admits this environment's runtime
// pod on, read from the live pod spec.
type RuntimeUsageRequests struct {
	// Containers is each container's own declared request, keyed by container
	// name. A container that declares nothing for a resource is present with
	// that resource zero, matching the pod spec; the runtime container and the
	// erun-dind sidecar are the two an operator is comparing against their own
	// limits above.
	Containers map[string]KubernetesRequests `json:"containers,omitempty"`
	// Pod is the effective request the scheduler sizes the pod for:
	// max(max over the init containers, sum over the containers), per resource.
	// Kubernetes admits a pod on the larger of the init phase's peak and the
	// containers' sum, so summing the containers alone understates a pod whose
	// init container asks for more.
	Pod KubernetesRequests `json:"pod,omitempty"`
	// Unavailable names why the pod spec could not be read -- no pod for this
	// release, kubectl's own failure, a declared quantity this parser cannot
	// read. Set with the rest of the reading left empty, so "could not read the
	// reservation" never renders as "reserves nothing".
	Unavailable string `json:"unavailable,omitempty"`
}

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
func RuntimePodRequestsArgs(req ShellLaunchParams) []string {
	args := kubectlTargetArgs(req)
	args = append(args, "get", "pods", "-l", "app="+RuntimeReleaseName(req.Tenant), "-o", "json")
	return args
}

// readRuntimeUsageRequests traces and runs the pod-spec read. It never fails
// its caller: everything that can go wrong here is reported on the reading as
// Unavailable, because losing the reservation figures must not cost the cgroup
// reading beside them.
func readRuntimeUsageRequests(ctx Context, runner RuntimePodRequestsRunnerFunc, req ShellLaunchParams) *RuntimeUsageRequests {
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
		return &RuntimeUsageRequests{Unavailable: err.Error()}
	}
	requests, err := parseRuntimePodRequests(raw)
	if err != nil {
		return &RuntimeUsageRequests{Unavailable: err.Error()}
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
func parseRuntimePodRequests(raw []byte) (*RuntimeUsageRequests, error) {
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
	return &RuntimeUsageRequests{Containers: containers, Pod: podRequests}, nil
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
func declaredContainerRequests(pod podStatusItem) (map[string]KubernetesRequests, error) {
	containers := make(map[string]KubernetesRequests, len(pod.Spec.Containers))
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
func effectivePodRequests(pod podStatusItem) (KubernetesRequests, error) {
	sum, err := sumContainerRequests(pod.Spec.Containers)
	if err != nil {
		return KubernetesRequests{}, err
	}
	initPeak, err := maxContainerRequests(pod.Spec.InitContainers)
	if err != nil {
		return KubernetesRequests{}, err
	}
	return kubectlRequestsMax(sum, initPeak), nil
}

func sumContainerRequests(containers []specContainerEntry) (KubernetesRequests, error) {
	total := KubernetesRequests{}
	for _, container := range containers {
		requests, err := kubernetesRequestsFromValues(container.Resources.Requests)
		if err != nil {
			return KubernetesRequests{}, fmt.Errorf("container %s: %w", container.Name, err)
		}
		total.CPUMilli += requests.CPUMilli
		total.MemoryBytes += requests.MemoryBytes
	}
	return total, nil
}

func maxContainerRequests(containers []specContainerEntry) (KubernetesRequests, error) {
	peak := KubernetesRequests{}
	for _, container := range containers {
		requests, err := kubernetesRequestsFromValues(container.Resources.Requests)
		if err != nil {
			return KubernetesRequests{}, fmt.Errorf("init container %s: %w", container.Name, err)
		}
		peak = kubectlRequestsMax(peak, requests)
	}
	return peak, nil
}

func kubectlRequestsMax(a, b KubernetesRequests) KubernetesRequests {
	return KubernetesRequests{
		CPUMilli:    max(a.CPUMilli, b.CPUMilli),
		MemoryBytes: max(a.MemoryBytes, b.MemoryBytes),
	}
}

// kubernetesRequestsFromValues reads the cpu/memory entries of one
// resources.requests map. An absent entry is no declaration; a present entry
// that cannot be read is an error, never a dropped zero.
func kubernetesRequestsFromValues(values map[string]string) (KubernetesRequests, error) {
	requests := KubernetesRequests{}
	if raw := strings.TrimSpace(values["cpu"]); raw != "" {
		milli, err := ParseKubernetesCPUToMilli(raw)
		if err != nil {
			return KubernetesRequests{}, fmt.Errorf("cpu request %q: %w", raw, err)
		}
		requests.CPUMilli = milli
	}
	if raw := strings.TrimSpace(values["memory"]); raw != "" {
		mib, err := ParseKubernetesMemoryToMi(raw)
		if err != nil {
			return KubernetesRequests{}, fmt.Errorf("memory request %q: %w", raw, err)
		}
		requests.MemoryBytes = mib * (1 << 20)
	}
	return requests, nil
}
