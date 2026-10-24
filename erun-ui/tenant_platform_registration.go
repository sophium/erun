package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// Tenant creation and user enrollment are deliberately absent: an issuer
// mapping cannot be guessed safely through a form.

// The write routes the Registration tab gates on, in the same canonical
// "METHOD /path" form tenant_dashboard.go's read routes use.
const (
	tenantDashboardWriteCreateContext = "POST /v1/contexts"
	// tenantDashboardWriteRegisterEnvironment also gates preview, which hits the
	// same route.
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

// loadTenantDashboardRegistration degrades each list independently and hides
// the tab only when nothing is readable or writable.
func loadTenantDashboardRegistration(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, dashboard *uiTenantDashboard) {
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

func loadTenantDashboardRegistrationCapabilities(capabilities model.PlatformCapabilities, dashboard *uiTenantDashboard) {
	dashboard.CanCreateContext = restrictedTenantDashboardRead(capabilities, tenantDashboardWriteCreateContext) == ""
	dashboard.CanRegisterEnvironment = restrictedTenantDashboardRead(capabilities, tenantDashboardWriteRegisterEnvironment) == ""
	dashboard.CanDeployEnvironment = restrictedTenantDashboardRead(capabilities, tenantDashboardWriteDeployEnvironment) == ""
	dashboard.CanStopEnvironment = restrictedTenantDashboardRead(capabilities, tenantDashboardWriteStopEnvironment) == ""
	dashboard.CanDeleteEnvironment = restrictedTenantDashboardRead(capabilities, tenantDashboardWriteDeleteEnvironment) == ""
}

// loadTenantDashboardContexts returns the restriction for the tab-level hide
// decision.
func loadTenantDashboardContexts(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, dashboard *uiTenantDashboard) string {
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

func loadTenantDashboardEnvironments(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, dashboard *uiTenantDashboard) string {
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

func uiPlatformContexts(contexts []model.PlatformContext) []uiPlatformContext {
	converted := make([]uiPlatformContext, 0, len(contexts))
	for _, cloudContext := range contexts {
		converted = append(converted, uiPlatformContextFrom(cloudContext))
	}
	return converted
}

func uiPlatformContextFrom(cloudContext model.PlatformContext) uiPlatformContext {
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

func uiPlatformEnvironments(environments []model.PlatformEnvironment) []uiPlatformEnvironment {
	converted := make([]uiPlatformEnvironment, 0, len(environments))
	for _, environment := range environments {
		converted = append(converted, uiPlatformEnvironmentFrom(environment))
	}
	return converted
}

func uiPlatformEnvironmentFrom(environment model.PlatformEnvironment) uiPlatformEnvironment {
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

// platformOutcomeKind returns "conflict"/"unavailable" for recoverable
// refusals, or "" for a genuine failure.
func platformOutcomeKind(err error) string {
	switch {
	case errors.Is(err, platformclient.ErrPlatformConflict):
		return "conflict"
	case errors.Is(err, platformclient.ErrPlatformNotImplemented):
		return "unavailable"
	default:
		return ""
	}
}

func platformActionMessage(err error) string {
	var statusErr *platformclient.PlatformStatusError
	if errors.As(err, &statusErr) {
		if body := strings.TrimSpace(string(statusErr.Body)); body != "" {
			return body
		}
	}
	return err.Error()
}

// CreatePlatformContext registers a cloud context, or with Preview only
// returns its plan.
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

	result, err := client.CreateContext(requestCtx, model.PlatformCreateContextParams{
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

// createEnvironmentParams is shared so preview and register never send
// different fields for the same input.
func createEnvironmentParams(input uiRegisterPlatformEnvironmentInput) (model.PlatformCreateEnvironmentParams, error) {
	name := strings.TrimSpace(input.Name)
	envType := strings.TrimSpace(input.Type)
	if name == "" || envType == "" {
		return model.PlatformCreateEnvironmentParams{}, fmt.Errorf("environment name and type are required")
	}
	return model.PlatformCreateEnvironmentParams{
		Name:              name,
		Type:              envType,
		ContextID:         strings.TrimSpace(input.ContextID),
		KubernetesContext: strings.TrimSpace(input.KubernetesContext),
		RuntimeVersion:    strings.TrimSpace(input.RuntimeVersion),
		Adopt:             input.Adopt,
	}, nil
}

// PreviewPlatformEnvironment returns the plan without creating anything;
// QuotaOk reports whether register would succeed.
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

// RegisterPlatformEnvironment registers, or with Adopt records, a hosted
// environment; a quota cap is a "conflict" outcome, not an error.
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

// DeployPlatformEnvironment starts a server-side deploy of a runtime environment.
func (a *App) DeployPlatformEnvironment(input uiPlatformEnvironmentActionInput) (uiPlatformEnvironmentOutcome, error) {
	return a.platformEnvironmentAction(input, actionDeployEnv, func(client *platformclient.PlatformClient, requestCtx context.Context, environmentID string) (model.PlatformEnvironment, error) {
		return client.DeployEnvironment(requestCtx, environmentID, model.PlatformDeployEnvironmentParams{Version: strings.TrimSpace(input.Version)})
	})
}

// StopPlatformEnvironment scales a runtime environment to zero.
func (a *App) StopPlatformEnvironment(input uiPlatformEnvironmentActionInput) (uiPlatformEnvironmentOutcome, error) {
	return a.platformEnvironmentAction(input, actionStopEnv, func(client *platformclient.PlatformClient, requestCtx context.Context, environmentID string) (model.PlatformEnvironment, error) {
		return client.StopEnvironment(requestCtx, environmentID)
	})
}

// DeletePlatformEnvironment tears down a hosted environment; the UI confirms first.
func (a *App) DeletePlatformEnvironment(input uiPlatformEnvironmentActionInput) (uiPlatformEnvironmentOutcome, error) {
	return a.platformEnvironmentAction(input, actionDeleteEnv, func(client *platformclient.PlatformClient, requestCtx context.Context, environmentID string) (model.PlatformEnvironment, error) {
		return client.DeleteEnvironment(requestCtx, environmentID)
	})
}

func (a *App) platformEnvironmentAction(input uiPlatformEnvironmentActionInput, action platformAction, call func(client *platformclient.PlatformClient, requestCtx context.Context, environmentID string) (model.PlatformEnvironment, error)) (uiPlatformEnvironmentOutcome, error) {
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
