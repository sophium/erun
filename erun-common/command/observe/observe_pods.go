package observe

import (
	"fmt"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/deploy/observe"
)

// fetchObservedPods reuses deploy's partial pod parse, tolerating kubectl version drift.
func fetchObservedPods(args []string) ([]model.ObservedPod, error) {
	raw, stderr, err := observe.RunObserveKubectl(args)
	if err != nil {
		return nil, fmt.Errorf("observe: get pods: %w", observe.KubectlErrorMessage(err, stderr))
	}
	list, ok := observe.ParsePodStatusList(raw)
	if !ok {
		return nil, fmt.Errorf("observe: parse pods: unrecognized kubectl output")
	}
	pods := make([]model.ObservedPod, 0, len(list.Items))
	for _, item := range list.Items {
		pods = append(pods, observedPodFromStatus(item))
	}
	return pods, nil
}

func observedPodFromStatus(item observe.PodStatusItem) model.ObservedPod {
	pod := model.ObservedPod{
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
		pod.Containers = append(pod.Containers, model.ObservedContainer{
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

func podStatusReady(item observe.PodStatusItem) bool {
	for _, cond := range item.Status.Conditions {
		if cond.Type == "Ready" {
			return cond.Status == "True"
		}
	}
	return false
}

// podStatusNotReadyReason prefers a container reason; PodScheduled is the only reason for an
// unadmitted pod.
func podStatusNotReadyReason(item observe.PodStatusItem) string {
	if reason := containerStatusReason(item.Status.ContainerStatuses); reason != "" {
		return reason
	}
	if reason := podConditionReason(item.Status.Conditions, "PodScheduled"); reason != "" {
		return reason
	}
	return podConditionReason(item.Status.Conditions, "Ready")
}

func containerStatusReason(statuses []observe.ContainerStatusEntry) string {
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

func podConditionReason(conditions []observe.PodConditionEntry, conditionType string) string {
	for _, cond := range conditions {
		if cond.Type == conditionType && cond.Status != "True" && cond.Reason != "" {
			return cond.Reason
		}
	}
	return ""
}
