package observe

import (
	"encoding/json"

	"github.com/sophium/erun/erun-common/orchestration/clusterread"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// ObservedSecretCheck reports whether a Secret carries a key, never the value. Error carries
// a non-"not found" failure such as an RBAC denial.
type ObservedSecretCheck struct {
	Name   string `json:"name"`
	Key    string `json:"key"`
	Exists bool   `json:"exists"`
	HasKey bool   `json:"hasKey"`
	Error  string `json:"error,omitempty"`
}

// observeSecretItem reads only key names, never values.
type observeSecretItem struct {
	Data       map[string]string `json:"data"`
	StringData map[string]string `json:"stringData"`
}

func fetchObservedSecretCheck(args []string, check ObserveSecretCheck) ObservedSecretCheck {
	result := ObservedSecretCheck{Name: check.Name, Key: check.Key}
	raw, stderr, err := deploy.RunObserveKubectl(args)
	if err != nil {
		if clusterread.IsKubectlNotFound(stderr) {
			return result
		}
		result.Error = deploy.KubectlErrorMessage(err, stderr).Error()
		return result
	}
	var secret observeSecretItem
	if err := json.Unmarshal(raw, &secret); err != nil {
		result.Error = "parse secret: " + err.Error()
		return result
	}
	result.Exists = true
	if check.Key == "" {
		return result
	}
	if _, ok := secret.Data[check.Key]; ok {
		result.HasKey = true
		return result
	}
	_, result.HasKey = secret.StringData[check.Key]
	return result
}
