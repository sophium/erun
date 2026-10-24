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

// deployClaimStaleAfter bounds how long a deploy may hold its environment's
// claim. It is longer than the deploy Job's own 30-minute deadline, so a claim
// only looks stale once the run behind it cannot still be live.
const deployClaimStaleAfter = 45 * time.Minute

// deployClaimRefusal names why ClaimDeploy refused, so a caller mid-teardown
// is told that rather than the misleading "a deploy is already in progress".
// It reads the status fetched before the claim attempt: the claim itself is
// the authority on the outcome, so a row that changed in between yields a
// slightly stale explanation but never a wrong decision.
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

// deployEnvironmentRequest re-deploys at an explicit version; omitted, the
// environment's own pinned runtimeVersion is deployed.
type deployEnvironmentRequest struct {
	Version string `json:"version"`
}

// deployEnvironment deploys an already-registered environment, which is what
// makes the runtime version an environment runs changeable after creation: a
// retry of a failed deploy, or a move to another published version. It composes
// the pure deploy primitive — the version must already be published, and this
// never builds or pushes.
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
	// Re-checked here, not just at create: an operator can lower a tenant's
	// quota (TenantQuotaRepository.Set) after the environment already exists,
	// and a redeploy is the next thing that would hit the now-insufficient
	// floor or budget. Failing here is a clear 409 instead of the Job's
	// Deployment sitting at 0/1 until the rollout times out (#1061).
	if err := r.revalidateResourceQuota(ctx); err != nil {
		var exceeded *resourceQuotaExceededError
		if errors.As(err, &exceeded) {
			writeError(w, http.StatusConflict, exceeded.Error())
			return
		}
		writeRepositoryError(w, req, err)
		return
	}
	// Claiming before starting the workflow is what keeps a double-submit from
	// running two rollouts into the same release.
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

// resolveDeployVersion picks the version to deploy and rejects the requests that
// have nothing deployable. Its error message is the operator-facing 400 reason.
// Placement is a create-time decision (resolvePlacement), not re-run here: a
// redeploy targets the cluster the environment was already placed on, never
// a freshly auto-selected one.
func resolveDeployVersion(req *http.Request, environment model.Environment) (string, error) {
	if environment.Type != model.EnvironmentTypeRuntime {
		return "", errors.New("only a runtime environment can be deployed")
	}
	var body deployEnvironmentRequest
	// A body-less deploy is the common case (deploy what the env is pinned to),
	// so only a malformed body is an error.
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

// writeStartDeployError marks the environment failed and answers 500:
// ClaimDeploy already moved the row to provisioning before startDeploy ran, so
// any failure to even enqueue the durable workflow would otherwise strand the
// environment there with no workflow run left to move it out.
func (r EnvironmentRoutes) writeStartDeployError(w http.ResponseWriter, ctx context.Context, environment model.Environment, err error) {
	_ = r.environments.MarkDeployFailed(ctx, environment.TenantID, environment.EnvironmentID, err.Error())
	logServerErrorForRoute(ctx, "POST /v1/environments/{environment_id}/deploy", err)
	writeError(w, http.StatusInternalServerError, "failed to start deploy")
}

// startDeploy kicks off the durable deploy workflow for an explicit deploy,
// tagged with a fresh attempt id so it is a real re-run rather than a replay of
// the environment's first deploy. It targets the cluster the environment was
// already placed on at create time (resolvePlacementCoordinates), never a
// freshly auto-selected one.
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

// deployInput assembles the durable workflow input. The deploy needs the tenant
// name (RLS-scoped to the caller, so always the caller's own) plus the
// request-scoped identity so the workflow's status writes rebind to the right
// tenant. placement carries the target cluster's non-secret coordinates
// (#1112); the credential itself is resolved fresh at Job-build time, never
// checkpointed in this durable-workflow input.
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
