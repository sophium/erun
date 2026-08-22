package eruncommon

import "encoding/json"

// ObservedSecretCheck reports whether a named Secret exists and carries a
// named key — never the value. Error carries a non-"not found" kubectl
// failure (for example an RBAC denial) so that case is never silently
// reported as the secret simply not existing.
type ObservedSecretCheck struct {
	Name   string `json:"name"`
	Key    string `json:"key"`
	Exists bool   `json:"exists"`
	HasKey bool   `json:"hasKey"`
	Error  string `json:"error,omitempty"`
}

// observeSecretItem is a deliberately partial parse of `kubectl get secret -o
// json`: only key names are read out of Data/StringData, never a value.
type observeSecretItem struct {
	Data       map[string]string `json:"data"`
	StringData map[string]string `json:"stringData"`
}

func fetchObservedSecretCheck(args []string, check ObserveSecretCheck) ObservedSecretCheck {
	result := ObservedSecretCheck{Name: check.Name, Key: check.Key}
	raw, stderr, err := runObserveKubectl(args)
	if err != nil {
		if isKubectlNotFound(stderr) {
			return result
		}
		result.Error = kubectlErrorMessage(err, stderr).Error()
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
