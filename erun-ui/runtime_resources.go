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

// LoadHostedRegistry reports whether erun's hosted container registry is
// reachable right now, so the new-environment dialog can gate the "Use
// erun's hosted registry" option on a real check instead of offering it
// unconditionally — the asymmetry the in-cluster registry option does not
// have, since it is already gated on clusterRegistry.deployed.
func (a *App) LoadHostedRegistry() (uiHostedRegistryStatus, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return a.deps.loadHostedRegistry(ctx), nil
}

func loadHostedRegistry(ctx context.Context) uiHostedRegistryStatus {
	status := eruncommon.ProbeHostedRegistry(ctx, nil)
	return uiHostedRegistryStatus{
		Host:      status.Host,
		Available: status.Available,
		Reason:    status.Reason,
		Recovery:  status.Recovery,
	}
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
		bursting := accounting.usage[name]
		requests := accounting.requests[name]
		targetHeld := accounting.targetUsage[name]
		item := uiRuntimeResourceNode{
			Name: name,
			// What the scheduler will do: allocatable minus what the pods on the
			// node request. This is the reading a deploy's outcome follows, and
			// it is deliberately *not* floored at this environment's own size --
			// a floor here would report scheduling capacity the node does not
			// have, which is the one claim this reading exists to make honestly.
			Schedulable: uiRuntimeResourceReading{
				CPU:    cpuMetricFree(cpuTotal, requests.CPUMilli),
				Memory: memoryMetricFree(memoryTotal, requests.MemoryMi),
			},
			SchedulableComplete: accounting.unreadable[name] == 0,
			// Worst case: what would be left if every container on the node ran
			// to its declared limit at once. Floored at what this environment
			// already holds so its current setting stays representable, and
			// labelled as headroom rather than scheduling.
			WorstCase: uiRuntimeResourceReading{
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

func runtimeResourceNodeLabel(status uiRuntimeResourceStatus) string {
	if node := strings.TrimSpace(status.Node); node != "" {
		return node
	}
	return "the selected node"
}

// runtimeSchedulableMessage states which question the schedulable reading
// answers. It is the reading a deploy's outcome follows: the scheduler admits a
// pod on what it *requests*, and the CPU/memory the configuration dialog sets
// are *limits*, which reserve nothing. Reporting one figure without saying
// which question it answers is the reported defect -- the panel read 0 free
// from the limits sum, an operator read that as "a deploy will not schedule",
// and the remedy it offered was to shrink the very limit the node's capacity
// planning assumes will be burst into.
func runtimeSchedulableMessage(status uiRuntimeResourceStatus, targeted bool) string {
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

// runtimeWorstCaseMessage labels the limits reading as the capacity-planning
// question it is. It answers "how much of this node is committed if everything
// bursts at once", which is a legitimate question and not the one a deploy
// outcome follows.
func runtimeWorstCaseMessage(status uiRuntimeResourceStatus) string {
	return fmt.Sprintf("Worst case, with every container on %s at its declared limit at once: %s CPU and %s memory left. "+
		"That is oversubscription headroom, not scheduling capacity.",
		runtimeResourceNodeLabel(status), status.WorstCase.CPU.Formatted, status.WorstCase.Memory.Formatted)
}

// runtimeSchedulableNotice carries the one thing this reading's figures cannot:
// what to do when the node has nothing left to admit a pod with. The remedy is
// capacity on the node, because that is what the scheduler is short of.
func runtimeSchedulableNotice(status uiRuntimeResourceStatus) string {
	if !status.SchedulableComplete && status.UnreadableRequests > 0 {
		return fmt.Sprintf("%s on this node declares a request this reading could not read, so the free figure above is an upper bound.",
			pluralizePods(status.UnreadableRequests))
	}
	if status.Schedulable.CPU.Free > 0 && status.Schedulable.Memory.Free > 0 {
		return ""
	}
	return "The scheduler has nothing left on this node to admit another pod with. " +
		"Stopping an environment nobody is using on it returns its reservation."
}

// runtimeWorstCaseNotice carries what the worst-case figure alone cannot say:
// that it is the node being committed rather than a ceiling on this
// environment, what part of the node's real consumption is invisible to it, and
// -- the reported defect -- which levers actually move it. It never offers a
// smaller limit: the runtime limit is sized for the cold `make check-gate` an
// agent runs in that container, and lowering it re-creates the out-of-memory
// kills that destroy an in-pod agent run and its unpushed work.
func runtimeWorstCaseNotice(status uiRuntimeResourceStatus) string {
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

// runtimeResourceAccounting is the per-node picture the status is built from,
// in both resource domains: what every pod holds at its declared limit, what
// every pod requests of the scheduler, what this environment's own container
// holds, and how much of either the reading could not account for at all.
type runtimeResourceAccounting struct {
	// usage is the node's commitment if every container bursts to its limit.
	usage map[string]runtimeResourceTotals
	// requests is what the scheduler has admitted the node's pods on. A limit
	// reserves nothing and a request is not in any cgroup, so this comes from
	// the pod specs rather than from the same reading as usage.
	requests map[string]runtimeResourceTotals
	// targetUsage is what this environment's own runtime container holds at its
	// limit, which is the floor the worst-case reading must not fall below.
	targetUsage map[string]runtimeResourceTotals
	// unaccounted counts containers that declare no limit and had no measured
	// usage either; unreadable counts pods whose declared requests could not be
	// read. Neither is ever counted as zero.
	unaccounted map[string]int
	unreadable  map[string]int
	targetNode  string
}

// accumulateRuntimePodUsage sums what the node is committed to, in both
// domains. A container's limit is the best answer when it declares one; when it
// does not, its measured usage is used instead, and when there is no
// measurement either the container is counted as unaccounted rather than as
// zero.
//
// Treating a limitless container as zero is what made the figure wrong in
// practice: every erun-dind sidecar declares no limits, so a node's real
// consumption — Testcontainers, the buildkit cache — was entirely invisible to
// a reading that summed limits alone.
//
// The request leg applies erun-common's EffectiveKubernetesPodRequests, which
// is the same admission rule the per-environment usage reading reports: a
// second spelling of "max over the init containers, sum over the containers"
// would understate what a node has already committed and report scheduling
// capacity that is not there.
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

		// The scheduler counts the whole pod -- the runtime container, the
		// erun-dind sidecar, and the init phase's own peak -- including this
		// environment's own pod, which is on the node and is holding its
		// reservation right now.
		podRequests, err := eruncommon.EffectiveKubernetesPodRequests(
			kubernetesContainerResources(pod.Spec.Containers),
			kubernetesContainerResources(pod.Spec.InitContainers),
		)
		if err != nil {
			// Unreadable is not zero: this pod reserves an amount the reading
			// cannot size, so the free figure is an upper bound rather than an
			// answer, and the status says so.
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

// kubernetesContainerResources projects a decoded pod spec onto erun-common's
// request-rule input, so the desktop never re-derives the rule itself.
func kubernetesContainerResources(containers []kubernetesContainer) []eruncommon.KubernetesContainerResources {
	out := make([]eruncommon.KubernetesContainerResources, 0, len(containers))
	for _, container := range containers {
		out = append(out, eruncommon.KubernetesContainerResources{
			Name:     container.Name,
			Requests: container.Resources.Requests,
		})
	}
	return out
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

// shouldUseRuntimeResourceNode picks the node the reading is anchored to. "The
// emptiest node" is the node a deploy would land on, and the scheduler decides
// that by requests, so the schedulable reading -- not the worst-case one -- is
// what selects it.
func shouldUseRuntimeResourceNode(name, targetNode string, item uiRuntimeResourceNode, result uiRuntimeResourceStatus) bool {
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
	return cpuMetric(totalMilli, freeMilli, usedMilli, floored)
}

// cpuMetricFree is the unfloored form: free capacity is exactly what the node
// has left, and a node with nothing left says zero rather than borrowing this
// environment's own size as a floor.
func cpuMetricFree(totalMilli, usedMilli int64) uiRuntimeResourceMetric {
	freeMilli := totalMilli - usedMilli
	if freeMilli < 0 {
		freeMilli = 0
	}
	return cpuMetric(totalMilli, freeMilli, usedMilli, false)
}

func cpuMetric(totalMilli, freeMilli, usedMilli int64, floored bool) uiRuntimeResourceMetric {
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
	return memoryMetric(totalMi, freeMi, usedMi, floored)
}

func memoryMetricFree(totalMi, usedMi int64) uiRuntimeResourceMetric {
	freeMi := totalMi - usedMi
	if freeMi < 0 {
		freeMi = 0
	}
	return memoryMetric(totalMi, freeMi, usedMi, false)
}

func memoryMetric(totalMi, freeMi, usedMi int64, floored bool) uiRuntimeResourceMetric {
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
		// InitContainers are read because Kubernetes admits a pod on
		// max(max over these, sum over Containers); a request reading that
		// skipped them would understate what the node has committed.
		InitContainers []kubernetesContainer `json:"initContainers"`
	} `json:"spec"`
}

type kubernetesContainer struct {
	Name      string `json:"name"`
	Resources struct {
		// Limits is the cgroup ceiling the worst-case reading sums. Requests is
		// what the scheduler admits the pod on, and no cgroup file carries it --
		// it is read from the pod spec, where it exists.
		Limits   kubernetesResourceQuantity `json:"limits"`
		Requests map[string]string          `json:"requests"`
	} `json:"resources"`
}

type kubernetesResourceQuantity struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}
