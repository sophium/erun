package eruncommon

import (
	"errors"
	"fmt"
	"strings"
)

// ExposedService is one active exposure erun-expose created for an
// environment: a Host-routing Ingress named "expose-<service>", discovered by
// listing the env namespace's Ingresses. The Ingress is the source of truth,
// so there is no separate record to keep in sync with it.
type ExposedService struct {
	Service  string `json:"service"`
	Hostname string `json:"hostname"`
	Scheme   string `json:"scheme"`
}

// exposeIngressNamePrefix matches the name resolveExposeServicePlan gives
// every Ingress RunExposeService applies.
const exposeIngressNamePrefix = "expose-"

// ErrListExposedServicesForbidden reports that the caller's Kubernetes
// credentials are not authorized to list the env namespace's Ingresses, so a
// caller can render a permission-restricted state rather than an empty one.
var ErrListExposedServicesForbidden = errors.New("list exposed services: forbidden")

// ListExposedServices reports the environment's active exposures by reading
// its namespace's Ingresses and keeping the ones erun-expose created.
func ListExposedServices(req ShellLaunchParams) ([]ExposedService, error) {
	ingresses, err := fetchObservedIngresses(observeGetArgs(req, "ingress"))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "forbidden") {
			return nil, fmt.Errorf("%w: %s", ErrListExposedServicesForbidden, err)
		}
		return nil, err
	}
	return filterExposedServices(ingresses), nil
}

// filterExposedServices maps observed Ingresses to the exposures erun-expose
// created among them, split out from ListExposedServices as a pure function so
// it can be tested without a kubectl process.
func filterExposedServices(ingresses []ObservedIngress) []ExposedService {
	tlsHosts := make(map[string]bool)
	for _, ing := range ingresses {
		for _, tls := range ing.TLS {
			for _, host := range tls.Hosts {
				tlsHosts[host] = true
			}
		}
	}
	services := make([]ExposedService, 0, len(ingresses))
	for _, ing := range ingresses {
		service, ok := strings.CutPrefix(ing.Name, exposeIngressNamePrefix)
		if !ok || service == "" || len(ing.Hosts) == 0 {
			continue
		}
		host := ing.Hosts[0]
		scheme := "http"
		if tlsHosts[host] {
			scheme = "https"
		}
		services = append(services, ExposedService{Service: service, Hostname: host, Scheme: scheme})
	}
	return services
}
