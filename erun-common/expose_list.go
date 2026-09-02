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
	// BackendService is the in-namespace Service the Ingress routes to, read
	// from the Ingress rather than derived from Service: the two differ
	// whenever the chart that rendered the Service is the repo's own.
	BackendService string `json:"backendService,omitempty"`
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
	tlsHosts := tlsHostSet(ingresses)
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
		exposed := ExposedService{Service: service, Hostname: host, Scheme: scheme}
		if len(ing.Backends) > 0 {
			exposed.BackendService = ing.Backends[0].Service
		}
		services = append(services, exposed)
	}
	return services
}

// tlsHostSet collects every host any Ingress in the namespace terminates TLS
// for. Shared with the service listing so both answer "is this https" the same
// way: from a tls block that names the host, never from the scheme someone
// asked for.
func tlsHostSet(ingresses []ObservedIngress) map[string]bool {
	hosts := make(map[string]bool)
	for _, ing := range ingresses {
		for _, tls := range ing.TLS {
			for _, host := range tls.Hosts {
				hosts[host] = true
			}
		}
	}
	return hosts
}
