package routes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// createEnvironmentRequest carries operator-authored fields. The tenant comes from
// the token; TenantID is honoured only for an operations caller (403 otherwise).
type createEnvironmentRequest struct {
	Name              string `json:"name"`
	Type              string `json:"type"`
	ContextID         string `json:"contextId"`
	KubernetesContext string `json:"kubernetesContext"`
	RuntimeVersion    string `json:"runtimeVersion"`
	Preview           bool   `json:"preview"`
	// Adopt registers an environment that already exists instead of provisioning one.
	Adopt    bool   `json:"adopt,omitempty"`
	TenantID string `json:"tenantId,omitempty"`
}

// validNamespaceLabel reports whether s is a DNS-1123 label safe to use as the
// env component of the <tenant>-<env> runtime namespace.
func validNamespaceLabel(s string) bool {
	if s == "" || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, r := range s {
		if !namespaceLabelRune(r) {
			return false
		}
	}
	return true
}

func namespaceLabelRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
}

var validEnvironmentTypes = map[model.EnvironmentType]struct{}{
	model.EnvironmentTypeRuntime:     {},
	model.EnvironmentTypeRemoteAgent: {},
	model.EnvironmentTypeLocalAgent:  {},
}

// decodeCreateEnvironmentInput returns the environment, preview, adopt, and the
// raw requested tenant ID; its error message is the operator-facing 400 reason.
func decodeCreateEnvironmentInput(req *http.Request) (model.Environment, bool, bool, string, error) {
	var body createEnvironmentRequest
	if err := decodeJSON(req, &body); err != nil {
		return model.Environment{}, false, false, "", errors.New("invalid request body")
	}
	name := strings.TrimSpace(body.Name)
	// The tenant is hyphen-free, so the first hyphen of <tenant>-<env> stays the split.
	if !validNamespaceLabel(name) {
		return model.Environment{}, false, false, "", errors.New("name must be a DNS-1123 label: lowercase letters, digits, and internal hyphens, not starting or ending with a hyphen, at most 63 characters")
	}
	envType := model.EnvironmentType(strings.TrimSpace(body.Type))
	if _, ok := validEnvironmentTypes[envType]; !ok {
		return model.Environment{}, false, false, "", errors.New("type must be one of runtime, remote-agent, local-agent")
	}
	environment := model.Environment{
		Name:              name,
		Type:              envType,
		ContextID:         strings.TrimSpace(body.ContextID),
		KubernetesContext: strings.TrimSpace(body.KubernetesContext),
		RuntimeVersion:    strings.TrimSpace(body.RuntimeVersion),
	}
	if err := validateAdoptInput(body.Adopt, environment); err != nil {
		return model.Environment{}, false, false, "", err
	}
	return environment, body.Preview, body.Adopt, strings.TrimSpace(body.TenantID), nil
}

// validateAdoptInput requires kubernetesContext and refuses anything that would
// trigger a deploy (runtimeVersion) or platform placement (contextId).
func validateAdoptInput(adopt bool, environment model.Environment) error {
	if !adopt {
		return nil
	}
	if environment.KubernetesContext == "" {
		return errors.New("kubernetesContext is required when adopting an existing environment")
	}
	if environment.RuntimeVersion != "" {
		return errors.New("runtimeVersion is not allowed when adopting an existing environment: adopting never starts a deploy")
	}
	if environment.ContextID != "" {
		return errors.New("contextId is not allowed when adopting an existing environment: adopt records a raw kubernetesContext instead")
	}
	return nil
}

// resolveCreateEnvironmentTenantScope writes the response itself on failure.
func (r EnvironmentRoutes) resolveCreateEnvironmentTenantScope(w http.ResponseWriter, req *http.Request, requestedTenantID string) (homeCtx, scopedCtx context.Context, targetTenantID string, crossTenant, ok bool) {
	securityContext, has := security.FromContext(req.Context())
	if !has {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), errors.New("security context not found in request"))
		return nil, nil, "", false, false
	}
	targetTenantID, err := resolveTargetTenant(securityContext, requestedTenantID)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return nil, nil, "", false, false
	}
	homeCtx = req.Context()
	return homeCtx, scopedContextForTenant(homeCtx, securityContext, targetTenantID), targetTenantID, targetTenantID != securityContext.TenantID, true
}

// resolveCreateEnvironmentPlacement skips adopt requests: they are never deployed,
// and placement would refuse the raw kubernetesContext adopt requires.
func (r EnvironmentRoutes) resolveCreateEnvironmentPlacement(w http.ResponseWriter, req *http.Request, environment *model.Environment, adopt bool) (resolvedPlacement, bool) {
	if adopt {
		return resolvedPlacement{}, true
	}
	placement, err := r.resolvePlacement(req.Context(), *environment)
	if err != nil {
		writePlacementError(w, req, err)
		return resolvedPlacement{}, false
	}
	environment.ContextID = placement.ContextID
	return placement, true
}

