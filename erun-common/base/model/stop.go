package model

// StopEnvironmentResult is the outcome of `erun stop`.
type StopEnvironmentResult struct {
	Tenant            string `json:"tenant"`
	Environment       string `json:"environment"`
	Namespace         string `json:"namespace"`
	Release           string `json:"release"`
	KubernetesContext string `json:"kubernetesContext"`
	Stopped           bool   `json:"stopped"`
	AlreadyStopped    bool   `json:"alreadyStopped"`
	// EndedSessions names the desktop terminal sessions the stop took down.
	EndedSessions []string `json:"endedSessions,omitempty"`
	// RemainingComponents names platform components the stop deliberately left running.
	RemainingComponents []string `json:"remainingComponents,omitempty"`
}
