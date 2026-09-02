package eruncommon

import (
	"encoding/json"
	"fmt"
	"sort"
)

// ObservedService is one Service in an environment's namespace. It exists so a
// caller can answer "what is this environment actually running, and on which
// port would I reach it" without composing kubectl: the question a developer
// asks before exposing something, and the one an orchestrator asks before
// routing to it.
type ObservedService struct {
	Name string `json:"name"`
	// Type is the Service type (ClusterIP, NodePort, LoadBalancer). Reported
	// because it changes what "reachable" means for the service even before any
	// ingress exists.
	Type  string                `json:"type,omitempty"`
	Ports []ObservedServicePort `json:"ports,omitempty"`
}

// ObservedServicePort is one port a Service publishes. Name is empty for a
// single-port Service, which is the common case and the reason the picker
// cannot rely on names alone.
type ObservedServicePort struct {
	Name     string `json:"name,omitempty"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol,omitempty"`
}

// serviceList is a deliberately partial parse of `kubectl get service -o
// json`, matching the ingressList idiom next door.
type serviceList struct {
	Items []serviceItem `json:"items"`
}

type serviceItem struct {
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

func fetchObservedServices(args []string) ([]ObservedService, error) {
	raw, stderr, err := runObserveKubectl(args)
	if err != nil {
		return nil, fmt.Errorf("observe: get service: %w", kubectlErrorMessage(err, stderr))
	}
	var list serviceList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("observe: parse service: %w", err)
	}
	return parseObservedServices(list), nil
}

// parseObservedServices is split out as a pure function so the mapping is
// testable without a kubectl process. Services are sorted by name: the list
// feeds a picker, and kubectl's own ordering is not something a UI should
// inherit.
func parseObservedServices(list serviceList) []ObservedService {
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
