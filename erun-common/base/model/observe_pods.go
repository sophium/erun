package model

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
