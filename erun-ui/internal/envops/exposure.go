package envops

import (
	"errors"
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/expose"
	"github.com/sophium/erun/erun-common/orchestration/clusterread"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// ExposureList: Configured false means exposure cannot apply, distinct from
// Restricted and from an empty list. NotConfiguredReason separates a host env
// from a project missing its platform: block.
type ExposureList struct {
	Configured          bool             `json:"configured"`
	Restricted          bool             `json:"restricted"`
	Error               string           `json:"error,omitempty"`
	Services            []ExposedService `json:"services"`
	NotConfiguredReason string           `json:"notConfiguredReason,omitempty"`
}

// The values of ExposureList.NotConfiguredReason.
const (
	ExposureNotConfiguredHostEnvironment = "host-environment"
	ExposureNotConfiguredNoPlatformBlock = "no-platform-block"
)

// ExposedService: TLSReady matters only for https.
type ExposedService struct {
	Service           string `json:"service"`
	Hostname          string `json:"hostname"`
	Scheme            string `json:"scheme"`
	BackendService    string `json:"backendService,omitempty"`
	TLSReady          bool   `json:"tlsReady,omitempty"`
	TLSNotReadyReason string `json:"tlsNotReadyReason,omitempty"`
}

// EnvironmentServiceList mirrors ExposureList's three states.
type EnvironmentServiceList struct {
	Configured          bool                 `json:"configured"`
	Restricted          bool                 `json:"restricted"`
	Error               string               `json:"error,omitempty"`
	Services            []EnvironmentService `json:"services"`
	NotConfiguredReason string               `json:"notConfiguredReason,omitempty"`
}

// EnvironmentService lists Ports so the picker can offer one.
type EnvironmentService struct {
	Name     string                   `json:"name"`
	Type     string                   `json:"type,omitempty"`
	Ports    []EnvironmentServicePort `json:"ports,omitempty"`
	Hostname string                   `json:"hostname,omitempty"`
	Scheme   string                   `json:"scheme,omitempty"`
	// ExposedAs is the hostname label, not necessarily the Service name.
	ExposedAs string `json:"exposedAs,omitempty"`
}

type EnvironmentServicePort struct {
	Name string `json:"name,omitempty"`
	Port int    `json:"port"`
}

// ExposeServiceInput: empty BackendService derives <tenant>-<service>.
type ExposeServiceInput struct {
	Service        string `json:"service"`
	BackendService string `json:"backendService,omitempty"`
	TargetIP       string `json:"targetIP"`
	Port           int    `json:"port,omitempty"`
}

// UnexposeResult names the DNS record removed.
type UnexposeResult struct {
	WildcardName string `json:"wildcardName"`
}

// ListEnvironmentExposures reports why exposure is not configured (host env vs
// no platform block) and Restricted when credentials cannot list.
func (s *Service) ListEnvironmentExposures(selection uimodel.Selection) (ExposureList, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("list environment exposures", selection.Tenant, selection.Environment); err != nil {
		return ExposureList{}, err
	}
	req, configured, notConfiguredReason, err := s.resolveExposureRequest(selection)
	if err != nil {
		return ExposureList{}, err
	}
	if !configured {
		return ExposureList{Services: []ExposedService{}, NotConfiguredReason: notConfiguredReason}, nil
	}
	services, err := clusterread.ListExposedServices(req)
	if err != nil {
		if errors.Is(err, clusterread.ErrListExposedServicesForbidden) {
			return ExposureList{Configured: true, Restricted: true, Services: []ExposedService{}}, nil
		}
		return ExposureList{Configured: true, Error: err.Error(), Services: []ExposedService{}}, nil
	}
	return ExposureList{Configured: true, Services: toExposedServices(services)}, nil
}

// ListEnvironmentServices lists Services and their existing public hostnames.
func (s *Service) ListEnvironmentServices(selection uimodel.Selection) (EnvironmentServiceList, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("list environment services", selection.Tenant, selection.Environment); err != nil {
		return EnvironmentServiceList{}, err
	}
	req, configured, notConfiguredReason, err := s.resolveExposureRequest(selection)
	if err != nil {
		return EnvironmentServiceList{}, err
	}
	if !configured {
		return EnvironmentServiceList{Services: []EnvironmentService{}, NotConfiguredReason: notConfiguredReason}, nil
	}
	services, err := expose.ListEnvironmentServices(req)
	if err != nil {
		if errors.Is(err, expose.ErrListEnvironmentServicesForbidden) {
			return EnvironmentServiceList{Configured: true, Restricted: true, Services: []EnvironmentService{}}, nil
		}
		return EnvironmentServiceList{Configured: true, Error: err.Error(), Services: []EnvironmentService{}}, nil
	}
	return EnvironmentServiceList{Configured: true, Services: toEnvironmentServices(services)}, nil
}

// ExposeEnvironmentService applies expose.RunExposeService directly, with no dry run.
func (s *Service) ExposeEnvironmentService(selection uimodel.Selection, input ExposeServiceInput) (ExposedService, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("expose environment service", selection.Tenant, selection.Environment); err != nil {
		return ExposedService{}, err
	}
	service := strings.TrimSpace(input.Service)
	targetIP := strings.TrimSpace(input.TargetIP)
	if service == "" || targetIP == "" {
		return ExposedService{}, fmt.Errorf("a service name and a target IP are required")
	}
	projectRoot := s.exposeProjectRoot()
	if !expose.ProjectHasExposablePlatform(projectRoot) {
		return ExposedService{}, fmt.Errorf("this environment's project has no platform block configured, so it cannot be exposed at a public hostname")
	}
	result, err := expose.RunExposeService(core.Context{}, expose.ExposeServiceParams{
		Tenant:         selection.Tenant,
		Environment:    selection.Environment,
		Service:        service,
		ProjectRoot:    projectRoot,
		TargetIP:       targetIP,
		ServicePort:    input.Port,
		BackendService: strings.TrimSpace(input.BackendService),
	}, s.d.Store, s.d.Store, s.d.Cloud, nil, nil)
	if err != nil {
		return ExposedService{}, err
	}
	return ExposedService{Service: result.Service, Hostname: result.Hostname, Scheme: result.Scheme}, nil
}

// UnexposeEnvironment removes the per-env wildcard DNS record, de-listing every
// service exposed under it; there is no per-service primitive.
func (s *Service) UnexposeEnvironment(selection uimodel.Selection) (UnexposeResult, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("unexpose environment", selection.Tenant, selection.Environment); err != nil {
		return UnexposeResult{}, err
	}
	projectRoot := s.exposeProjectRoot()
	if !expose.ProjectHasExposablePlatform(projectRoot) {
		return UnexposeResult{}, fmt.Errorf("this environment's project has no platform block configured")
	}
	result, err := expose.RunUnexposeService(core.Context{}, model.UnexposeParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
		ProjectRoot: projectRoot,
	}, s.d.Store, s.d.Store, s.d.Cloud, nil)
	if err != nil {
		return UnexposeResult{}, err
	}
	return UnexposeResult{WildcardName: result.WildcardName}, nil
}

