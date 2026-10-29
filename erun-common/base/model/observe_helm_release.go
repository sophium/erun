package model

// ObservedHelmRelease is the runtime release's record; Found false with Error set means observe could not look.
type ObservedHelmRelease struct {
	Name           string              `json:"name"`
	Found          bool                `json:"found"`
	Revision       int                 `json:"revision,omitempty"`
	Status         string              `json:"status,omitempty"`
	Chart          string              `json:"chart,omitempty"`
	ChartVersion   string              `json:"chartVersion,omitempty"`
	AppVersion     string              `json:"appVersion,omitempty"`
	ImageOverrides map[string]string   `json:"imageOverrides,omitempty"`
	RuntimePod     RuntimePodResources `json:"runtimePod,omitempty"`
	// Error explains why a field could not be populated; empty means the read is trustworthy.
	Error string `json:"error,omitempty"`
}
