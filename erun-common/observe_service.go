package eruncommon

import (
	"encoding/json"
	"fmt"
)

// ObservedService is one in-namespace Kubernetes Service and the ports it
// declares, matching `metadata.name` / `spec.ports[]`.
type ObservedService struct {
	Name  string                `json:"name"`
	Ports []ObservedServicePort `json:"ports,omitempty"`
}

type ObservedServicePort struct {
	Name     string `json:"name,omitempty"`
	Port     int32  `json:"port"`
	Protocol string `json:"protocol,omitempty"`
}

// serviceList is a deliberately partial parse of `kubectl get service -o
// json`, matching the ingressList idiom in observe_ingress.go.
type serviceList struct {
	Items []serviceItem `json:"items"`
}

type serviceItem struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Ports []struct {
			Name     string `json:"name"`
			Port     int32  `json:"port"`
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
	services := make([]ObservedService, 0, len(list.Items))
	for _, item := range list.Items {
		svc := ObservedService{Name: item.Metadata.Name}
		for _, port := range item.Spec.Ports {
			svc.Ports = append(svc.Ports, ObservedServicePort{Name: port.Name, Port: port.Port, Protocol: port.Protocol})
		}
		services = append(services, svc)
	}
	return services, nil
}
