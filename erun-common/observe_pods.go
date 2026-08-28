package eruncommon

import "fmt"

// ObservedPod is one pod's readiness as reported by the Ready condition, plus
// the reason it is not ready when it isn't.
type ObservedPod struct {
	Name         string              `json:"name"`
	Phase        string              `json:"phase"`
	Ready        bool                `json:"ready"`
	RestartCount int                 `json:"restartCount"`
	Reason       string              `json:"reason,omitempty"`
	Containers   []ObservedContainer `json:"containers,omitempty"`
}

// ObservedContainer names the image a pod's container is actually running —
// the single most consequential fact an orchestrator cannot see without this,
// since a hand-patched or out-of-band image looks identical to the recorded
// one in every other erun surface — plus the resource limits it was started
// with, read from the pod spec (status carries no limits of its own).
type ObservedContainer struct {
	Name           string            `json:"name"`
	Image          string            `json:"image"`
	ResourceLimits map[string]string `json:"resourceLimits,omitempty"`
}

// fetchObservedPods reuses podStatusList/podStatusItem (deploy_pod_watch.go):
// the same deliberately partial `kubectl get pods -o json` parse deploy's
// watcher uses, tolerating kubectl version drift the same way.
func fetchObservedPods(args []string) ([]ObservedPod, error) {
	raw, stderr, err := runObserveKubectl(args)
	if err != nil {
		return nil, fmt.Errorf("observe: get pods: %w", kubectlErrorMessage(err, stderr))
	}
	list, ok := parsePodStatusList(raw)
	if !ok {
		return nil, fmt.Errorf("observe: parse pods: unrecognized kubectl output")
	}
	pods := make([]ObservedPod, 0, len(list.Items))
	for _, item := range list.Items {
		pods = append(pods, observedPodFromStatus(item))
	}
	return pods, nil
}

func observedPodFromStatus(item podStatusItem) ObservedPod {
	pod := ObservedPod{
		Name:  item.Metadata.Name,
		Phase: item.Status.Phase,
		Ready: podStatusReady(item),
	}
	specLimits := make(map[string]map[string]string, len(item.Spec.Containers))
	for _, c := range item.Spec.Containers {
		specLimits[c.Name] = c.Resources.Limits
	}
	for _, cs := range item.Status.ContainerStatuses {
		pod.RestartCount += cs.RestartCount
		pod.Containers = append(pod.Containers, ObservedContainer{
			Name:           cs.Name,
			Image:          cs.Image,
			ResourceLimits: specLimits[cs.Name],
		})
	}
	if !pod.Ready {
		pod.Reason = podStatusNotReadyReason(item)
	}
	return pod
}

func podStatusReady(item podStatusItem) bool {
	for _, cond := range item.Status.Conditions {
		if cond.Type == "Ready" {
			return cond.Status == "True"
		}
	}
	return false
}

// podStatusNotReadyReason prefers a container-level reason (the concrete
// image-pull/crash cause) and falls back to the pod-level conditions —
// PodScheduled is the only place a reason exists for a pod that never got
// admitted to a node at all.
func podStatusNotReadyReason(item podStatusItem) string {
	if reason := containerStatusReason(item.Status.ContainerStatuses); reason != "" {
		return reason
	}
	if reason := podConditionReason(item.Status.Conditions, "PodScheduled"); reason != "" {
		return reason
	}
	return podConditionReason(item.Status.Conditions, "Ready")
}

func containerStatusReason(statuses []containerStatusEntry) string {
	for _, cs := range statuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			return cs.State.Waiting.Reason
		}
		if cs.State.Terminated != nil && cs.State.Terminated.Reason != "" {
			return cs.State.Terminated.Reason
		}
	}
	return ""
}

func podConditionReason(conditions []podConditionEntry, conditionType string) string {
	for _, cond := range conditions {
		if cond.Type == conditionType && cond.Status != "True" && cond.Reason != "" {
			return cond.Reason
		}
	}
	return ""
}
