package model

// A state file records that someone (CLI or desktop) established a forward, so it
// answers "is this environment reachable" for both transports.

// PortForwardState is one forward's record; sshd's extra fields are ignored.
type PortForwardState struct {
	Tenant            string `json:"tenant"`
	Environment       string `json:"environment"`
	KubernetesContext string `json:"kubernetesContext"`
	Namespace         string `json:"namespace"`
	LocalPort         int    `json:"localPort"`
	LogPath           string `json:"logPath,omitempty"`
	ProcessID         int    `json:"processId,omitempty"`
}
