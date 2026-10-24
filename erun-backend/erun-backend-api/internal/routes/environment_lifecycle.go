package routes

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/provision"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// stopEnvironment scales a runtime environment's Deployment to zero, the
// server-side equivalent of `erun stop`.
func (r EnvironmentRoutes) stopEnvironment(w http.ResponseWriter, req *http.Request) {
	if r.lifecycle == nil {
		writeError(w, http.StatusNotImplemented, "the deploy executor is not configured")
		return
	}
	ctx := req.Context()
	environment, err := r.environments.Get(ctx, req.PathValue("environment_id"))
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	if environment.Type != model.EnvironmentTypeRuntime {
		writeError(w, http.StatusBadRequest, "only a runtime environment can be stopped")
		return
	}
	input, err := r.lifecycleInput(ctx, environment)
	if err != nil {
		writeInternalError(w, req, "failed to resolve environment placement", err)
		return
	}
	input.StopID = uuid.NewString()
	if err := r.lifecycle.Stop(ctx, input); err != nil {
		writeError(w, http.StatusBadGateway, "failed to stop environment: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, environment)
}

// deleteClaimStaleAfter is provision.DeleteClaimStaleAfter under its
// routes-local name, matching deployClaimStaleAfter's naming here.
const deleteClaimStaleAfter = provision.DeleteClaimStaleAfter

// deleteEnvironment starts tearing down a runtime environment's namespace
// (when it has one) and its row, the server-side equivalent of `erun
// delete`. The teardown runs asynchronously: a namespace stuck on an
// unsatisfiable finalizer can sit in Terminating indefinitely, so this
// handler claims the delete, starts the durable workflow, and returns 202. A
// caller polls GET /v1/environments/{id} until it is gone (a later GET
// answers 204) or deletion-blocked.
func (r EnvironmentRoutes) deleteEnvironment(w http.ResponseWriter, req *http.Request) {
	if r.deleter == nil {
		writeError(w, http.StatusNotImplemented, "the deploy executor is not configured")
		return
	}
	ctx := req.Context()
	environment, err := r.environments.Get(ctx, req.PathValue("environment_id"))
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	// Claiming before starting the workflow is what keeps a double-submit
	// from launching two delete Jobs against the same namespace, and reclaims
	// a stale or already-blocked attempt so a retry never needs an operator
	// to notice and wait it out by hand.
	claimed, err := r.environments.ClaimDelete(ctx, environment.TenantID, environment.EnvironmentID, deleteClaimStaleAfter)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	if !claimed {
		writeError(w, http.StatusConflict, deleteClaimRefusal(environment.Status))
		return
	}
	if err := r.startDelete(ctx, environment); err != nil {
		r.writeStartDeleteError(w, ctx, environment, err)
		return
	}
	environment.Status = model.EnvironmentStatusDeleting
	writeJSON(w, http.StatusAccepted, environment)
}

// startDelete kicks off the durable delete workflow, tagged with a fresh
// attempt id so a retry (an operator's or the reconciler's) never replays a
// previous attempt's cached (e.g. deletion-blocked) result instead of
// actually running again. It targets the cluster the environment was already
// placed on at create time (resolvePlacementCoordinates), never a freshly
// auto-selected one, mirroring startDeploy.
func (r EnvironmentRoutes) startDelete(ctx context.Context, environment model.Environment) error {
	tenant, err := r.tenants.Current(ctx)
	if err != nil {
		return err
	}
	securityContext, ok := security.FromContext(ctx)
	if !ok {
		return fmt.Errorf("missing security context")
	}
	// The workflow's tenant identity and the placement's owner both come from
	// the environment row, not from the caller: the row is what owns the
	// context whose credential the teardown Job will be handed. On the
	// ordinary single-tenant path the two are the same tenant and this is a
	// no-op; it is what keeps an OPERATIONS caller's own tenant from being
	// paired with a context that belongs to someone else.
	ownerTenantID := placementOwnerTenant(environment, securityContext)
	placement, err := r.resolvePlacementCoordinates(ctx, ownerTenantID, environment.ContextID)
	if err != nil {
		return err
	}
	version := environment.DeployedVersion
	if version == "" {
		version = environment.RuntimeVersion
	}
	return r.deleter.Start(provision.EnvDeleteInput{
		TenantID:                   ownerTenantID,
		TenantType:                 securityContext.TenantType,
		ErunUserID:                 securityContext.ErunUserID,
		EnvironmentID:              environment.EnvironmentID,
		Tenant:                     strings.TrimSpace(tenant.Name),
		Environment:                environment.Name,
		RunningVersion:             version,
		ContextID:                  placement.ContextID,
		PlacementKubernetesContext: placement.KubernetesContext,
		PlacementServerURL:         placement.ServerURL,
		DeleteID:                   uuid.NewString(),
	})
}

// deleteClaimRefusal is deployClaimRefusal's counterpart: a delete refused
// because a deploy holds the row is told so, since the actionable step is to
// wait for that deploy rather than to retry immediately.
func deleteClaimRefusal(status model.EnvironmentStatus) string {
	if status == model.EnvironmentStatusProvisioning {
		return "a deploy is in progress for this environment; retry the delete once it finishes"
	}
	return "a delete is already in progress for this environment"
}

// writeStartDeleteError marks the environment deletion-blocked and answers
// 500: ClaimDelete already moved the row to `deleting` before startDelete
// ran, so any failure to even enqueue the durable workflow would otherwise
// strand the environment there with no workflow run left to move it out.
func (r EnvironmentRoutes) writeStartDeleteError(w http.ResponseWriter, ctx context.Context, environment model.Environment, err error) {
	_ = r.environments.MarkDeleteBlocked(ctx, environment.TenantID, environment.EnvironmentID, err.Error())
	logServerErrorForRoute(ctx, "DELETE /v1/environments/{environment_id}", err)
	writeError(w, http.StatusInternalServerError, "failed to start delete")
}

// lifecycleInput resolves the placement a stop/delete Job needs: the tenant
// name (RLS-scoped to the caller), the version the environment last actually
// deployed, and the cluster it was placed on at create time —
// stop/delete always target that same cluster, never a freshly auto-selected
// one, mirroring deployInput's tenant resolution.
func (r EnvironmentRoutes) lifecycleInput(ctx context.Context, environment model.Environment) (provision.EnvLifecycleInput, error) {
	tenant, err := r.tenants.Current(ctx)
	if err != nil {
		return provision.EnvLifecycleInput{}, err
	}
	// The stop/delete Job's placement credential is fetched for the tenant
	// that owns the environment, never for whoever asked — see startDelete.
	ownerTenantID := r.placementOwnerTenantFor(ctx, environment)
	placement, err := r.resolvePlacementCoordinates(ctx, ownerTenantID, environment.ContextID)
	if err != nil {
		return provision.EnvLifecycleInput{}, err
	}
	version := environment.DeployedVersion
	if version == "" {
		version = environment.RuntimeVersion
	}
	return provision.EnvLifecycleInput{
		TenantID:                   ownerTenantID,
		Tenant:                     strings.TrimSpace(tenant.Name),
		Environment:                environment.Name,
		EnvironmentID:              environment.EnvironmentID,
		RunningVersion:             version,
		ContextID:                  placement.ContextID,
		PlacementKubernetesContext: placement.KubernetesContext,
		PlacementServerURL:         placement.ServerURL,
	}, nil
}
