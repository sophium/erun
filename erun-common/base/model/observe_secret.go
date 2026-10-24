package model

// ObservedSecretCheck reports whether a Secret carries a key, never the value. Error carries
// a non-"not found" failure such as an RBAC denial.
type ObservedSecretCheck struct {
	Name   string `json:"name"`
	Key    string `json:"key"`
	Exists bool   `json:"exists"`
	HasKey bool   `json:"hasKey"`
	Error  string `json:"error,omitempty"`
}
