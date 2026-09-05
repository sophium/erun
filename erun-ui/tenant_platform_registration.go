package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// tenant_platform_registration.go is the Registration tab's own surface: the
// hosted objects `erun platform` registers — cloud contexts and hosted
// environments — that a tenant/env created only in the desktop's local
// config has no automatic counterpart of on a hosted platform. It gives the
// desktop the same registration path `erun platform context create` /
// `erun platform provision` / `erun platform env register|deploy|stop|delete`
// already give the CLI, through the same eruncommon.PlatformClient the rest
// of the dashboard reads and writes through.
//
// `erun platform tenant create` and `erun platform user enroll` stay
// deliberately absent here: a tenant registration configures an OIDC issuer
// mapping (and, for a shared issuer, an org-claim pair) that the desktop has
// no safe way to guess at through a form, and the first tenant only ever
// comes from the platform's own bootstrap. NotEnrolledState (tenant_platform.go
// / TenantPlatformState.tsx) already gives user enrollment a self-service
// attempt plus an administrator hand-off; the Registration panel points at
// `erun platform tenant create` for the tenant case rather than
// half-implementing it.

// The write routes the Registration tab gates on, in the same canonical
// "METHOD /path" form tenant_dashboard.go's read routes use.
const (
	tenantDashboardWriteCreateContext = "POST /v1/contexts"
	// tenantDashboardWriteRegisterEnvironment also gates PreviewPlatformEnvironment
	// (register with preview:true): both hit this same POST /v1/environments
	// route, so a preview needs no separate capability from the register it
	// precedes.
	tenantDashboardWriteRegisterEnvironment = "POST /v1/environments"
	tenantDashboardWriteDeployEnvironment   = "POST /v1/environments/{environment_id}/deploy"
	tenantDashboardWriteStopEnvironment     = "POST /v1/environments/{environment_id}/stop"
	tenantDashboardWriteDeleteEnvironment   = "DELETE /v1/environments/{environment_id}"
)

const (
	actionCreateContext platformAction = "create a cloud context"
	actionPreviewEnv    platformAction = "preview registering a hosted environment"
	actionRegisterEnv   platformAction = "register a hosted environment"
	actionDeployEnv     platformAction = "deploy this environment"
	actionStopEnv       platformAction = "stop this environment"
	actionDeleteEnv     platformAction = "delete this environment"
)

// loadTenantDashboardRegistration resolves the Registration tab's two lists
// (contexts, environments) and the write capabilities its forms/buttons
// gate on. Each list degrades independently — a caller denied one, or whose
// read of one fails, still sees the other — and the tab itself is hidden
// only when the caller can neither read nor write anything registration-
// shaped at all.
func loadTenantDashboardRegistration(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, dashboard *uiTenantDashboard) {
	loadTenantDashboardRegistrationCapabilities(capabilities, dashboard)
	contextsRestricted := loadTenantDashboardContexts(ctx, client, capabilities, dashboard)
	environmentsRestricted := loadTenantDashboardEnvironments(ctx, client, capabilities, dashboard)

	panel := uiTenantDashboardPanel{Tab: tenantDashboardTabRegistration}
	nothingToRead := contextsRestricted != "" && environmentsRestricted != ""
	nothingToWrite := !dashboard.CanCreateContext && !dashboard.CanRegisterEnvironment
	if nothingToRead && nothingToWrite {
		panel.Restricted = contextsRestricted
	}
	dashboard.Panels = append(dashboard.Panels, panel)
}

func loadTenantDashboardRegistrationCapabilities(capabilities eruncommon.PlatformCapabilities, dashboard *uiTenantDashboard) {
	dashboard.CanCreateContext = restrictedTenantDashboardRead(capabilities, tenantDashboardWriteCreateContext) == ""
	dashboard.CanRegisterEnvironment = restrictedTenantDashboardRead(capabilities, tenantDashboardWriteRegisterEnvironment) == ""
	dashboard.CanDeployEnvironment = restrictedTenantDashboardRead(capabilities, tenantDashboardWriteDeployEnvironment) == ""
	dashboard.CanStopEnvironment = restrictedTenantDashboardRead(capabilities, tenantDashboardWriteStopEnvironment) == ""
	dashboard.CanDeleteEnvironment = restrictedTenantDashboardRead(capabilities, tenantDashboardWriteDeleteEnvironment) == ""
}

