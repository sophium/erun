package main

import (
	"errors"
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/expose"
	"github.com/sophium/erun/erun-common/orchestration/clusterread"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// ListEnvironmentExposures reports why exposure is not configured (host env vs
// no platform block) and Restricted when credentials cannot list.
func (a *App) ListEnvironmentExposures(selection uimodel.Selection) (uiExposureList, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("list environment exposures", selection.Tenant, selection.Environment); err != nil {
		return uiExposureList{}, err
	}
	req, configured, notConfiguredReason, err := a.resolveExposureRequest(selection)
	if err != nil {
		return uiExposureList{}, err
	}
	if !configured {
		return uiExposureList{Services: []uiExposedService{}, NotConfiguredReason: notConfiguredReason}, nil
	}
	services, err := clusterread.ListExposedServices(req)
	if err != nil {
		if errors.Is(err, clusterread.ErrListExposedServicesForbidden) {
			return uiExposureList{Configured: true, Restricted: true, Services: []uiExposedService{}}, nil
		}
		return uiExposureList{Configured: true, Error: err.Error(), Services: []uiExposedService{}}, nil
	}
	return uiExposureList{Configured: true, Services: toUIExposedServices(services)}, nil
}

// ListEnvironmentServices lists Services and their existing public hostnames.
func (a *App) ListEnvironmentServices(selection uimodel.Selection) (uiEnvironmentServiceList, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("list environment services", selection.Tenant, selection.Environment); err != nil {
		return uiEnvironmentServiceList{}, err
	}
	req, configured, notConfiguredReason, err := a.resolveExposureRequest(selection)
	if err != nil {
		return uiEnvironmentServiceList{}, err
	}
	if !configured {
		return uiEnvironmentServiceList{Services: []uiEnvironmentService{}, NotConfiguredReason: notConfiguredReason}, nil
	}
	services, err := expose.ListEnvironmentServices(req)
	if err != nil {
		if errors.Is(err, expose.ErrListEnvironmentServicesForbidden) {
			return uiEnvironmentServiceList{Configured: true, Restricted: true, Services: []uiEnvironmentService{}}, nil
		}
		return uiEnvironmentServiceList{Configured: true, Error: err.Error(), Services: []uiEnvironmentService{}}, nil
	}
	return uiEnvironmentServiceList{Configured: true, Services: toUIEnvironmentServices(services)}, nil
}

// ExposeEnvironmentService applies expose.RunExposeService directly, with no dry run.
func (a *App) ExposeEnvironmentService(selection uimodel.Selection, input uiExposeServiceInput) (uiExposedService, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("expose environment service", selection.Tenant, selection.Environment); err != nil {
		return uiExposedService{}, err
	}
	service := strings.TrimSpace(input.Service)
	targetIP := strings.TrimSpace(input.TargetIP)
	if service == "" || targetIP == "" {
		return uiExposedService{}, fmt.Errorf("a service name and a target IP are required")
	}
	projectRoot := a.exposeProjectRoot()
	if !expose.ProjectHasExposablePlatform(projectRoot) {
		return uiExposedService{}, fmt.Errorf("this environment's project has no platform block configured, so it cannot be exposed at a public hostname")
	}
	result, err := expose.RunExposeService(core.Context{}, expose.ExposeServiceParams{
		Tenant:         selection.Tenant,
		Environment:    selection.Environment,
		Service:        service,
		ProjectRoot:    projectRoot,
		TargetIP:       targetIP,
		ServicePort:    input.Port,
		BackendService: strings.TrimSpace(input.BackendService),
	}, a.deps.store, a.deps.store, a.deps.cloudDeps, nil, nil)
	if err != nil {
		return uiExposedService{}, err
	}
	return uiExposedService{Service: result.Service, Hostname: result.Hostname, Scheme: result.Scheme}, nil
}

// UnexposeEnvironment removes the per-env wildcard DNS record, de-listing every
// service exposed under it; there is no per-service primitive.
func (a *App) UnexposeEnvironment(selection uimodel.Selection) (uiUnexposeResult, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("unexpose environment", selection.Tenant, selection.Environment); err != nil {
		return uiUnexposeResult{}, err
	}
	projectRoot := a.exposeProjectRoot()
	if !expose.ProjectHasExposablePlatform(projectRoot) {
		return uiUnexposeResult{}, fmt.Errorf("this environment's project has no platform block configured")
	}
	result, err := expose.RunUnexposeService(core.Context{}, model.UnexposeParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
		ProjectRoot: projectRoot,
	}, a.deps.store, a.deps.store, a.deps.cloudDeps, nil)
	if err != nil {
		return uiUnexposeResult{}, err
	}
	return uiUnexposeResult{WildcardName: result.WildcardName}, nil
}

// resolveExposureRequest checks a host environment first: it has no cluster.
func (a *App) resolveExposureRequest(selection uimodel.Selection) (req configpkg.ShellLaunchParams, configured bool, notConfiguredReason string, err error) {
	config, _, err := a.deps.store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return configpkg.ShellLaunchParams{}, false, "", err
	}
	if config.Type == configpkg.EnvironmentTypeHost {
		return configpkg.ShellLaunchParams{}, false, uiExposureNotConfiguredHostEnvironment, nil
	}
	req = configpkg.ShellLaunchParams{
		Tenant:            selection.Tenant,
		Environment:       selection.Environment,
		Namespace:         configpkg.KubernetesNamespaceName(selection.Tenant, selection.Environment),
		KubernetesContext: strings.TrimSpace(config.KubernetesContext),
	}
	if !expose.ProjectHasExposablePlatform(a.exposeProjectRoot()) {
		return req, false, uiExposureNotConfiguredNoPlatformBlock, nil
	}
	return req, true, "", nil
}

// exposeProjectRoot returns "" when no project is found, which reads as not configured.
func (a *App) exposeProjectRoot() string {
	if a.deps.findProjectRoot == nil {
		return ""
	}
	_, root, err := a.deps.findProjectRoot()
	if err != nil {
		return ""
	}
	return root
}

func toUIExposedServices(services []clusterread.ExposedService) []uiExposedService {
	result := make([]uiExposedService, 0, len(services))
	for _, service := range services {
		result = append(result, uiExposedService{
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

func toUIEnvironmentServices(services []model.EnvironmentService) []uiEnvironmentService {
	result := make([]uiEnvironmentService, 0, len(services))
	for _, service := range services {
		entry := uiEnvironmentService{Name: service.Name, Type: service.Type}
		for _, port := range service.Ports {
			entry.Ports = append(entry.Ports, uiEnvironmentServicePort{Name: port.Name, Port: port.Port})
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
