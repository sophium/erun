package model

// ObservedResourceQuota pairs a ResourceQuota cap with current usage.
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
