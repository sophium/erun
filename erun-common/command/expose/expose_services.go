package expose

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/orchestration/clusterread"
)

// EnvironmentService is one Service in an environment's namespace with its
// ports and any existing exposure.
type EnvironmentService struct {
	Name     string                            `json:"name"`
	Type     string                            `json:"type,omitempty"`
	Ports    []clusterread.ObservedServicePort `json:"ports,omitempty"`
	Exposure *ServiceExposure                  `json:"exposure,omitempty"`
}

// ServiceExposure is an EnvironmentService's existing exposure; Label need not
// be the Service's name.
type ServiceExposure struct {
	Label    string `json:"label"`
	Hostname string `json:"hostname"`
	Scheme   string `json:"scheme"`
}

// EnvironmentServiceList is the shared CLI/MCP result of a services read.
type EnvironmentServiceList struct {
	Tenant      string               `json:"tenant"`
	Environment string               `json:"environment"`
	Namespace   string               `json:"namespace"`
	Services    []EnvironmentService `json:"services"`
}

// ErrListEnvironmentServicesForbidden reports that the caller cannot list the namespace's Services.
var ErrListEnvironmentServicesForbidden = errors.New("list environment services: forbidden")

// RunListEnvironmentServices traces both `kubectl get`s up front because
// ListEnvironmentServices, the desktop's call, has no Context to trace with.
func RunListEnvironmentServices(ctx core.Context, req config.ShellLaunchParams) (EnvironmentServiceList, error) {
	result := EnvironmentServiceList{Tenant: req.Tenant, Environment: req.Environment, Namespace: req.Namespace}
	ctx.TraceCommand("", "kubectl", clusterread.ObserveGetArgs(req, "service")...)
	ctx.TraceCommand("", "kubectl", clusterread.ObserveGetArgs(req, "ingress")...)
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

// ListEnvironmentServices runs only plain `kubectl get`s, so it is safe for
// callers that must never get `erun exec raw`.
func ListEnvironmentServices(req config.ShellLaunchParams) ([]EnvironmentService, error) {
	services, err := clusterread.FetchObservedServices(clusterread.ObserveGetArgs(req, "service"))
	if err != nil {
		if clusterread.IsForbiddenKubectlError(err) {
			return nil, fmt.Errorf("%w: %w", ErrListEnvironmentServicesForbidden, err)
		}
		return nil, err
	}
	ingresses, err := clusterread.FetchObservedIngresses(clusterread.ObserveGetArgs(req, "ingress"))
	if err != nil {
		if clusterread.IsForbiddenKubectlError(err) {
			return nil, fmt.Errorf("%w: %w", ErrListEnvironmentServicesForbidden, err)
		}
		return nil, err
	}
	return mergeServicesWithExposures(services, ingresses), nil
}

// mergeServicesWithExposures reads the backend from the Ingress rather than
// re-deriving it from the naming convention a repo-native chart breaks.
func mergeServicesWithExposures(services []clusterread.ObservedService, ingresses []clusterread.ObservedIngress) []EnvironmentService {
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

func exposuresByBackendService(ingresses []clusterread.ObservedIngress) map[string]ServiceExposure {
	tlsSecretByHost := clusterread.TlsSecretsByHost(ingresses)
	byService := make(map[string]ServiceExposure, len(ingresses))
	for _, ing := range ingresses {
		label, ok := strings.CutPrefix(ing.Name, clusterread.ExposeIngressNamePrefix)
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
