package eruncommon

import (
	"errors"
	"fmt"
	"strings"
)

// EnvironmentService is one Kubernetes Service running in an environment's
// namespace, discovered directly rather than requiring an operator to already
// know its name and the `<tenant>-<service>` convention `erun expose` routes
// by (issue #1906). Exposed is ground truth read back from the namespace's
// own Ingresses -- never inferred from this Service's name -- so a caller can
// tell "already reachable at Hostname" apart from "not exposed" without
// guessing.
type EnvironmentService struct {
	Name  string                   `json:"name"`
	Ports []EnvironmentServicePort `json:"ports"`
	// Exposed is true only when a real erun-expose Ingress's backend routes to
	// this exact Service name. When true, Hostname/Scheme are read from that
	// Ingress, never derived.
	Exposed  bool   `json:"exposed"`
	Hostname string `json:"hostname,omitempty"`
	Scheme   string `json:"scheme,omitempty"`
	// ExposableLabel is the logical service label `erun expose` would need to
	// route back to this exact Service (BackendService in expose.go derives
	// "<tenant>-<service>" from it) -- this Service's name with the tenant's
	// resource prefix stripped. Empty when this Service's name does not carry
	// that prefix, which means expose has no way to route to it correctly
	// today (see erun-common/expose.go's BackendService derivation and issue
	// #1906's point 2) -- a caller must not offer to expose it. Only
	// meaningful when Exposed is false.
	ExposableLabel string `json:"exposableLabel,omitempty"`
}

type EnvironmentServicePort struct {
	Name     string `json:"name,omitempty"`
	Port     int32  `json:"port"`
	Protocol string `json:"protocol,omitempty"`
}

// ErrListEnvironmentServicesForbidden reports that the caller's Kubernetes
// credentials are not authorized to list the env namespace's Services or
// Ingresses, so a caller can render a permission-restricted state rather than
// an empty one.
var ErrListEnvironmentServicesForbidden = errors.New("list environment services: forbidden")

// ListEnvironmentServices reports every Service running in an environment's
// namespace, noting which are already reachable at a public hostname. It is
// read-only: two `kubectl get -o json` calls, traced before either runs so a
// dry-run is a complete plan.
func ListEnvironmentServices(ctx Context, req ShellLaunchParams, tenant string) ([]EnvironmentService, error) {
	serviceArgs := observeGetArgs(req, "service")
	ingressArgs := observeGetArgs(req, "ingress")
	ctx.TraceCommand("", "kubectl", serviceArgs...)
	ctx.TraceCommand("", "kubectl", ingressArgs...)
	if ctx.DryRun {
		return nil, nil
	}

	services, err := fetchObservedServices(serviceArgs)
	if err != nil {
		return nil, wrapForbiddenListError(err)
	}
	ingresses, err := fetchObservedIngresses(ingressArgs)
	if err != nil {
		return nil, wrapForbiddenListError(err)
	}
	return buildEnvironmentServices(tenant, services, ingresses), nil
}

func wrapForbiddenListError(err error) error {
	if strings.Contains(strings.ToLower(err.Error()), "forbidden") {
		return fmt.Errorf("%w: %s", ErrListEnvironmentServicesForbidden, err)
	}
	return err
}

// buildEnvironmentServices cross-references real Services against the
// Ingresses erun-expose created (named "expose-<label>"), keyed by each
// Ingress's own backend Service reference -- the ground-truth link, not the
// naming convention every Service's ExposableLabel is derived from below.
// Split out as a pure function so it is testable without a kubectl process.
func buildEnvironmentServices(tenant string, services []ObservedService, ingresses []ObservedIngress) []EnvironmentService {
	exposed := indexExposedServicesByBackend(ingresses)
	prefix := TenantResourcePrefix(tenant) + "-"
	result := make([]EnvironmentService, 0, len(services))
	for _, svc := range services {
		entry := EnvironmentService{Name: svc.Name}
		for _, port := range svc.Ports {
			entry.Ports = append(entry.Ports, EnvironmentServicePort(port))
		}
		if exp, ok := exposed[svc.Name]; ok {
			entry.Exposed = true
			entry.Hostname = exp.Hostname
			entry.Scheme = exp.Scheme
		} else if label, ok := strings.CutPrefix(svc.Name, prefix); ok && label != "" {
			entry.ExposableLabel = label
		}
		result = append(result, entry)
	}
	return result
}

// indexExposedServicesByBackend maps every backend Service name a real
// erun-expose Ingress routes to, to that Ingress's own hostname/scheme.
func indexExposedServicesByBackend(ingresses []ObservedIngress) map[string]ExposedService {
	exposed := make(map[string]ExposedService, len(ingresses))
	for _, ing := range ingresses {
		label, ok := strings.CutPrefix(ing.Name, exposeIngressNamePrefix)
		if !ok || label == "" || len(ing.Hosts) == 0 {
			continue
		}
		exp := ExposedService{Service: label, Hostname: ing.Hosts[0], Scheme: ingressScheme(ing)}
		for _, backend := range ing.Backends {
			exposed[backend] = exp
		}
	}
	return exposed
}

// ingressScheme reports "https" when the Ingress's first host also appears
// in a TLS host group, else "http" -- mirrors filterExposedServices' own
// per-Ingress scheme resolution in expose_list.go.
func ingressScheme(ing ObservedIngress) string {
	for _, tls := range ing.TLS {
		for _, host := range tls.Hosts {
			if host == ing.Hosts[0] {
				return "https"
			}
		}
	}
	return "http"
}
