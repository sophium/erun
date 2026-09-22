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

// EnvironmentServiceList is the shared result of a services read: the
// environment it describes and the Services its namespace runs, each carrying
// the exposure erun-expose already gave it. It mirrors ObserveResult's shape
// so the CLI's --output json and the MCP tool return one contract rather than
// two renderings of the same read.
type EnvironmentServiceList struct {
	Tenant      string               `json:"tenant"`
	Environment string               `json:"environment"`
	Namespace   string               `json:"namespace"`
	Services    []EnvironmentService `json:"services"`
}

// ErrListEnvironmentServicesForbidden reports that the caller's Kubernetes
// credentials cannot list the namespace's Services, so a caller can render a
// permission-restricted state rather than an empty environment.
var ErrListEnvironmentServicesForbidden = errors.New("list environment services: forbidden")

// RunListEnvironmentServices is the CLI's and MCP's entry point to the same
// read the desktop's Ports tab picker makes: it traces the two `kubectl get`s
// before running them, so `--dry-run` reports exactly the calls a real run
// would make, in the same order. Traced and returned up front rather than
// discovered during execution because ListEnvironmentServices itself holds no
// Context -- it is the desktop's call, and the desktop has no trace to emit.
func RunListEnvironmentServices(ctx Context, req ShellLaunchParams) (EnvironmentServiceList, error) {
	result := EnvironmentServiceList{Tenant: req.Tenant, Environment: req.Environment, Namespace: req.Namespace}
	ctx.TraceCommand("", "kubectl", observeGetArgs(req, "service")...)
	ctx.TraceCommand("", "kubectl", observeGetArgs(req, "ingress")...)
	ctx.Trace("services: each Service is matched to an erun-expose Ingress by the Ingress's own backend, not by re-deriving the <tenant>-<service> naming convention")
	if ctx.DryRun {
		return result, nil
	}
	services, err := ListEnvironmentServices(req)
	if err != nil {
		return EnvironmentServiceList{}, err
	}
	result.Services = services
	return result, nil
}

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
	tlsSecretByHost := tlsSecretsByHost(ingresses)
	byService := make(map[string]ServiceExposure, len(ingresses))
	for _, ing := range ingresses {
		label, ok := strings.CutPrefix(ing.Name, exposeIngressNamePrefix)
		if !ok || label == "" || len(ing.Hosts) == 0 {
			continue
		}
		host := ing.Hosts[0]
		scheme := "http"
		if _, hasTLS := tlsSecretByHost[host]; hasTLS {
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
