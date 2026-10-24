package clusterread

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// ObservedService is one Service in an environment's namespace.
type ObservedService struct {
	Name  string                `json:"name"`
	Type  string                `json:"type,omitempty"`
	Ports []ObservedServicePort `json:"ports,omitempty"`
}

// ObservedServicePort is one published port; Name is empty for a single-port Service.
type ObservedServicePort struct {
	Name     string `json:"name,omitempty"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol,omitempty"`
}

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
func FetchObservedServices(args []string) ([]ObservedService, error) {
	raw, stderr, err := deploy.RunObserveKubectl(args)
	if err != nil {
		return nil, fmt.Errorf("get service: %w", deploy.KubectlErrorMessage(err, stderr))
	}
	var list ServiceList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("parse service: %w", err)
	}
	return ParseObservedServices(list), nil
}

// ParseObservedServices sorts by name so a picker does not inherit kubectl's ordering.
func ParseObservedServices(list ServiceList) []ObservedService {
	services := make([]ObservedService, 0, len(list.Items))
	for _, item := range list.Items {
		if item.Metadata.Name == "" {
			continue
		}
		service := ObservedService{Name: item.Metadata.Name, Type: item.Spec.Type}
		for _, port := range item.Spec.Ports {
			service.Ports = append(service.Ports, ObservedServicePort{
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
