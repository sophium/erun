package tenant

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Tenant creation and user enrollment are deliberately absent: an issuer
// mapping cannot be guessed safely through a form.

// The write routes the Registration tab gates on, in the same canonical
// "METHOD /path" form dashboard.go's read routes use.
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
func loadTenantDashboardRegistration(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, dashboard *Dashboard) {
	loadTenantDashboardRegistrationCapabilities(capabilities, dashboard)
	contextsRestricted := loadTenantDashboardContexts(ctx, client, capabilities, dashboard)
	environmentsRestricted := loadTenantDashboardEnvironments(ctx, client, capabilities, dashboard)

	panel := DashboardPanel{Tab: tenantDashboardTabRegistration}
	nothingToRead := contextsRestricted != "" && environmentsRestricted != ""
	nothingToWrite := !dashboard.CanCreateContext && !dashboard.CanRegisterEnvironment
	if nothingToRead && nothingToWrite {
		panel.Restricted = contextsRestricted
	}
	dashboard.Panels = append(dashboard.Panels, panel)
}

func loadTenantDashboardRegistrationCapabilities(capabilities model.PlatformCapabilities, dashboard *Dashboard) {
	dashboard.CanCreateContext = RestrictedRead(capabilities, tenantDashboardWriteCreateContext) == ""
	dashboard.CanRegisterEnvironment = RestrictedRead(capabilities, tenantDashboardWriteRegisterEnvironment) == ""
	dashboard.CanDeployEnvironment = RestrictedRead(capabilities, tenantDashboardWriteDeployEnvironment) == ""
	dashboard.CanStopEnvironment = RestrictedRead(capabilities, tenantDashboardWriteStopEnvironment) == ""
	dashboard.CanDeleteEnvironment = RestrictedRead(capabilities, tenantDashboardWriteDeleteEnvironment) == ""
}

