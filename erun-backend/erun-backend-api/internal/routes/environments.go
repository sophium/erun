package routes

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/provision"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type EnvironmentRepository interface {
	List(ctx context.Context) ([]model.Environment, error)
	Get(ctx context.Context, environmentID string) (model.Environment, error)
	Create(ctx context.Context, environment model.Environment) (model.Environment, error)
	Count(ctx context.Context) (int, error)
	// CountByContext reports how many environments already occupy a placement
	// candidate, for the capacity check (#1112).
	CountByContext(ctx context.Context, contextID string) (int, error)
	// CountByType reports how many of the caller's tenant's environments are
	// of the given type, for the aggregate resource-budget check (#1113).
	CountByType(ctx context.Context, envType model.EnvironmentType) (int, error)
	// The mutating methods below take the owning tenant explicitly, alongside
	// the environment id. They are reached both from here — where the row was
	// just read back under the caller's own tenant — and from the delete
	// reconciler and lifecycle, whose contexts carry no tenant at all, so the
	// owner has to travel as an argument rather than be inferred from ctx.
	// See repository.EnvironmentRepository.UpdateProvisioningStatus.
	ClaimDeploy(ctx context.Context, tenantID, environmentID string, staleAfter time.Duration) (bool, error)
	// MarkDeployFailed records a deploy claim that never reached the durable
	// workflow (see writeStartProvisioningError), so the environment does not
	// stay stranded in provisioning.
	MarkDeployFailed(ctx context.Context, tenantID, environmentID, reason string) error
	// ClaimDelete takes exclusive ownership of a delete attempt (#1140),
	// mirroring ClaimDeploy: false means another delete already holds it.
	ClaimDelete(ctx context.Context, tenantID, environmentID string, staleAfter time.Duration) (bool, error)
	// MarkDeleteBlocked records a delete claim that never reached the durable
	// workflow (see writeStartDeleteError), so the environment does not stay
	// stranded in `deleting`.
	MarkDeleteBlocked(ctx context.Context, tenantID, environmentID, reason string) error
}

// PlacementContextRepository is the read access placement (#1112) needs: list
// the tenant's own registered contexts (RLS-scoped) for auto-selection, and
// fetch one by id to validate an explicit request and read its coordinates.
type PlacementContextRepository interface {
	List(ctx context.Context) ([]model.Context, error)
	// Get takes the owning tenant alongside the context id. erun_operations'
	// RLS policy on contexts is unconditional, so an id-only lookup returns
	// whichever tenant's context that id names.
	Get(ctx context.Context, tenantID, contextID string) (model.Context, error)
}

// EnvironmentProvisioner starts the durable server-side deploy of an
// environment — once on create, and again for each explicit deploy. Optional:
// when nil, the environment routes only register rows (no live deploy),
// matching the pre-executor behavior.
type EnvironmentProvisioner interface {
	Start(provision.EnvProvisionInput) error
	StartDeploy(provision.EnvProvisionInput) error
}

// EnvironmentLifecycle stops an already-registered runtime environment.
// Optional: when nil, the stop route reports 501, the same "executor not
// configured" shape as a nil EnvironmentProvisioner.
type EnvironmentLifecycle interface {
	Stop(ctx context.Context, input provision.EnvLifecycleInput) error
}

// EnvironmentDeleter starts an already-registered runtime environment's
// delete attempt asynchronously (#1140): the durable workflow behind it may
// run long or wedge entirely (a stuck namespace finalizer), so the route that
// starts one must not be the thing waiting on it. Optional: when nil, the
// delete route reports 501, the same "executor not configured" shape as a nil
// EnvironmentProvisioner.
type EnvironmentDeleter interface {
	Start(input provision.EnvDeleteInput) error
}

// TenantQuotaRepository reports the caller's full quota row (env count plus
// the per-environment CPU/memory/storage namespace ceiling). When the tenant
// has no quota row the repository returns the defaulted caps, so the route
// never needs to distinguish "unconfigured" from "explicit".
type TenantQuotaRepository interface {
	Get(ctx context.Context) (model.TenantQuota, error)
}

