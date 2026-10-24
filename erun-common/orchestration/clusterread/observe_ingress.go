package clusterread

import (
	"encoding/json"
	"fmt"

	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// ObservedIngress is one Ingress's routed hosts and TLS secrets.
type ObservedIngress struct {
	Name  string               `json:"name"`
	Hosts []string             `json:"hosts,omitempty"`
	TLS   []ObservedIngressTLS `json:"tls,omitempty"`
	// Backends avoids re-deriving Service names from a convention repo-native charts break.
	Backends []ObservedIngressBackend `json:"backends,omitempty"`
}

type ObservedIngressBackend struct {
	Service string `json:"service"`
	Port    int    `json:"port,omitempty"`
}

type ObservedIngressTLS struct {
	Hosts      []string `json:"hosts,omitempty"`
	SecretName string   `json:"secretName,omitempty"`
}

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

// FetchObservedIngresses errors name the read, not "observe:": callers other than observe use it.
func FetchObservedIngresses(args []string) ([]ObservedIngress, error) {
	raw, stderr, err := deploy.RunObserveKubectl(args)
	if err != nil {
		return nil, fmt.Errorf("get ingress: %w", deploy.KubectlErrorMessage(err, stderr))
	}
	var list ingressList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("parse ingress: %w", err)
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

func appendUniqueIngressBackend(backends []ObservedIngressBackend, backend ObservedIngressBackend) []ObservedIngressBackend {
	for _, existing := range backends {
		if existing == backend {
			return backends
		}
	}
	return append(backends, backend)
}
