package eruncommon

import (
	"errors"
	"fmt"
	"strings"
)

// EnvironmentService is one Service in an environment's namespace as the
// expose picker sees it: the Service an Ingress would route to, the ports it
// offers, and the exposure it already has. It is the answer to "what is this
// environment running", which until now nothing could report -- the only
// listing was of exposures, so choosing something to expose meant already
// knowing its name.
type EnvironmentService struct {
	Name  string                `json:"name"`
	Type  string                `json:"type,omitempty"`
	Ports []ObservedServicePort `json:"ports,omitempty"`
	// Exposure is set when an erun-expose Ingress already fronts this Service,
	// so a caller can offer "open" rather than "expose" for it.
	Exposure *ServiceExposure `json:"exposure,omitempty"`
}

// ServiceExposure is the public face an EnvironmentService already has.
// Label is the logical name in the hostname, which is not necessarily the
// Service's own name.
type ServiceExposure struct {
	Label    string `json:"label"`
	Hostname string `json:"hostname"`
	Scheme   string `json:"scheme"`
}

// ErrListEnvironmentServicesForbidden reports that the caller's Kubernetes
// credentials cannot list the namespace's Services, so a caller can render a
// permission-restricted state rather than an empty environment.
var ErrListEnvironmentServicesForbidden = errors.New("list environment services: forbidden")

// ListEnvironmentServices reports the environment's Services and, for each,
// the exposure erun-expose already gave it. Both reads are plain `kubectl
// get`s, so this is safe to grant to a caller that must never be handed
// `erun exec raw`.
func ListEnvironmentServices(req ShellLaunchParams) ([]EnvironmentService, error) {
	services, err := fetchObservedServices(observeGetArgs(req, "service"))
	if err != nil {
		if isForbiddenKubectlError(err) {
			return nil, fmt.Errorf("%w: %s", ErrListEnvironmentServicesForbidden, err)
		}
		return nil, err
	}
	ingresses, err := fetchObservedIngresses(observeGetArgs(req, "ingress"))
	if err != nil {
		if isForbiddenKubectlError(err) {
			return nil, fmt.Errorf("%w: %s", ErrListEnvironmentServicesForbidden, err)
		}
		return nil, err
	}
	return mergeServicesWithExposures(services, ingresses), nil
}

// isForbiddenKubectlError keeps the RBAC case distinguishable from a real
// failure. It matches on the message because kubectl reports authorization
// through its exit status and text, not a typed error.
func isForbiddenKubectlError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "forbidden")
}

// mergeServicesWithExposures attaches each erun-expose Ingress to the Service
// it actually routes to, read from the Ingress backend rather than re-derived
// from the naming convention that produced it -- the convention is what a
// repo-native chart breaks, and re-deriving it here would reproduce that bug
// in the list. Split out as a pure function so the matching is testable
// without a cluster.
func mergeServicesWithExposures(services []ObservedService, ingresses []ObservedIngress) []EnvironmentService {
	exposures := exposuresByBackendService(ingresses)
	out := make([]EnvironmentService, 0, len(services))
	for _, service := range services {
		entry := EnvironmentService{Name: service.Name, Type: service.Type, Ports: service.Ports}
		if exposure, ok := exposures[service.Name]; ok {
			entry.Exposure = &exposure
		}
		out = append(out, entry)
	}
	return out
}

func exposuresByBackendService(ingresses []ObservedIngress) map[string]ServiceExposure {
	tlsHosts := tlsHostSet(ingresses)
	byService := make(map[string]ServiceExposure, len(ingresses))
	for _, ing := range ingresses {
		label, ok := strings.CutPrefix(ing.Name, exposeIngressNamePrefix)
		if !ok || label == "" || len(ing.Hosts) == 0 {
			continue
		}
		host := ing.Hosts[0]
		scheme := "http"
		if tlsHosts[host] {
			scheme = "https"
		}
		for _, backend := range ing.Backends {
			if _, seen := byService[backend.Service]; seen {
				continue
			}
			byService[backend.Service] = ServiceExposure{Label: label, Hostname: host, Scheme: scheme}
		}
	}
	return byService
}
