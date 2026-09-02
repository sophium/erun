package main

import (
	"errors"
	"fmt"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// exposure_app.go is the desktop half of the Ports tab's public-exposure
// surface: list an environment's active exposures, expose a new service at a
// public hostname, and remove the environment's public DNS record. It
// composes the shared expose/unexpose primitives in erun-common; it holds no
// expose/unexpose planning of its own.

// ListEnvironmentExposures powers the Ports tab's exposure list. Configured
// is false for either of two distinct reasons, named in NotConfiguredReason
// so the tab's empty state can tell them apart: a host environment has no
// pod and no cluster at all, so exposure can never apply; a cluster-backed
// environment whose project carries no platform block simply hasn't been set
// up for it yet. A listing the caller's Kubernetes credentials cannot make
// reports Restricted instead of a bare empty list.
func (a *App) ListEnvironmentExposures(selection uiSelection) (uiExposureList, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("list environment exposures", selection.Tenant, selection.Environment); err != nil {
		return uiExposureList{}, err
	}
	req, configured, notConfiguredReason, err := a.resolveExposureRequest(selection)
	if err != nil {
		return uiExposureList{}, err
	}
	if !configured {
		return uiExposureList{Services: []uiExposedService{}, NotConfiguredReason: notConfiguredReason}, nil
	}
	services, err := eruncommon.ListExposedServices(req)
	if err != nil {
		if errors.Is(err, eruncommon.ErrListExposedServicesForbidden) {
			return uiExposureList{Configured: true, Restricted: true, Services: []uiExposedService{}}, nil
		}
		return uiExposureList{Configured: true, Error: err.Error(), Services: []uiExposedService{}}, nil
	}
	return uiExposureList{Configured: true, Services: toUIExposedServices(services)}, nil
}

// ListEnvironmentServices powers the Ports tab's service picker: what the
// environment is actually running, and which of those already have a public
// hostname. It answers the question that used to have no answer anywhere in
// the product — exposing a service meant typing a name you had to know
// already, and knowing the naming convention the Ingress backend is derived
// from.
func (a *App) ListEnvironmentServices(selection uiSelection) (uiEnvironmentServiceList, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("list environment services", selection.Tenant, selection.Environment); err != nil {
		return uiEnvironmentServiceList{}, err
	}
	req, configured, notConfiguredReason, err := a.resolveExposureRequest(selection)
	if err != nil {
		return uiEnvironmentServiceList{}, err
	}
	if !configured {
		return uiEnvironmentServiceList{Services: []uiEnvironmentService{}, NotConfiguredReason: notConfiguredReason}, nil
	}
	services, err := eruncommon.ListEnvironmentServices(req)
	if err != nil {
		if errors.Is(err, eruncommon.ErrListEnvironmentServicesForbidden) {
			return uiEnvironmentServiceList{Configured: true, Restricted: true, Services: []uiEnvironmentService{}}, nil
		}
		return uiEnvironmentServiceList{Configured: true, Error: err.Error(), Services: []uiEnvironmentService{}}, nil
	}
	return uiEnvironmentServiceList{Configured: true, Services: toUIEnvironmentServices(services)}, nil
}

// ExposeEnvironmentService exposes one Service at a public hostname under the
// platform's services zone. See eruncommon.RunExposeService for the resolved
// plan; it is applied here directly (no dry-run switch -- the Ports tab form
// commits on submit, matching every other Manage dialog save action).
func (a *App) ExposeEnvironmentService(selection uiSelection, input uiExposeServiceInput) (uiExposedService, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("expose environment service", selection.Tenant, selection.Environment); err != nil {
		return uiExposedService{}, err
	}
	service := strings.TrimSpace(input.Service)
	targetIP := strings.TrimSpace(input.TargetIP)
	if service == "" || targetIP == "" {
		return uiExposedService{}, fmt.Errorf("a service name and a target IP are required")
	}
	projectRoot := a.exposeProjectRoot()
	if !eruncommon.ProjectHasExposablePlatform(projectRoot) {
		return uiExposedService{}, fmt.Errorf("this environment's project has no platform block configured, so it cannot be exposed at a public hostname")
	}
	result, err := eruncommon.RunExposeService(eruncommon.Context{}, eruncommon.ExposeServiceParams{
		Tenant:         selection.Tenant,
		Environment:    selection.Environment,
		Service:        service,
		ProjectRoot:    projectRoot,
		TargetIP:       targetIP,
		ServicePort:    input.Port,
		BackendService: strings.TrimSpace(input.BackendService),
	}, a.deps.store, nil, nil)
	if err != nil {
		return uiExposedService{}, err
	}
	return uiExposedService{Service: result.Service, Hostname: result.Hostname, Scheme: result.Scheme}, nil
}

// UnexposeEnvironment removes the environment's per-env wildcard DNS record --
// the DNS-side counterpart to every service exposed under it. It de-lists
// every hostname the Ports tab shows, not just one row: the wildcard record
// covers the whole environment, and there is no narrower primitive that
// removes a single service's route without it (see
// eruncommon.RunUnexposeService).
func (a *App) UnexposeEnvironment(selection uiSelection) (uiUnexposeResult, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("unexpose environment", selection.Tenant, selection.Environment); err != nil {
		return uiUnexposeResult{}, err
	}
	projectRoot := a.exposeProjectRoot()
	if !eruncommon.ProjectHasExposablePlatform(projectRoot) {
		return uiUnexposeResult{}, fmt.Errorf("this environment's project has no platform block configured")
	}
	result, err := eruncommon.RunUnexposeService(eruncommon.Context{}, eruncommon.UnexposeParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
		ProjectRoot: projectRoot,
	}, a.deps.store, nil)
	if err != nil {
		return uiUnexposeResult{}, err
	}
	return uiUnexposeResult{WildcardName: result.WildcardName}, nil
}

// resolveExposureRequest resolves the env's namespace/context (for listing
// its Ingresses), whether it is configured for exposure at all, and -- when
// it isn't -- which of the two distinct reasons applies. A host environment
// is checked first and always wins: it has no cluster to check a platform
// block against, so that reason takes priority over the project's config.
func (a *App) resolveExposureRequest(selection uiSelection) (req eruncommon.ShellLaunchParams, configured bool, notConfiguredReason string, err error) {
	config, _, err := a.deps.store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return eruncommon.ShellLaunchParams{}, false, "", err
	}
	if config.Type == eruncommon.EnvironmentTypeHost {
		return eruncommon.ShellLaunchParams{}, false, uiExposureNotConfiguredHostEnvironment, nil
	}
	req = eruncommon.ShellLaunchParams{
		Tenant:            selection.Tenant,
		Environment:       selection.Environment,
		Namespace:         eruncommon.KubernetesNamespaceName(selection.Tenant, selection.Environment),
		KubernetesContext: strings.TrimSpace(config.KubernetesContext),
	}
	if !eruncommon.ProjectHasExposablePlatform(a.exposeProjectRoot()) {
		return req, false, uiExposureNotConfiguredNoPlatformBlock, nil
	}
	return req, true, "", nil
}

// exposeProjectRoot resolves the currently open project, the same way every
// other command that reads a project's platform block does. Empty (never an
// error) when no project can be found -- that reads as "not configured" here,
// exactly as it does for a project with no platform block at all.
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

func toUIExposedServices(services []eruncommon.ExposedService) []uiExposedService {
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

func toUIEnvironmentServices(services []eruncommon.EnvironmentService) []uiEnvironmentService {
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
