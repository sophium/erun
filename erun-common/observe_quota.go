package eruncommon

import (
	"encoding/json"
	"fmt"
)

// ObservedResourceQuota pairs a namespace's ResourceQuota cap with what is
// currently consumed against it.
type ObservedResourceQuota struct {
	Name string            `json:"name"`
	Hard map[string]string `json:"hard,omitempty"`
	Used map[string]string `json:"used,omitempty"`
}

// ObservedLimitRange is a namespace's LimitRange, per constraint entry.
type ObservedLimitRange struct {
	Name   string                   `json:"name"`
	Limits []ObservedLimitRangeItem `json:"limits,omitempty"`
}

type ObservedLimitRangeItem struct {
	Type           string            `json:"type"`
	Max            map[string]string `json:"max,omitempty"`
	Min            map[string]string `json:"min,omitempty"`
	Default        map[string]string `json:"default,omitempty"`
	DefaultRequest map[string]string `json:"defaultRequest,omitempty"`
}

// resourceQuotaList/limitRangeList are deliberately partial parses of
// `kubectl get resourcequota|limitrange -o json`, matching the podStatusList
// idiom in deploy_pod_watch.go: unknown fields are ignored so kubectl version
// drift does not break observe.
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

func fetchObservedResourceQuotas(args []string) ([]ObservedResourceQuota, error) {
	raw, stderr, err := runObserveKubectl(args)
	if err != nil {
		return nil, fmt.Errorf("observe: get resourcequota: %w", kubectlErrorMessage(err, stderr))
	}
	var list resourceQuotaList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("observe: parse resourcequota: %w", err)
	}
	quotas := make([]ObservedResourceQuota, 0, len(list.Items))
	for _, item := range list.Items {
		quotas = append(quotas, ObservedResourceQuota{Name: item.Metadata.Name, Hard: item.Status.Hard, Used: item.Status.Used})
	}
	return quotas, nil
}

func fetchObservedLimitRanges(args []string) ([]ObservedLimitRange, error) {
	raw, stderr, err := runObserveKubectl(args)
	if err != nil {
		return nil, fmt.Errorf("observe: get limitrange: %w", kubectlErrorMessage(err, stderr))
	}
	var list limitRangeList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("observe: parse limitrange: %w", err)
	}
	ranges := make([]ObservedLimitRange, 0, len(list.Items))
	for _, item := range list.Items {
		lr := ObservedLimitRange{Name: item.Metadata.Name}
		for _, l := range item.Spec.Limits {
			lr.Limits = append(lr.Limits, ObservedLimitRangeItem(l))
		}
		ranges = append(ranges, lr)
	}
	return ranges, nil
}