// loadTenantDashboardContexts resolves the contexts list (or its restriction/
// error) and returns the restriction, so the caller can fold it into the
// tab-level hide decision without re-deriving it.
func loadTenantDashboardContexts(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, dashboard *uiTenantDashboard) string {
	restricted := restrictedTenantDashboardRead(capabilities, tenantDashboardReadContexts)
	if restricted != "" {
		dashboard.ContextsRestricted = restricted
		return restricted
	}
	contexts, err := client.ListContexts(ctx)
	if err != nil {
		dashboard.ContextsError = tenantDashboardReadError(tenantDashboardReadContexts, err)
		return ""
	}
	dashboard.Contexts = uiPlatformContexts(contexts)
	return ""
}

// loadTenantDashboardEnvironments is loadTenantDashboardContexts's mirror for
// the environments list.
func loadTenantDashboardEnvironments(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, dashboard *uiTenantDashboard) string {
	restricted := restrictedTenantDashboardRead(capabilities, tenantDashboardReadEnvironments)
	if restricted != "" {
		dashboard.EnvironmentsRestricted = restricted
		return restricted
	}
	environments, err := client.ListEnvironments(ctx)
	if err != nil {
		dashboard.EnvironmentsError = tenantDashboardReadError(tenantDashboardReadEnvironments, err)
		return ""
	}
	dashboard.Environments = uiPlatformEnvironments(environments)
	return ""
}

func uiPlatformContexts(contexts []eruncommon.PlatformContext) []uiPlatformContext {
	converted := make([]uiPlatformContext, 0, len(contexts))
	for _, cloudContext := range contexts {
		converted = append(converted, uiPlatformContextFrom(cloudContext))
	}
	return converted
}

func uiPlatformContextFrom(cloudContext eruncommon.PlatformContext) uiPlatformContext {
	return uiPlatformContext{
		ContextID:          cloudContext.ContextID,
		Name:               cloudContext.Name,
		Provider:           cloudContext.Provider,
		CloudProviderAlias: cloudContext.CloudProviderAlias,
		Region:             cloudContext.Region,
		InstanceType:       cloudContext.InstanceType,
		KubernetesContext:  cloudContext.KubernetesContext,
		Status:             cloudContext.Status,
		ProvisionError:     cloudContext.ProvisionError,
	}
}

func uiPlatformEnvironments(environments []eruncommon.PlatformEnvironment) []uiPlatformEnvironment {
	converted := make([]uiPlatformEnvironment, 0, len(environments))
	for _, environment := range environments {
		converted = append(converted, uiPlatformEnvironmentFrom(environment))
	}
	return converted
}

func uiPlatformEnvironmentFrom(environment eruncommon.PlatformEnvironment) uiPlatformEnvironment {
	return uiPlatformEnvironment{
		EnvironmentID:     environment.EnvironmentID,
		Name:              environment.Name,
		Type:              environment.Type,
		ContextID:         environment.ContextID,
		KubernetesContext: environment.KubernetesContext,
		RuntimeVersion:    environment.RuntimeVersion,
		Status:            environment.Status,
		ProvisionError:    environment.ProvisionError,
		DeployedVersion:   environment.DeployedVersion,
		DeleteError:       environment.DeleteError,
	}
}

// platformOutcomeKind classifies a write's error into "conflict"/
// "unavailable" (an expected, actionable refusal the caller renders as a
// recoverable state, not a raw error — e.g. a quota cap, or a deploy already
// in flight) or "" (a genuine failure the caller returns as an error,
// through operatorPlatformError). platformActionMessage names the message a
// "conflict"/"unavailable" outcome carries, taken from the platform's own
// response body when one exists rather than the wrapped Go error text.
func platformOutcomeKind(err error) string {
	switch {
	case errors.Is(err, eruncommon.ErrPlatformConflict):
		return "conflict"
	case errors.Is(err, eruncommon.ErrPlatformNotImplemented):
		return "unavailable"
	default:
		return ""
	}
}

func platformActionMessage(err error) string {
	var statusErr *eruncommon.PlatformStatusError
	if errors.As(err, &statusErr) {
		if body := strings.TrimSpace(string(statusErr.Body)); body != "" {
			return body
		}
	}
	return err.Error()
}