// enforceCreateEnvironmentResourceQuota skips adopted environments, which use no
// platform-managed capacity.
func (r EnvironmentRoutes) enforceCreateEnvironmentResourceQuota(w http.ResponseWriter, req *http.Request, environment model.Environment, adopt bool, quota model.TenantQuota) bool {
	if adopt || environment.Type != model.EnvironmentTypeRuntime {
		return true
	}
	if err := r.enforceRuntimeResourceQuotaOnCreate(req.Context(), quota); err != nil {
		var exceeded *resourceQuotaExceededError
		if errors.As(err, &exceeded) {
			writeError(w, http.StatusConflict, exceeded.Error())
			return false
		}
		writeRepositoryError(w, req, err)
		return false
	}
	return true
}

func (r EnvironmentRoutes) createEnvironment(w http.ResponseWriter, req *http.Request) {
	environment, preview, adopt, requestedTenantID, err := decodeCreateEnvironmentInput(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	homeCtx, scopedCtx, targetTenantID, crossTenant, ok := r.resolveCreateEnvironmentTenantScope(w, req, requestedTenantID)
	if !ok {
		return
	}
	req = req.WithContext(scopedCtx)
	placement, ok := r.resolveCreateEnvironmentPlacement(w, req, &environment, adopt)
	if !ok {
		return
	}

	count, quota, err := environmentQuotaUsage(req.Context(), r.environments, r.quotas)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	if preview {
		r.previewCreateEnvironment(w, req, environment, count, quota, adopt)
		return
	}
	if count >= quota.MaxEnvironments {
		writeError(w, http.StatusConflict, fmt.Sprintf("environment quota reached: this tenant already has %d of %d environments", count, quota.MaxEnvironments))
		return
	}
	if !r.enforceCreateEnvironmentResourceQuota(w, req, environment, adopt, quota) {
		return
	}
	r.persistAndMaybeProvision(w, req, environment, placement, homeCtx, targetTenantID, crossTenant)
}

// createEnvironmentRow routes a cross-tenant write through the admin creator so
// it is audited.
func (r EnvironmentRoutes) createEnvironmentRow(scopedCtx, homeCtx context.Context, targetTenantID string, crossTenant bool, environment model.Environment) (model.Environment, error) {
	if !crossTenant {
		return r.environments.Create(scopedCtx, environment)
	}
	if r.admin == nil {
		return model.Environment{}, errors.New("creating an environment in another tenant requires cross-tenant administration, which is not configured on this control plane")
	}
	return r.admin.CreateForTenant(scopedCtx, homeCtx, targetTenantID, environment)
}

// persistAndMaybeProvision relies on the ContextID FK to reject another tenant's context.
func (r EnvironmentRoutes) persistAndMaybeProvision(w http.ResponseWriter, req *http.Request, environment model.Environment, placement resolvedPlacement, homeCtx context.Context, targetTenantID string, crossTenant bool) {
	created, err := r.createEnvironmentRow(req.Context(), homeCtx, targetTenantID, crossTenant, environment)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}

	// A runtime env with a pinned version and a provisioner deploys server-side (202);
	// otherwise the row is registered config only (201).
	if r.provisioner == nil || environment.Type != model.EnvironmentTypeRuntime || created.RuntimeVersion == "" {
		writeJSON(w, http.StatusCreated, created)
		return
	}
	if err := r.startProvisioning(req.Context(), created, placement); err != nil {
		writeStartProvisioningError(w, req.Context(), err)
		return
	}
	writeJSON(w, http.StatusAccepted, created)
}

// writeStartProvisioningError needs no unwinding: the row stays registered.
func writeStartProvisioningError(w http.ResponseWriter, ctx context.Context, err error) {
	logServerErrorForRoute(ctx, "POST /v1/environments", err)
	writeError(w, http.StatusInternalServerError, "failed to start provisioning")
}

// previewCreateEnvironment uses adoptPlan for adopt because provisionPlan always
// renders a deploy step. Quota is reported, not 409'd, so the full plan shows.
func (r EnvironmentRoutes) previewCreateEnvironment(w http.ResponseWriter, req *http.Request, environment model.Environment, count int, quota model.TenantQuota, adopt bool) {
	tenant, err := r.tenants.Current(req.Context())
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	tenantName := strings.TrimSpace(tenant.Name)
	if adopt {
		writeJSON(w, http.StatusOK, provisionResponse{
			Plan:    adoptPlan(tenantName, environment.Name, environment.Type, environment.KubernetesContext, count, quota),
			QuotaOk: count < quota.MaxEnvironments,
		})
		return
	}
	runtimeCount, err := r.environments.CountByType(req.Context(), model.EnvironmentTypeRuntime)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	plan := provisionPlanInput{
		tenantName:        tenantName,
		envName:           environment.Name,
		envType:           environment.Type,
		kubernetesContext: environment.ContextID,
		count:             count,
		runtimeCount:      runtimeCount,
		quota:             quota,
	}
	writeJSON(w, http.StatusOK, provisionResponse{Plan: provisionPlan(plan), QuotaOk: plan.quotaOk()})
}

// startProvisioning is keyed by the environment so a retried create never
// double-deploys.
func (r EnvironmentRoutes) startProvisioning(ctx context.Context, created model.Environment, placement resolvedPlacement) error {
	input, err := r.deployInput(ctx, created, created.RuntimeVersion, placement)
	if err != nil {
		return err
	}
	return r.provisioner.Start(input)
}