// loadTenantDashboardContexts returns the restriction for the tab-level hide
// decision.
func loadTenantDashboardContexts(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, dashboard *Dashboard) string {
	restricted := RestrictedRead(capabilities, tenantDashboardReadContexts)
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

func loadTenantDashboardEnvironments(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, dashboard *Dashboard) string {
	restricted := RestrictedRead(capabilities, tenantDashboardReadEnvironments)
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

func uiPlatformContexts(contexts []model.PlatformContext) []PlatformContext {
	converted := make([]PlatformContext, 0, len(contexts))
	for _, cloudContext := range contexts {
		converted = append(converted, uiPlatformContextFrom(cloudContext))
	}
	return converted
}

func uiPlatformContextFrom(cloudContext model.PlatformContext) PlatformContext {
	return PlatformContext{
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

func uiPlatformEnvironments(environments []model.PlatformEnvironment) []PlatformEnvironment {
	converted := make([]PlatformEnvironment, 0, len(environments))
	for _, environment := range environments {
		converted = append(converted, uiPlatformEnvironmentFrom(environment))
	}
	return converted
}

func uiPlatformEnvironmentFrom(environment model.PlatformEnvironment) PlatformEnvironment {
	return PlatformEnvironment{
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
func (s *Service) CreatePlatformContext(input CreatePlatformContextInput) (PlatformContextOutcome, error) {
	tenant, err := uimodel.RequireTenant("creating a cloud context", input.Tenant)
	if err != nil {
		return PlatformContextOutcome{}, err
	}
	name := strings.TrimSpace(input.Name)
	alias := strings.TrimSpace(input.CloudProviderAlias)
	region := strings.TrimSpace(input.Region)
	if name == "" || alias == "" || region == "" {
		return PlatformContextOutcome{}, fmt.Errorf("context name, cloud provider alias, and region are required")
	}

	ctx := s.d.Context()
	client, requestCtx, cancel, err := s.Client(ctx, tenant)
	if err != nil {
		return PlatformContextOutcome{}, err
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
			return PlatformContextOutcome{Kind: kind, Message: platformActionMessage(err)}, nil
		}
		return PlatformContextOutcome{}, operatorPlatformError(actionCreateContext, err)
	}
	outcome := PlatformContextOutcome{Kind: "accepted", Plan: result.Plan}
	if result.Context != nil {
		createdContext := uiPlatformContextFrom(*result.Context)
		outcome.Context = &createdContext
	}
	return outcome, nil
}

// createEnvironmentParams is shared so preview and register never send
// different fields for the same input.
func createEnvironmentParams(input RegisterPlatformEnvironmentInput) (model.PlatformCreateEnvironmentParams, error) {
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
func (s *Service) PreviewPlatformEnvironment(input RegisterPlatformEnvironmentInput) (PlatformProvisionResult, error) {
	tenant, err := uimodel.RequireTenant("previewing a hosted environment", input.Tenant)
	if err != nil {
		return PlatformProvisionResult{}, err
	}
	params, err := createEnvironmentParams(input)
	if err != nil {
		return PlatformProvisionResult{}, err
	}

	ctx := s.d.Context()
	client, requestCtx, cancel, err := s.Client(ctx, tenant)
	if err != nil {
		return PlatformProvisionResult{}, err
	}
	defer cancel()

	result, err := client.PreviewCreateEnvironment(requestCtx, params)
	if err != nil {
		return PlatformProvisionResult{}, operatorPlatformError(actionPreviewEnv, err)
	}
	return PlatformProvisionResult{Plan: result.Plan, QuotaOk: result.QuotaOk}, nil
}

// RegisterPlatformEnvironment registers, or with Adopt records, a hosted
// environment; a quota cap is a "conflict" outcome, not an error.
func (s *Service) RegisterPlatformEnvironment(input RegisterPlatformEnvironmentInput) (PlatformEnvironmentOutcome, error) {
	tenant, err := uimodel.RequireTenant("registering a hosted environment", input.Tenant)
	if err != nil {
		return PlatformEnvironmentOutcome{}, err
	}
	params, err := createEnvironmentParams(input)
	if err != nil {
		return PlatformEnvironmentOutcome{}, err
	}

	ctx := s.d.Context()
	client, requestCtx, cancel, err := s.Client(ctx, tenant)
	if err != nil {
		return PlatformEnvironmentOutcome{}, err
	}
	defer cancel()

	environment, err := client.CreateEnvironment(requestCtx, params)
	if err != nil {
		if kind := platformOutcomeKind(err); kind != "" {
			return PlatformEnvironmentOutcome{Kind: kind, Message: platformActionMessage(err)}, nil
		}
		return PlatformEnvironmentOutcome{}, operatorPlatformError(actionRegisterEnv, err)
	}
	converted := uiPlatformEnvironmentFrom(environment)
	return PlatformEnvironmentOutcome{Kind: "accepted", Environment: &converted}, nil
}

// DeployPlatformEnvironment starts a server-side deploy of a runtime environment.
func (s *Service) DeployPlatformEnvironment(input PlatformEnvironmentActionInput) (PlatformEnvironmentOutcome, error) {
	return s.platformEnvironmentAction(input, actionDeployEnv, func(client *platformclient.PlatformClient, requestCtx context.Context, environmentID string) (model.PlatformEnvironment, error) {
		return client.DeployEnvironment(requestCtx, environmentID, model.PlatformDeployEnvironmentParams{Version: strings.TrimSpace(input.Version)})
	})
}

// StopPlatformEnvironment scales a runtime environment to zero.
func (s *Service) StopPlatformEnvironment(input PlatformEnvironmentActionInput) (PlatformEnvironmentOutcome, error) {
	return s.platformEnvironmentAction(input, actionStopEnv, func(client *platformclient.PlatformClient, requestCtx context.Context, environmentID string) (model.PlatformEnvironment, error) {
		return client.StopEnvironment(requestCtx, environmentID)
	})
}

// DeletePlatformEnvironment tears down a hosted environment; the UI confirms first.
func (s *Service) DeletePlatformEnvironment(input PlatformEnvironmentActionInput) (PlatformEnvironmentOutcome, error) {
	return s.platformEnvironmentAction(input, actionDeleteEnv, func(client *platformclient.PlatformClient, requestCtx context.Context, environmentID string) (model.PlatformEnvironment, error) {
		return client.DeleteEnvironment(requestCtx, environmentID)
	})
}

func (s *Service) platformEnvironmentAction(input PlatformEnvironmentActionInput, action platformAction, call func(client *platformclient.PlatformClient, requestCtx context.Context, environmentID string) (model.PlatformEnvironment, error)) (PlatformEnvironmentOutcome, error) {
	tenant, err := uimodel.RequireTenant(string(action), input.Tenant)
	if err != nil {
		return PlatformEnvironmentOutcome{}, err
	}
	environmentID := strings.TrimSpace(input.EnvironmentID)
	if environmentID == "" {
		return PlatformEnvironmentOutcome{}, fmt.Errorf("environment id is required")
	}

	ctx := s.d.Context()
	client, requestCtx, cancel, err := s.Client(ctx, tenant)
	if err != nil {
		return PlatformEnvironmentOutcome{}, err
	}
	defer cancel()

	environment, err := call(client, requestCtx, environmentID)
	if err != nil {
		if kind := platformOutcomeKind(err); kind != "" {
			return PlatformEnvironmentOutcome{Kind: kind, Message: platformActionMessage(err)}, nil
		}
		return PlatformEnvironmentOutcome{}, operatorPlatformError(action, err)
	}
	converted := uiPlatformEnvironmentFrom(environment)
	return PlatformEnvironmentOutcome{Kind: "accepted", Environment: &converted}, nil
}