// CreatePlatformContext registers a cloud context, or — with input.Preview
// set — only resolves and returns its bootstrap plan without creating
// anything, mirroring `erun platform context create [--preview]`.
func (a *App) CreatePlatformContext(input uiCreatePlatformContextInput) (uiPlatformContextOutcome, error) {
	tenant, err := requireTenant("creating a cloud context", input.Tenant)
	if err != nil {
		return uiPlatformContextOutcome{}, err
	}
	name := strings.TrimSpace(input.Name)
	alias := strings.TrimSpace(input.CloudProviderAlias)
	region := strings.TrimSpace(input.Region)
	if name == "" || alias == "" || region == "" {
		return uiPlatformContextOutcome{}, fmt.Errorf("context name, cloud provider alias, and region are required")
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client, requestCtx, cancel, err := a.tenantPlatformClient(ctx, tenant)
	if err != nil {
		return uiPlatformContextOutcome{}, err
	}
	defer cancel()

	result, err := client.CreateContext(requestCtx, eruncommon.PlatformCreateContextParams{
		Name:               name,
		CloudProviderAlias: alias,
		Region:             region,
		InstanceType:       strings.TrimSpace(input.InstanceType),
		DiskType:           strings.TrimSpace(input.DiskType),
		DiskSizeGB:         input.DiskSizeGB,
		Preview:            input.Preview,
	})
	if err != nil {
		if kind := platformOutcomeKind(err); kind != "" {
			return uiPlatformContextOutcome{Kind: kind, Message: platformActionMessage(err)}, nil
		}
		return uiPlatformContextOutcome{}, operatorPlatformError(actionCreateContext, err)
	}
	outcome := uiPlatformContextOutcome{Kind: "accepted", Plan: result.Plan}
	if result.Context != nil {
		createdContext := uiPlatformContextFrom(*result.Context)
		outcome.Context = &createdContext
	}
	return outcome, nil
}

// createEnvironmentParams validates and converts input, shared by
// PreviewPlatformEnvironment and RegisterPlatformEnvironment so the two can
// never diverge on what fields they send for the same operator input — the
// defect class of a preview that cannot model what submit does.
func createEnvironmentParams(input uiRegisterPlatformEnvironmentInput) (eruncommon.PlatformCreateEnvironmentParams, error) {
	name := strings.TrimSpace(input.Name)
	envType := strings.TrimSpace(input.Type)
	if name == "" || envType == "" {
		return eruncommon.PlatformCreateEnvironmentParams{}, fmt.Errorf("environment name and type are required")
	}
	return eruncommon.PlatformCreateEnvironmentParams{
		Name:              name,
		Type:              envType,
		ContextID:         strings.TrimSpace(input.ContextID),
		KubernetesContext: strings.TrimSpace(input.KubernetesContext),
		RuntimeVersion:    strings.TrimSpace(input.RuntimeVersion),
		Adopt:             input.Adopt,
	}, nil
}

// PreviewPlatformEnvironment resolves and returns the ordered plan the exact
// same input would submit through RegisterPlatformEnvironment, without
// creating anything. Always returns a plan (QuotaOk names whether it can
// actually register), never a conflict: this call creates nothing.
func (a *App) PreviewPlatformEnvironment(input uiRegisterPlatformEnvironmentInput) (uiPlatformProvisionResult, error) {
	tenant, err := requireTenant("previewing a hosted environment", input.Tenant)
	if err != nil {
		return uiPlatformProvisionResult{}, err
	}
	params, err := createEnvironmentParams(input)
	if err != nil {
		return uiPlatformProvisionResult{}, err
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client, requestCtx, cancel, err := a.tenantPlatformClient(ctx, tenant)
	if err != nil {
		return uiPlatformProvisionResult{}, err
	}
	defer cancel()

	result, err := client.PreviewCreateEnvironment(requestCtx, params)
	if err != nil {
		return uiPlatformProvisionResult{}, operatorPlatformError(actionPreviewEnv, err)
	}
	return uiPlatformProvisionResult{Plan: result.Plan, QuotaOk: result.QuotaOk}, nil
}

// RegisterPlatformEnvironment registers a hosted environment, mirroring
// `erun platform env register`, or — with input.Adopt set — records one that
// already exists without provisioning or deploying anything. A quota
// cap (the tenant's environment count limit) reports as Kind "conflict"
// naming the cap, not a raw error — the recoverable state the operator
// resolves by deleting or stopping another environment first.
func (a *App) RegisterPlatformEnvironment(input uiRegisterPlatformEnvironmentInput) (uiPlatformEnvironmentOutcome, error) {
	tenant, err := requireTenant("registering a hosted environment", input.Tenant)
	if err != nil {
		return uiPlatformEnvironmentOutcome{}, err
	}
	params, err := createEnvironmentParams(input)
	if err != nil {
		return uiPlatformEnvironmentOutcome{}, err
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client, requestCtx, cancel, err := a.tenantPlatformClient(ctx, tenant)
	if err != nil {
		return uiPlatformEnvironmentOutcome{}, err
	}
	defer cancel()

	environment, err := client.CreateEnvironment(requestCtx, params)
	if err != nil {
		if kind := platformOutcomeKind(err); kind != "" {
			return uiPlatformEnvironmentOutcome{Kind: kind, Message: platformActionMessage(err)}, nil
		}
		return uiPlatformEnvironmentOutcome{}, operatorPlatformError(actionRegisterEnv, err)
	}
	converted := uiPlatformEnvironmentFrom(environment)
	return uiPlatformEnvironmentOutcome{Kind: "accepted", Environment: &converted}, nil
}

// DeployPlatformEnvironment starts a server-side deploy of an
// already-registered runtime environment, mirroring `erun platform env
// deploy`. Reports a deploy already in flight (Kind "conflict") or no
// deploy executor configured (Kind "unavailable") as a recoverable state.
func (a *App) DeployPlatformEnvironment(input uiPlatformEnvironmentActionInput) (uiPlatformEnvironmentOutcome, error) {
	return a.platformEnvironmentAction(input, actionDeployEnv, func(client *eruncommon.PlatformClient, requestCtx context.Context, environmentID string) (eruncommon.PlatformEnvironment, error) {
		return client.DeployEnvironment(requestCtx, environmentID, eruncommon.PlatformDeployEnvironmentParams{Version: strings.TrimSpace(input.Version)})
	})
}

// StopPlatformEnvironment scales a runtime environment's Deployment to zero,
// mirroring `erun platform env stop`.
func (a *App) StopPlatformEnvironment(input uiPlatformEnvironmentActionInput) (uiPlatformEnvironmentOutcome, error) {
	return a.platformEnvironmentAction(input, actionStopEnv, func(client *eruncommon.PlatformClient, requestCtx context.Context, environmentID string) (eruncommon.PlatformEnvironment, error) {
		return client.StopEnvironment(requestCtx, environmentID)
	})
}

// DeletePlatformEnvironment starts tearing down a hosted environment's
// namespace and its row, mirroring `erun platform env delete`. The desktop
// asks for confirmation before calling this — see the Registration panel's
// delete control — the same boundary every other destructive dashboard
// action gets.
func (a *App) DeletePlatformEnvironment(input uiPlatformEnvironmentActionInput) (uiPlatformEnvironmentOutcome, error) {
	return a.platformEnvironmentAction(input, actionDeleteEnv, func(client *eruncommon.PlatformClient, requestCtx context.Context, environmentID string) (eruncommon.PlatformEnvironment, error) {
		return client.DeleteEnvironment(requestCtx, environmentID)
	})
}

// platformEnvironmentAction is Deploy/Stop/Delete's shared shape: resolve the
// tenant's platform client, validate the environment id, run the given call,
// and classify its error into a recoverable outcome or a real error.
func (a *App) platformEnvironmentAction(input uiPlatformEnvironmentActionInput, action platformAction, call func(client *eruncommon.PlatformClient, requestCtx context.Context, environmentID string) (eruncommon.PlatformEnvironment, error)) (uiPlatformEnvironmentOutcome, error) {
	tenant, err := requireTenant(string(action), input.Tenant)
	if err != nil {
		return uiPlatformEnvironmentOutcome{}, err
	}
	environmentID := strings.TrimSpace(input.EnvironmentID)
	if environmentID == "" {
		return uiPlatformEnvironmentOutcome{}, fmt.Errorf("environment id is required")
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client, requestCtx, cancel, err := a.tenantPlatformClient(ctx, tenant)
	if err != nil {
		return uiPlatformEnvironmentOutcome{}, err
	}
	defer cancel()

	environment, err := call(client, requestCtx, environmentID)
	if err != nil {
		if kind := platformOutcomeKind(err); kind != "" {
			return uiPlatformEnvironmentOutcome{Kind: kind, Message: platformActionMessage(err)}, nil
		}
		return uiPlatformEnvironmentOutcome{}, operatorPlatformError(action, err)
	}
	converted := uiPlatformEnvironmentFrom(environment)
	return uiPlatformEnvironmentOutcome{Kind: "accepted", Environment: &converted}, nil
}
