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
	// Backends are the in-namespace Services this Ingress routes to. Without
	// them an exposure can only be matched to a Service by re-deriving the
	// naming convention that produced it, which is exactly the assumption a
	// repo-native chart breaks.
	Backends []ObservedIngressBackend `json:"backends,omitempty"`
}

// ObservedIngressBackend is one Service an Ingress rule routes to.
type ObservedIngressBackend struct {
	Service string `json:"service"`
	Port    int    `json:"port,omitempty"`
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
							Port struct {
								Number int `json:"number"`
							} `json:"port"`
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
		for _, rule := range item.Spec.Rules {
			if rule.Host != "" {
				ing.Hosts = append(ing.Hosts, rule.Host)
			}
			for _, path := range rule.HTTP.Paths {
				if name := path.Backend.Service.Name; name != "" {
					ing.Backends = appendUniqueIngressBackend(ing.Backends, ObservedIngressBackend{
						Service: name,
						Port:    path.Backend.Service.Port.Number,
					})
				}
			}
		}
		for _, tls := range item.Spec.TLS {
			ing.TLS = append(ing.TLS, ObservedIngressTLS{Hosts: tls.Hosts, SecretName: tls.SecretName})
		}
		ingresses = append(ingresses, ing)
	}
	return ingresses, nil
}

// appendUniqueIngressBackend keeps one entry per (service, port): an Ingress
// routing several paths to the same Service is the ordinary shape, and
// repeating it would read as several backends.
func appendUniqueIngressBackend(backends []ObservedIngressBackend, backend ObservedIngressBackend) []ObservedIngressBackend {
	for _, existing := range backends {
		if existing == backend {
			return backends
		}
	}
	return append(backends, backend)
}
