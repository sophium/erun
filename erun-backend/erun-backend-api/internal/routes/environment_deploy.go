package routes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/provision"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// deployClaimStaleAfter exceeds the deploy Job's 30-minute deadline, so a stale
// claim can no longer have a live run behind it.
const deployClaimStaleAfter = 45 * time.Minute

// deployClaimRefusal explains a refused ClaimDeploy from the pre-claim status; the
// claim itself decides, so a race only makes the message stale.
func deployClaimRefusal(status model.EnvironmentStatus) string {
	switch status {
	case model.EnvironmentStatusDeleting:
		return "this environment is being deleted; it cannot be deployed until the teardown finishes"
	case model.EnvironmentStatusDeletionBlocked:
		return "this environment's delete is blocked and still outstanding; resolve the teardown before deploying it again"
	case model.EnvironmentStatusRegistered, model.EnvironmentStatusProvisioning, model.EnvironmentStatusRunning, model.EnvironmentStatusFailed:
	}
	return "a deploy is already in progress for this environment"
}

type deployEnvironmentRequest struct {
	Version string `json:"version"`
}

// deployEnvironment redeploys a registered environment at an already-published
// version; it never builds or pushes.
func (r EnvironmentRoutes) deployEnvironment(w http.ResponseWriter, req *http.Request) {
	if r.provisioner == nil {
		writeError(w, http.StatusNotImplemented, "the deploy executor is not configured")
		return
	}
	ctx := req.Context()
	environment, err := r.environments.Get(ctx, req.PathValue("environment_id"))
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	version, err := resolveDeployVersion(req, environment)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Re-checked on redeploy: the quota may have been lowered since create.
	if err := r.revalidateResourceQuota(ctx); err != nil {
		var exceeded *resourceQuotaExceededError
		if errors.As(err, &exceeded) {
			writeError(w, http.StatusConflict, exceeded.Error())
			return
		}
		writeRepositoryError(w, req, err)
		return
	}
	// Claiming first keeps a double-submit from running two rollouts.
	claimed, err := r.environments.ClaimDeploy(ctx, environment.TenantID, environment.EnvironmentID, deployClaimStaleAfter)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	if !claimed {
		writeError(w, http.StatusConflict, deployClaimRefusal(environment.Status))
		return
	}
	if err := r.startDeploy(ctx, environment, version); err != nil {
		r.writeStartDeployError(w, ctx, environment, err)
		return
	}
	writeJSON(w, http.StatusAccepted, environment)
}

// resolveDeployVersion does not re-run placement: a redeploy stays on the
// environment's existing cluster.
func resolveDeployVersion(req *http.Request, environment model.Environment) (string, error) {
	if environment.Type != model.EnvironmentTypeRuntime {
		return "", errors.New("only a runtime environment can be deployed")
	}
	var body deployEnvironmentRequest
	if err := decodeJSON(req, &body); err != nil && !errors.Is(err, io.EOF) {
		return "", errors.New("invalid request body")
	}
	version := strings.TrimSpace(body.Version)
	if version == "" {
		version = environment.RuntimeVersion
	}
	if version == "" {
		return "", errors.New("version is required: the environment has no pinned runtimeVersion to deploy")
	}
	return version, nil
}

// writeStartDeployError marks the row failed, since ClaimDeploy already moved it
// to provisioning and no workflow would move it out.
func (r EnvironmentRoutes) writeStartDeployError(w http.ResponseWriter, ctx context.Context, environment model.Environment, err error) {
	_ = r.environments.MarkDeployFailed(ctx, environment.TenantID, environment.EnvironmentID, err.Error())
	logServerErrorForRoute(ctx, "POST /v1/environments/{environment_id}/deploy", err)
	writeError(w, http.StatusInternalServerError, "failed to start deploy")
}

// startDeploy uses a fresh attempt id so it re-runs rather than replays the first deploy.
func (r EnvironmentRoutes) startDeploy(ctx context.Context, environment model.Environment, version string) error {
	placement, err := r.resolvePlacementCoordinates(ctx, r.placementOwnerTenantFor(ctx, environment), environment.ContextID)
	if err != nil {
		return err
	}
	input, err := r.deployInput(ctx, environment, version, placement)
	if err != nil {
		return err
	}
	input.DeployID = uuid.NewString()
	return r.provisioner.StartDeploy(input)
}

// deployInput carries non-secret placement coordinates only; the credential is
// resolved at Job-build time, never checkpointed in the workflow input.
func (r EnvironmentRoutes) deployInput(ctx context.Context, environment model.Environment, version string, placement resolvedPlacement) (provision.EnvProvisionInput, error) {
	tenant, err := r.tenants.Current(ctx)
	if err != nil {
		return provision.EnvProvisionInput{}, err
	}
	securityContext, ok := security.FromContext(ctx)
	if !ok {
		return provision.EnvProvisionInput{}, fmt.Errorf("missing security context")
	}
	quota, err := r.quotas.Get(ctx)
	if err != nil {
		return provision.EnvProvisionInput{}, err
	}
	return provision.EnvProvisionInput{
		TenantID:                   placementOwnerTenant(environment, securityContext),
		TenantType:                 securityContext.TenantType,
		ErunUserID:                 securityContext.ErunUserID,
		EnvironmentID:              environment.EnvironmentID,
		Tenant:                     strings.TrimSpace(tenant.Name),
		Environment:                environment.Name,
		Version:                    version,
		ContextID:                  placement.ContextID,
		PlacementKubernetesContext: placement.KubernetesContext,
		PlacementServerURL:         placement.ServerURL,
		MaxCPUMillicores:           quota.MaxCPUMillicores,
		MaxMemoryMB:                quota.MaxMemoryMB,
		MaxStorageGB:               quota.MaxStorageGB,
	}, nil
}
