package observe

import (
	"encoding/json"
)

// PodStatusList is a partial parse so kubectl version drift does not break it.
type PodStatusList struct {
	Items []PodStatusItem `json:"items"`
}

type PodStatusItem struct {
	Metadata struct {
		Name        string            `json:"name"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		Containers     []SpecContainerEntry `json:"containers"`
		InitContainers []SpecContainerEntry `json:"initContainers"`
	} `json:"spec"`
	Status struct {
		Phase                 string                 `json:"phase"`
		Conditions            []PodConditionEntry    `json:"conditions"`
		ContainerStatuses     []ContainerStatusEntry `json:"containerStatuses"`
		InitContainerStatuses []ContainerStatusEntry `json:"initContainerStatuses"`
	} `json:"status"`
}

// SpecContainerEntry carries the declared requests, which no cgroup file records.
type SpecContainerEntry struct {
	Name      string `json:"name"`
	Resources struct {
		Limits   map[string]string `json:"limits"`
		Requests map[string]string `json:"requests"`
	} `json:"resources"`
}

// PodConditionEntry carries the scheduler's verdict for a pod never admitted to
// a node, which has no container statuses.
type PodConditionEntry struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type ContainerStatusEntry struct {
	Name         string         `json:"name"`
	Image        string         `json:"image"`
	RestartCount int            `json:"restartCount"`
	Ready        bool           `json:"ready"`
	State        ContainerState `json:"state"`
	LastState    ContainerState `json:"lastState"`
}

type ContainerState struct {
	Waiting    *ContainerStateWaiting    `json:"waiting"`
	Running    *ContainerStateRunning    `json:"running"`
	Terminated *containerStateTerminated `json:"terminated"`
}

type ContainerStateWaiting struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type ContainerStateRunning struct {
	StartedAt string `json:"startedAt"`
}

type containerStateTerminated struct {
	Reason   string `json:"reason"`
	Message  string `json:"message"`
	ExitCode int    `json:"exitCode"`
}

func ParsePodStatusList(raw []byte) (PodStatusList, bool) {
	var list PodStatusList
	if err := json.Unmarshal(raw, &list); err != nil {
		return PodStatusList{}, false
	}
	return list, true
}
