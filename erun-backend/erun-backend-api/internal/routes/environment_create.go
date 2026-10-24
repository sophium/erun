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

// createEnvironmentRequest carries only operator-authored fields; the tenant is
// resolved from the caller's token, never trusted from the body, with one
// deliberate exception: TenantID, honoured only for an operations caller, to
// place the row in a named tenant instead of their own — refused with 403
// for any other caller. Preview resolves and returns the same
// ordered plan POST /v1/provision renders, without creating the row — the
// executing path previewing itself, so the plan an operator audits here is
// the plan a non-preview call then runs.
type createEnvironmentRequest struct {
	Name              string `json:"name"`
	Type              string `json:"type"`
	ContextID         string `json:"contextId"`
	KubernetesContext string `json:"kubernetesContext"`
	RuntimeVersion    string `json:"runtimeVersion"`
	Preview           bool   `json:"preview"`
	// Adopt registers a row for an environment that already exists instead
	// of asking the platform to provision one — see validateAdoptInput for
	// the shape it requires.
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

// decodeCreateEnvironmentInput validates the create body and returns the
// environment it describes, whether the caller asked for a preview, whether
// the caller asked to adopt an environment that already exists (see
// validateAdoptInput), and the raw requested tenant ID (empty unless the
// caller named one — resolved and authorized by resolveTargetTenant). Its
// error message is the operator-facing 400 reason.
func decodeCreateEnvironmentInput(req *http.Request) (model.Environment, bool, bool, string, error) {
	var body createEnvironmentRequest
	if err := decodeJSON(req, &body); err != nil {
		return model.Environment{}, false, false, "", errors.New("invalid request body")
	}
	name := strings.TrimSpace(body.Name)
	// The tenant is hyphen-free (enforced at tenant registration), so allowing
	// internal hyphens in the env keeps the first-hyphen split of the
	// <tenant>-<env> namespace unambiguous.
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

// validateAdoptInput enforces the shape of an adopt request: it describes
// an environment that already exists rather than asking the
// platform to provision one, so it names where that environment runs
// (kubernetesContext) and carries nothing that would trigger a deploy
// (runtimeVersion) or a platform-managed placement decision (contextId) —
// see createEnvironment's placement bypass for adopt requests below.
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

// resolveCreateEnvironmentTenantScope resolves and authorizes the tenant
// this create targets (see resolveTargetTenant), writing the response itself
// on failure so createEnvironment only needs to check ok. homeCtx is the caller's own, unscoped context; scopedCtx is already
// resolved to the target tenant (see scopedContextForTenant); crossTenant
// tells the caller whether that target differs from the caller's own.
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

// resolveCreateEnvironmentPlacement resolves and persists the create-time
// placement decision for an ordinary request, or leaves it at the zero value
// for an adopt request. An adopt request skips placement entirely: the
// platform never places or deploys it, so its kubernetesContext is opaque
// metadata, and the runtime placement rules would wrongly refuse the raw
// kubernetesContext adopt requires.
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

// enforceCreateEnvironmentResourceQuota runs enforceRuntimeResourceQuotaOnCreate
// only for an ordinary runtime create. An adopted environment
// consumes no platform-managed cluster capacity (it is never placed or
// deployed), so the runtime resource-quota floor and aggregate budget do not
// apply to it.
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

// createEnvironmentRow persists the new row: the caller's own tenant by
// default (unchanged) when crossTenant is false, or — already authorized by
// resolveTargetTenant — targetTenantID instead, through EnvironmentAdminCreator
// so the cross-tenant write is audited. scopedCtx is req.Context() at
// the point of the call (already resolved to the target tenant when
// crossTenant is true); homeCtx is the original, unscoped context the
// request arrived with.
func (r EnvironmentRoutes) createEnvironmentRow(scopedCtx, homeCtx context.Context, targetTenantID string, crossTenant bool, environment model.Environment) (model.Environment, error) {
	if !crossTenant {
		return r.environments.Create(scopedCtx, environment)
	}
	if r.admin == nil {
		return model.Environment{}, errors.New("creating an environment in another tenant requires cross-tenant administration, which is not configured on this control plane")
	}
	return r.admin.CreateForTenant(scopedCtx, homeCtx, targetTenantID, environment)
}

// persistAndMaybeProvision creates the row and, for a runtime env with a
// pinned version and a wired provisioner, starts the durable server-side
// deploy — the non-preview tail of createEnvironment. placement is the
// decision resolvePlacement already made; created.ContextID (an FK, so a
// context belonging to another tenant is rejected here too, the enforcement
// point for tenant isolation on the reference) should always echo it back.
func (r EnvironmentRoutes) persistAndMaybeProvision(w http.ResponseWriter, req *http.Request, environment model.Environment, placement resolvedPlacement, homeCtx context.Context, targetTenantID string, crossTenant bool) {
	created, err := r.createEnvironmentRow(req.Context(), homeCtx, targetTenantID, crossTenant, environment)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}

	// A runtime env with a pinned version deploys server-side: when a provisioner
	// is wired, start the durable deploy and return 202 so the caller polls
	// GET /v1/environments/{id} to running/failed. Otherwise (no provisioner, a
	// non-runtime env, or no version to deploy) the row is only registered
	// config (201).
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

// writeStartProvisioningError answers a failure to enqueue the durable
// workflow. The environment row stays exactly as persistAndMaybeProvision
// already left it (registered, not provisioning) — nothing was attempted, so
// nothing needs unwinding. A missing tenant runtime image does not reach this
// path: it selects the canonical-image bootstrap instead.
func writeStartProvisioningError(w http.ResponseWriter, ctx context.Context, err error) {
	logServerErrorForRoute(ctx, "POST /v1/environments", err)
	writeError(w, http.StatusInternalServerError, "failed to start provisioning")
}

// previewCreateEnvironment resolves and returns the same ordered plan a
// non-preview call with the same body would run, without creating the row —
// provisionPlan for an ordinary create, or adoptPlan for an adopt request
// (provisionPlan always renders a "deploy:" line for a runtime environment,
// which would misrepresent an adopt request that never deploys). For a
// non-adopt request resolvePlacement has already run, so
// environment.ContextID is the resolved placement decision, and the only way this diverges from what a non-preview call
// would do is the quota check, which — like /v1/provision — reports rather
// than 409s so the full intended plan is still visible.
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

// startProvisioning kicks off the durable deploy workflow for a newly-created
// runtime env, keyed by the environment so a retried create never double-deploys.
// placement is the decision createEnvironment's resolvePlacement already made.
func (r EnvironmentRoutes) startProvisioning(ctx context.Context, created model.Environment, placement resolvedPlacement) error {
	input, err := r.deployInput(ctx, created, created.RuntimeVersion, placement)
	if err != nil {
		return err
	}
	return r.provisioner.Start(input)
}
