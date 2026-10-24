package clusterread

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/deploy/observe"
)

type ServiceList struct {
	Items []ServiceItem `json:"items"`
}

type ServiceItem struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Type  string `json:"type"`
		Ports []struct {
			Name     string `json:"name"`
			Port     int    `json:"port"`
			Protocol string `json:"protocol"`
		} `json:"ports"`
	} `json:"spec"`
}

// FetchObservedServices errors name the read, not "observe:": callers other than observe use it.
func FetchObservedServices(args []string) ([]model.ObservedService, error) {
	raw, stderr, err := observe.RunObserveKubectl(args)
	if err != nil {
		return nil, fmt.Errorf("get service: %w", observe.KubectlErrorMessage(err, stderr))
	}
	var list ServiceList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("parse service: %w", err)
	}
	return ParseObservedServices(list), nil
}

// ParseObservedServices sorts by name so a picker does not inherit kubectl's ordering.
func ParseObservedServices(list ServiceList) []model.ObservedService {
	services := make([]model.ObservedService, 0, len(list.Items))
	for _, item := range list.Items {
		if item.Metadata.Name == "" {
			continue
		}
		service := model.ObservedService{Name: item.Metadata.Name, Type: item.Spec.Type}
		for _, port := range item.Spec.Ports {
			service.Ports = append(service.Ports, model.ObservedServicePort{
				Name:     port.Name,
				Port:     port.Port,
				Protocol: port.Protocol,
			})
		}
		services = append(services, service)
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	return services
}