// resolveExposureRequest checks a host environment first: it has no cluster.
func (s *Service) resolveExposureRequest(selection uimodel.Selection) (req configpkg.ShellLaunchParams, configured bool, notConfiguredReason string, err error) {
	config, _, err := s.d.Store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return configpkg.ShellLaunchParams{}, false, "", err
	}
	if config.Type == configpkg.EnvironmentTypeHost {
		return configpkg.ShellLaunchParams{}, false, ExposureNotConfiguredHostEnvironment, nil
	}
	req = configpkg.ShellLaunchParams{
		Tenant:            selection.Tenant,
		Environment:       selection.Environment,
		Namespace:         runtimetarget.KubernetesNamespaceName(selection.Tenant, selection.Environment),
		KubernetesContext: strings.TrimSpace(config.KubernetesContext),
	}
	if !expose.ProjectHasExposablePlatform(s.exposeProjectRoot()) {
		return req, false, ExposureNotConfiguredNoPlatformBlock, nil
	}
	return req, true, "", nil
}

// exposeProjectRoot returns "" when no project is found, which reads as not configured.
func (s *Service) exposeProjectRoot() string {
	_, root, err := s.d.FindProjectRoot()
	if err != nil {
		return ""
	}
	return root
}

func toExposedServices(services []clusterread.ExposedService) []ExposedService {
	result := make([]ExposedService, 0, len(services))
	for _, service := range services {
		result = append(result, ExposedService{
			Service:           service.Service,
			Hostname:          service.Hostname,
			Scheme:            service.Scheme,
			BackendService:    service.BackendService,
			TLSReady:          service.TLSReady,
			TLSNotReadyReason: service.TLSNotReadyReason,
		})
	}
	return result
}

func toEnvironmentServices(services []model.EnvironmentService) []EnvironmentService {
	result := make([]EnvironmentService, 0, len(services))
	for _, service := range services {
		entry := EnvironmentService{Name: service.Name, Type: service.Type}
		for _, port := range service.Ports {
			entry.Ports = append(entry.Ports, EnvironmentServicePort{Name: port.Name, Port: port.Port})
		}
		if service.Exposure != nil {
			entry.Hostname = service.Exposure.Hostname
			entry.Scheme = service.Exposure.Scheme
			entry.ExposedAs = service.Exposure.Label
		}
		result = append(result, entry)
	}
	return result
}
