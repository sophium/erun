package observe

import (
	"fmt"

	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// ObservedPod is one pod's readiness and, when not ready, the reason.
type ObservedPod struct {
	Name         string              `json:"name"`
	Phase        string              `json:"phase"`
	Ready        bool                `json:"ready"`
	RestartCount int                 `json:"restartCount"`
	Reason       string              `json:"reason,omitempty"`
	Containers   []ObservedContainer `json:"containers,omitempty"`
}

// ObservedContainer is the image a container actually runs, plus its spec limits;
// a hand-patched image is otherwise invisible.
type ObservedContainer struct {
	Name           string            `json:"name"`
	Image          string            `json:"image"`
	ResourceLimits map[string]string `json:"resourceLimits,omitempty"`
}

// fetchObservedPods reuses deploy's partial pod parse, tolerating kubectl version drift.
func fetchObservedPods(args []string) ([]ObservedPod, error) {
	raw, stderr, err := deploy.RunObserveKubectl(args)
	if err != nil {
		return nil, fmt.Errorf("observe: get pods: %w", deploy.KubectlErrorMessage(err, stderr))
	}
	list, ok := deploy.ParsePodStatusList(raw)
	if !ok {
		return nil, fmt.Errorf("observe: parse pods: unrecognized kubectl output")
	}
	pods := make([]ObservedPod, 0, len(list.Items))
	for _, item := range list.Items {
		pods = append(pods, observedPodFromStatus(item))
	}
	return pods, nil
}

func observedPodFromStatus(item deploy.PodStatusItem) ObservedPod {
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

func podStatusReady(item deploy.PodStatusItem) bool {
	for _, cond := range item.Status.Conditions {
		if cond.Type == "Ready" {
			return cond.Status == "True"
		}
	}
	return false
}

// podStatusNotReadyReason prefers a container reason; PodScheduled is the only reason for an
// unadmitted pod.
func podStatusNotReadyReason(item deploy.PodStatusItem) string {
	if reason := containerStatusReason(item.Status.ContainerStatuses); reason != "" {
		return reason
	}
	if reason := podConditionReason(item.Status.Conditions, "PodScheduled"); reason != "" {
		return reason
	}
	return podConditionReason(item.Status.Conditions, "Ready")
}

func containerStatusReason(statuses []deploy.ContainerStatusEntry) string {
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

func podConditionReason(conditions []deploy.PodConditionEntry, conditionType string) string {
	for _, cond := range conditions {
		if cond.Type == conditionType && cond.Status != "True" && cond.Reason != "" {
			return cond.Reason
		}
	}
	return ""
}
