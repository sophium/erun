package eruncommon

import (
	"encoding/json"
	"fmt"
)

// ObservedIngress is one Ingress's routed hosts and the TLS secret backing
// each host group, matching `spec.rules[].host` / `spec.tls[]`.
type ObservedIngress struct {
	Name  string               `json:"name"`
	Hosts []string             `json:"hosts,omitempty"`
	TLS   []ObservedIngressTLS `json:"tls,omitempty"`
	// Backends names every in-namespace Service this Ingress's rules route to
	// (`spec.rules[].http.paths[].backend.service.name`), deduplicated. This
	// is the ground truth for "does a real Ingress already route to Service
	// X" -- reading it directly avoids assuming the Ingress name or its public
	// host label matches the backend Service's real name.
	Backends []string `json:"backends,omitempty"`
}

type ObservedIngressTLS struct {
	Hosts      []string `json:"hosts,omitempty"`
	SecretName string   `json:"secretName,omitempty"`
}

// ingressList is a deliberately partial parse of `kubectl get ingress -o
// json`, matching the podStatusList idiom in deploy_pod_watch.go.
type ingressList struct {
	Items []ingressItem `json:"items"`
}

type ingressItem struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Rules []struct {
			Host string `json:"host"`
			HTTP struct {
				Paths []struct {
					Backend struct {
						Service struct {
							Name string `json:"name"`
						} `json:"service"`
					} `json:"backend"`
				} `json:"paths"`
			} `json:"http"`
		} `json:"rules"`
		TLS []struct {
			Hosts      []string `json:"hosts"`
			SecretName string   `json:"secretName"`
		} `json:"tls"`
	} `json:"spec"`
}

func fetchObservedIngresses(args []string) ([]ObservedIngress, error) {
	raw, stderr, err := runObserveKubectl(args)
	if err != nil {
		return nil, fmt.Errorf("observe: get ingress: %w", kubectlErrorMessage(err, stderr))
	}
	var list ingressList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("observe: parse ingress: %w", err)
	}
	ingresses := make([]ObservedIngress, 0, len(list.Items))
	for _, item := range list.Items {
		ing := ObservedIngress{Name: item.Metadata.Name}
		seenBackend := make(map[string]bool)
		for _, rule := range item.Spec.Rules {
			if rule.Host != "" {
				ing.Hosts = append(ing.Hosts, rule.Host)
			}
			for _, path := range rule.HTTP.Paths {
				name := path.Backend.Service.Name
				if name == "" || seenBackend[name] {
					continue
				}
				seenBackend[name] = true
				ing.Backends = append(ing.Backends, name)
			}
		}
		for _, tls := range item.Spec.TLS {
			ing.TLS = append(ing.TLS, ObservedIngressTLS{Hosts: tls.Hosts, SecretName: tls.SecretName})
		}
		ingresses = append(ingresses, ing)
	}
	return ingresses, nil
}
