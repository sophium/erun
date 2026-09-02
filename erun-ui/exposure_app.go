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

// ListEnvironmentExposures powers the Ports tab's Public access section: every
// Service the environment is actually running, not just the ones already
// exposed (issue #1906) -- so the picker below has a real Service to offer
// instead of a name the operator has to already know. Configured is false for
// either of two distinct reasons, named in NotConfiguredReason so the tab's
// empty state can tell them apart: a host environment has no pod and no
// cluster at all, so exposure can never apply; a cluster-backed environment
// whose project carries no platform block simply hasn't been set up for it
// yet. A listing the caller's Kubernetes credentials cannot make reports
// Restricted instead of a bare empty list.
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
		return uiExposureList{Services: []uiEnvironmentService{}, NotConfiguredReason: notConfiguredReason}, nil
	}
	defaultTargetIP := a.exposeDefaultTargetIP(req.KubernetesContext)
	services, err := eruncommon.ListEnvironmentServices(eruncommon.Context{}, req, selection.Tenant)
	if err != nil {
		if errors.Is(err, eruncommon.ErrListEnvironmentServicesForbidden) {
			return uiExposureList{Configured: true, Restricted: true, Services: []uiEnvironmentService{}, DefaultTargetIP: defaultTargetIP}, nil
		}
		return uiExposureList{Configured: true, Error: err.Error(), Services: []uiEnvironmentService{}, DefaultTargetIP: defaultTargetIP}, nil
	}
	return uiExposureList{Configured: true, Services: toUIEnvironmentServices(services), DefaultTargetIP: defaultTargetIP}, nil
}

// PreviewExposeEnvironmentService resolves the hostname/scheme
// ExposeEnvironmentService would produce for input, without applying
// anything -- issue #1906's "see the hostname it will get before committing".
// It runs the exact same primitive forced into dry-run, so the preview can
// never drift from what a real expose would actually do.
func (a *App) PreviewExposeEnvironmentService(selection uiSelection, input uiExposeServiceInput) (uiExposePreview, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("preview expose environment service", selection.Tenant, selection.Environment); err != nil {
		return uiExposePreview{}, err
	}
	result, err := a.runExposeEnvironmentService(eruncommon.Context{DryRun: true}, selection, input)
	if err != nil {
		return uiExposePreview{}, err
	}
	return uiExposePreview{
		Hostname:          result.Hostname,
		Scheme:            result.Scheme,
		TLSEnabled:        result.TLSEnabled,
		TLSDisabledReason: result.TLSDisabledReason,
	}, nil
}

// ExposeEnvironmentService exposes one Service at a public hostname under the
// platform's services zone. See eruncommon.RunExposeService for the resolved
// plan; it is applied here directly (no dry-run switch -- the Ports tab form
// commits on submit, matching every other Manage dialog save action).
func (a *App) ExposeEnvironmentService(selection uiSelection, input uiExposeServiceInput) (uiExposeServiceResult, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("expose environment service", selection.Tenant, selection.Environment); err != nil {
		return uiExposeServiceResult{}, err
	}
	result, err := a.runExposeEnvironmentService(eruncommon.Context{}, selection, input)
	if err != nil {
		return uiExposeServiceResult{}, err
	}
	return uiExposeServiceResult{Hostname: result.Hostname, Scheme: result.Scheme}, nil
}

// runExposeEnvironmentService is the shared resolution behind
// ExposeEnvironmentService and its dry-run preview -- one place validating
// the form, so the preview can never resolve a plan the real call would
// reject.
func (a *App) runExposeEnvironmentService(ctx eruncommon.Context, selection uiSelection, input uiExposeServiceInput) (eruncommon.ExposeServiceResult, error) {
	service := strings.TrimSpace(input.Service)
	targetIP := strings.TrimSpace(input.TargetIP)
	if service == "" || targetIP == "" {
		return eruncommon.ExposeServiceResult{}, fmt.Errorf("a service name and a target IP are required")
	}
	projectRoot := a.exposeProjectRoot()
	if !eruncommon.ProjectHasExposablePlatform(projectRoot) {
		return eruncommon.ExposeServiceResult{}, fmt.Errorf("this environment's project has no platform block configured, so it cannot be exposed at a public hostname")
	}
	return eruncommon.RunExposeService(ctx, eruncommon.ExposeServiceParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
		Service:     service,
		ProjectRoot: projectRoot,
		TargetIP:    targetIP,
		ServicePort: input.Port,
	}, a.deps.store, nil, nil)
}

// exposeDefaultTargetIP prefills the Target IP field for a local cluster --
// one whose kubernetes context matches no registered cloud context, the same
// signal deploy's own cloud-metadata resolution uses (see
// applyCloudProviderDeployMetadata's "a local cluster, say" case in
// erun-common/deploy.go). Best-effort: an unreadable cloud-context list
// leaves the field empty rather than failing the whole listing over a
// convenience default.
func (a *App) exposeDefaultTargetIP(kubernetesContext string) string {
	kubernetesContext = strings.TrimSpace(kubernetesContext)
	if kubernetesContext == "" {
		return ""
	}
	contexts, err := eruncommon.ListCloudContexts(a.deps.store)
	if err != nil {
		return ""
	}
	for _, context := range contexts {
		context = eruncommon.NormalizeCloudContextConfig(context)
		if strings.TrimSpace(context.KubernetesContext) == kubernetesContext || strings.TrimSpace(context.Name) == kubernetesContext {
			return ""
		}
	}
	return "127.0.0.1"
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

func toUIEnvironmentServices(services []eruncommon.EnvironmentService) []uiEnvironmentService {
	result := make([]uiEnvironmentService, 0, len(services))
	for _, service := range services {
		ports := make([]uiEnvironmentServicePort, 0, len(service.Ports))
		for _, port := range service.Ports {
			ports = append(ports, uiEnvironmentServicePort{Name: port.Name, Port: port.Port, Protocol: port.Protocol})
		}
		result = append(result, uiEnvironmentService{
			Name:           service.Name,
			Ports:          ports,
			Exposed:        service.Exposed,
			Hostname:       service.Hostname,
			Scheme:         service.Scheme,
			ExposableLabel: service.ExposableLabel,
		})
	}
	return result
}
