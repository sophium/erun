package observe

import (
	"encoding/json"
	"fmt"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// Deliberately partial parses, so kubectl version drift does not break observe.
type resourceQuotaList struct {
	Items []resourceQuotaItem `json:"items"`
}

type resourceQuotaItem struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Status struct {
		Hard map[string]string `json:"hard"`
		Used map[string]string `json:"used"`
	} `json:"status"`
}

type limitRangeList struct {
	Items []limitRangeItem `json:"items"`
}

type limitRangeItem struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Limits []limitRangeLimitEntry `json:"limits"`
	} `json:"spec"`
}

type limitRangeLimitEntry struct {
	Type           string            `json:"type"`
	Max            map[string]string `json:"max"`
	Min            map[string]string `json:"min"`
	Default        map[string]string `json:"default"`
	DefaultRequest map[string]string `json:"defaultRequest"`
}

func fetchObservedResourceQuotas(args []string) ([]model.ObservedResourceQuota, error) {
	raw, stderr, err := deploy.RunObserveKubectl(args)
	if err != nil {
		return nil, fmt.Errorf("observe: get resourcequota: %w", deploy.KubectlErrorMessage(err, stderr))
	}
	var list resourceQuotaList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("observe: parse resourcequota: %w", err)
	}
	quotas := make([]model.ObservedResourceQuota, 0, len(list.Items))
	for _, item := range list.Items {
		quotas = append(quotas, model.ObservedResourceQuota{Name: item.Metadata.Name, Hard: item.Status.Hard, Used: item.Status.Used})
	}
	return quotas, nil
}

func fetchObservedLimitRanges(args []string) ([]model.ObservedLimitRange, error) {
	raw, stderr, err := deploy.RunObserveKubectl(args)
	if err != nil {
		return nil, fmt.Errorf("observe: get limitrange: %w", deploy.KubectlErrorMessage(err, stderr))
	}
	var list limitRangeList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("observe: parse limitrange: %w", err)
	}
	ranges := make([]model.ObservedLimitRange, 0, len(list.Items))
	for _, item := range list.Items {
		lr := model.ObservedLimitRange{Name: item.Metadata.Name}
		for _, l := range item.Spec.Limits {
			lr.Limits = append(lr.Limits, model.ObservedLimitRangeItem(l))
		}
		ranges = append(ranges, lr)
	}
	return ranges, nil
}