type EnvironmentRoutes struct {
	environments EnvironmentRepository
	quotas       TenantQuotaRepository
	// tenants resolves the caller's tenant name (not UUID) for the deploy, which
	// forms the <tenant>-<env> namespace and runtime release name.
	tenants ConfigTenantRepository
	// contexts resolves placement candidates (#1112): the tenant's own
	// registered clusters, read-only from this route's point of view.
	contexts PlacementContextRepository
	// provisioner is nil when live env provisioning is not wired; then create
	// only registers the row.
	provisioner EnvironmentProvisioner
	// lifecycle is nil when live env provisioning is not wired; then stop is
	// unavailable rather than acting on config no Job can realize.
	lifecycle EnvironmentLifecycle
	// deleter is nil when live env provisioning is not wired; then delete is
	// unavailable rather than acting on config no Job can realize.
	deleter EnvironmentDeleter
	// admin is nil unless the server wires cross-tenant administration; a
	// create that targets another tenant then fails clearly (500) rather
	// than silently falling back to the caller's own tenant. Only
	// createEnvironment uses it — list needs no audit trail.
	admin EnvironmentAdminCreator
}

// EnvironmentAdminCreator creates an environment in a tenant other than the
// caller's own and records who did it: every ordinary repository
// Create is a single write, but a cross-tenant one is a workflow — the write
// plus a durable record of which operator, from which home tenant, placed a
// row somewhere they do not otherwise operate. scopedCtx is already resolved
// to the target tenant (see scopedContextForTenant); homeCtx is the caller's
// own, unscoped security context, so the audit trail names the operator's
// real home tenant rather than the tenant the write landed in.
type EnvironmentAdminCreator interface {
	CreateForTenant(scopedCtx, homeCtx context.Context, targetTenantID string, environment model.Environment) (model.Environment, error)
}

func RegisterEnvironmentRoutes(register ProtectedRouteRegistrar, environments EnvironmentRepository, quotas TenantQuotaRepository, tenants ConfigTenantRepository, contexts PlacementContextRepository, provisioner EnvironmentProvisioner, lifecycle EnvironmentLifecycle, deleter EnvironmentDeleter, admin EnvironmentAdminCreator) {
	routes := EnvironmentRoutes{environments: environments, quotas: quotas, tenants: tenants, contexts: contexts, provisioner: provisioner, lifecycle: lifecycle, deleter: deleter, admin: admin}
	register(http.MethodGet, "/v1/environments", http.HandlerFunc(routes.listEnvironments))
	register(http.MethodPost, "/v1/environments", http.HandlerFunc(routes.createEnvironment))
	register(http.MethodGet, "/v1/environments/{environment_id}", http.HandlerFunc(routes.getEnvironment))
	register(http.MethodPost, "/v1/environments/{environment_id}/deploy", http.HandlerFunc(routes.deployEnvironment))
	register(http.MethodPost, "/v1/environments/{environment_id}/stop", http.HandlerFunc(routes.stopEnvironment))
	register(http.MethodDelete, "/v1/environments/{environment_id}", http.HandlerFunc(routes.deleteEnvironment))
}

// scopedContextForTenant returns ctx unchanged when targetTenantID is the
// caller's own tenant — resolveTargetTenant's own default, and every
// caller's ordinary behavior, unaffected by any of this existing at all — or
// a context whose security.Context.TenantID is substituted for
// targetTenantID, already authorized by resolveTargetTenant, so every
// repository method this package calls that derives its tenant scope from
// the security context (EnvironmentRepository.List/Count,
// TenantQuotaRepository.Get, ConfigTenantRepository.Current) operates on the
// target tenant instead. No repository method needs a parallel "or
// this other tenant" parameter to support it.
func scopedContextForTenant(ctx context.Context, securityContext security.Context, targetTenantID string) context.Context {
	if targetTenantID == securityContext.TenantID {
		return ctx
	}
	securityContext.TenantID = targetTenantID
	return security.WithContext(ctx, securityContext)
}

func (r EnvironmentRoutes) listEnvironments(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), errors.New("security context not found in request"))
		return
	}
	targetTenantID, err := resolveTargetTenant(securityContext, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	environments, err := r.environments.List(scopedContextForTenant(req.Context(), securityContext, targetTenantID))
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, environments)
}

func (r EnvironmentRoutes) getEnvironment(w http.ResponseWriter, req *http.Request) {
	environment, err := r.environments.Get(req.Context(), req.PathValue("environment_id"))
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, environment)
}
