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

// stopEnvironment is the server-side `erun stop`.
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

const deleteClaimStaleAfter = provision.DeleteClaimStaleAfter

// deleteEnvironment runs the teardown asynchronously (202), since a namespace can
// sit in Terminating indefinitely on an unsatisfiable finalizer.
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
	// Claiming first blocks a double-submit and reclaims stale or blocked attempts.
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

// startDelete uses a fresh attempt id so a retry never replays a cached result.
func (r EnvironmentRoutes) startDelete(ctx context.Context, environment model.Environment) error {
	tenant, err := r.tenants.Current(ctx)
	if err != nil {
		return err
	}
	securityContext, ok := security.FromContext(ctx)
	if !ok {
		return fmt.Errorf("missing security context")
	}
	// Identity and placement owner come from the row, so an OPERATIONS caller is never
	// paired with another tenant's context credential.
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

// deleteClaimRefusal tells a caller blocked by a deploy to wait for it.
func deleteClaimRefusal(status model.EnvironmentStatus) string {
	if status == model.EnvironmentStatusProvisioning {
		return "a deploy is in progress for this environment; retry the delete once it finishes"
	}
	return "a delete is already in progress for this environment"
}

// writeStartDeleteError marks the row deletion-blocked, since ClaimDelete already
// moved it to deleting and no workflow would move it out.
func (r EnvironmentRoutes) writeStartDeleteError(w http.ResponseWriter, ctx context.Context, environment model.Environment, err error) {
	_ = r.environments.MarkDeleteBlocked(ctx, environment.TenantID, environment.EnvironmentID, err.Error())
	logServerErrorForRoute(ctx, "DELETE /v1/environments/{environment_id}", err)
	writeError(w, http.StatusInternalServerError, "failed to start delete")
}

// lifecycleInput targets the cluster chosen at create time, never a re-selected one.
func (r EnvironmentRoutes) lifecycleInput(ctx context.Context, environment model.Environment) (provision.EnvLifecycleInput, error) {
	tenant, err := r.tenants.Current(ctx)
	if err != nil {
		return provision.EnvLifecycleInput{}, err
	}
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
