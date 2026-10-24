package model

type RuntimePodDiagnostic struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Status struct {
		Phase                 string                             `json:"phase"`
		Reason                string                             `json:"reason"`
		Message               string                             `json:"message"`
		Conditions            []RuntimePodConditionDiagnostic    `json:"conditions"`
		InitContainerStatuses []RuntimeContainerStatusDiagnostic `json:"initContainerStatuses"`
		ContainerStatuses     []RuntimeContainerStatusDiagnostic `json:"containerStatuses"`
	} `json:"status"`
}

type RuntimePodConditionDiagnostic struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type RuntimeContainerStatusDiagnostic struct {
	Name         string                          `json:"name"`
	Ready        bool                            `json:"ready"`
	RestartCount int                             `json:"restartCount"`
	State        RuntimeContainerStateDiagnostic `json:"state"`
	LastState    RuntimeContainerStateDiagnostic `json:"lastState"`
}

type RuntimeContainerStateDiagnostic struct {
	Waiting    *runtimeContainerWaitingDiagnostic    `json:"waiting"`
	Running    *runtimeContainerRunningDiagnostic    `json:"running"`
	Terminated *runtimeContainerTerminatedDiagnostic `json:"terminated"`
}

type runtimeContainerWaitingDiagnostic struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type runtimeContainerRunningDiagnostic struct {
	StartedAt string `json:"startedAt"`
}

type runtimeContainerTerminatedDiagnostic struct {
	ExitCode   int    `json:"exitCode"`
	Reason     string `json:"reason"`
	Message    string `json:"message"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
}
