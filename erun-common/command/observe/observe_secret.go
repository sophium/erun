package observe

import (
	"encoding/json"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/orchestration/clusterread"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// observeSecretItem reads only key names, never values.
type observeSecretItem struct {
	Data       map[string]string `json:"data"`
	StringData map[string]string `json:"stringData"`
}

func fetchObservedSecretCheck(args []string, check model.ObserveSecretCheck) model.ObservedSecretCheck {
	result := model.ObservedSecretCheck{Name: check.Name, Key: check.Key}
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
