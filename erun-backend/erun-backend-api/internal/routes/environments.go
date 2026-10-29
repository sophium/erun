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
	CountByContext(ctx context.Context, contextID string) (int, error)
	CountByType(ctx context.Context, envType model.EnvironmentType) (int, error)
	// Mutators take the owning tenant explicitly: the delete reconciler and lifecycle
	// call them with contexts that carry no tenant.
	ClaimDeploy(ctx context.Context, tenantID, environmentID string, staleAfter time.Duration) (bool, error)
	// MarkDeployFailed un-strands a claim that never reached the workflow.
	MarkDeployFailed(ctx context.Context, tenantID, environmentID, reason string) error
	ClaimDelete(ctx context.Context, tenantID, environmentID string, staleAfter time.Duration) (bool, error)
	// MarkDeleteBlocked un-strands a claim that never reached the workflow.
	MarkDeleteBlocked(ctx context.Context, tenantID, environmentID, reason string) error
}

// PlacementContextRepository reads the tenant's registered contexts.
type PlacementContextRepository interface {
	List(ctx context.Context) ([]model.Context, error)
	// Get takes the tenant because erun_operations' RLS on contexts is unconditional.
	Get(ctx context.Context, tenantID, contextID string) (model.Context, error)
}

// EnvironmentProvisioner starts the durable server-side deploy. Nil means rows are
// registered only.
type EnvironmentProvisioner interface {
	Start(provision.EnvProvisionInput) error
	StartDeploy(provision.EnvProvisionInput) error
}

// EnvironmentLifecycle stops a runtime environment. Nil means the route answers 501.
type EnvironmentLifecycle interface {
	Stop(ctx context.Context, input provision.EnvLifecycleInput) error
}

// EnvironmentDeleter starts a delete asynchronously, since the workflow can wedge
// on a stuck finalizer. Nil means the route answers 501.
type EnvironmentDeleter interface {
	Start(input provision.EnvDeleteInput) error
}

// TenantQuotaRepository returns the tenant's quota, defaulted when no row exists.
type TenantQuotaRepository interface {
	Get(ctx context.Context) (model.TenantQuota, error)
}

type EnvironmentRoutes struct {
	environments EnvironmentRepository
	quotas       TenantQuotaRepository
	// tenants resolves the tenant name, which forms the <tenant>-<env> namespace.
	tenants     ConfigTenantRepository
	contexts    PlacementContextRepository
	provisioner EnvironmentProvisioner
	lifecycle   EnvironmentLifecycle
	deleter     EnvironmentDeleter
	// admin is nil unless cross-tenant administration is wired; a cross-tenant create
	// then fails rather than falling back to the caller's tenant.
	admin EnvironmentAdminCreator
}

// EnvironmentAdminCreator creates an environment in another tenant and audits it.
// homeCtx is the caller's unscoped context, so the audit names the real home tenant.
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

// scopedContextForTenant substitutes the already-authorized target tenant into
// the security context, so tenant-scoped repository calls act on it.
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
