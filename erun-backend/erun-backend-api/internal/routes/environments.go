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

	eruncommon "github.com/sophium/erun/erun-common"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/provision"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
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
	ClaimDeploy(ctx context.Context, environmentID string, staleAfter time.Duration) (bool, error)
	// MarkDeployFailed records a deploy claim that never reached the durable
	// workflow (see writeStartProvisioningError), so the environment does not
	// stay stranded in provisioning.
	MarkDeployFailed(ctx context.Context, environmentID, reason string) error
}

// PlacementContextRepository is the read access placement (#1112) needs: list
// the tenant's own registered contexts (RLS-scoped) for auto-selection, and
// fetch one by id to validate an explicit request and read its coordinates.
type PlacementContextRepository interface {
	List(ctx context.Context) ([]model.Context, error)
	Get(ctx context.Context, contextID string) (model.Context, error)
}

// EnvironmentProvisioner starts the durable server-side deploy of an
// environment — once on create, and again for each explicit deploy. Optional:
// when nil, the environment routes only register rows (no live deploy),
// matching the pre-executor behavior.
type EnvironmentProvisioner interface {
	Start(provision.EnvProvisionInput) error
	StartDeploy(provision.EnvProvisionInput) error
}

// EnvironmentLifecycle stops or deletes an already-registered runtime
// environment. Optional: when nil, the stop/delete routes report 501, the
// same "executor not configured" shape as a nil EnvironmentProvisioner.
type EnvironmentLifecycle interface {
	Stop(ctx context.Context, input provision.EnvLifecycleInput) error
	Delete(ctx context.Context, input provision.EnvLifecycleInput) error
}

// deployClaimStaleAfter bounds how long a deploy may hold its environment's
// claim. It is longer than the deploy Job's own 30-minute deadline, so a claim
// only looks stale once the run behind it cannot still be live.
const deployClaimStaleAfter = 45 * time.Minute

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
	// lifecycle is nil when live env provisioning is not wired; then stop and
	// delete are unavailable rather than acting on config no Job can realize.
	lifecycle EnvironmentLifecycle
}

// createEnvironmentRequest carries only operator-authored fields; the tenant is
// resolved from the caller's token, never trusted from the body. Preview
// resolves and returns the same ordered plan POST /v1/provision renders,
// without creating the row — the executing path previewing itself, so the
// plan an operator audits here is the plan a non-preview call then runs.
type createEnvironmentRequest struct {
	Name              string `json:"name"`
	Type              string `json:"type"`
	ContextID         string `json:"contextId"`
	KubernetesContext string `json:"kubernetesContext"`
	RuntimeVersion    string `json:"runtimeVersion"`
	Preview           bool   `json:"preview"`
}

func RegisterEnvironmentRoutes(register ProtectedRouteRegistrar, environments EnvironmentRepository, quotas TenantQuotaRepository, tenants ConfigTenantRepository, contexts PlacementContextRepository, provisioner EnvironmentProvisioner, lifecycle EnvironmentLifecycle) {
	routes := EnvironmentRoutes{environments: environments, quotas: quotas, tenants: tenants, contexts: contexts, provisioner: provisioner, lifecycle: lifecycle}
	register(http.MethodGet, "/v1/environments", http.HandlerFunc(routes.listEnvironments))
	register(http.MethodPost, "/v1/environments", http.HandlerFunc(routes.createEnvironment))
	register(http.MethodGet, "/v1/environments/{environment_id}", http.HandlerFunc(routes.getEnvironment))
	register(http.MethodPost, "/v1/environments/{environment_id}/deploy", http.HandlerFunc(routes.deployEnvironment))
	register(http.MethodPost, "/v1/environments/{environment_id}/stop", http.HandlerFunc(routes.stopEnvironment))
	register(http.MethodDelete, "/v1/environments/{environment_id}", http.HandlerFunc(routes.deleteEnvironment))
}

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
		writeRepositoryError(w, err)
		return
	}
	if environment.Type != model.EnvironmentTypeRuntime {
		writeError(w, http.StatusBadRequest, "only a runtime environment can be stopped")
		return
	}
	input, err := r.lifecycleInput(ctx, environment)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve environment placement")
		return
	}
	if err := r.lifecycle.Stop(ctx, input); err != nil {
		writeError(w, http.StatusBadGateway, "failed to stop environment: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, environment)
}

// deleteEnvironment tears down a runtime environment's namespace (when it has
// one) and removes its row, the server-side equivalent of `erun delete`.
func (r EnvironmentRoutes) deleteEnvironment(w http.ResponseWriter, req *http.Request) {
	if r.lifecycle == nil {
		writeError(w, http.StatusNotImplemented, "the deploy executor is not configured")
		return
	}
	ctx := req.Context()
	environment, err := r.environments.Get(ctx, req.PathValue("environment_id"))
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	input, err := r.lifecycleInput(ctx, environment)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve environment placement")
		return
	}
	if err := r.lifecycle.Delete(ctx, input); err != nil {
		writeError(w, http.StatusBadGateway, "failed to delete environment: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// lifecycleInput resolves the placement a stop/delete Job needs: the tenant
// name (RLS-scoped to the caller), the version the environment last actually
// deployed, and the cluster it was placed on at create time (#1112) —
// stop/delete always target that same cluster, never a freshly auto-selected
// one, mirroring deployInput's tenant resolution.
func (r EnvironmentRoutes) lifecycleInput(ctx context.Context, environment model.Environment) (provision.EnvLifecycleInput, error) {
	tenant, err := r.tenants.Current(ctx)
	if err != nil {
		return provision.EnvLifecycleInput{}, err
	}
	placement, err := r.resolvePlacementCoordinates(ctx, environment.ContextID)
	if err != nil {
		return provision.EnvLifecycleInput{}, err
	}
	version := environment.DeployedVersion
	if version == "" {
		version = environment.RuntimeVersion
	}
	return provision.EnvLifecycleInput{
		Tenant:                     strings.TrimSpace(tenant.Name),
		Environment:                environment.Name,
		EnvironmentID:              environment.EnvironmentID,
		RunningVersion:             version,
		ContextID:                  placement.ContextID,
		PlacementKubernetesContext: placement.KubernetesContext,
		PlacementServerURL:         placement.ServerURL,
	}, nil
}

// resolvePlacementCoordinates reads back an already-placed environment's
// target-cluster coordinates: no capacity check, no auto-select. Placement is
// decided once, at create time (resolvePlacement); a redeploy, stop, or
// delete always targets the cluster the environment was already placed on.
// Empty contextID (the platform's own cluster) resolves to the zero
// resolvedPlacement with no repository read.
func (r EnvironmentRoutes) resolvePlacementCoordinates(ctx context.Context, contextID string) (resolvedPlacement, error) {
	contextID = strings.TrimSpace(contextID)
	if contextID == "" {
		return resolvedPlacement{}, nil
	}
	cloudContext, err := r.contexts.Get(ctx, contextID)
	if err != nil {
		return resolvedPlacement{}, err
	}
	return placementFromContext(cloudContext), nil
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
		writeRepositoryError(w, err)
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
		writeRepositoryError(w, err)
		return
	}
	// Claiming before starting the workflow is what keeps a double-submit from
	// running two rollouts into the same release.
	claimed, err := r.environments.ClaimDeploy(ctx, environment.EnvironmentID, deployClaimStaleAfter)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	if !claimed {
		writeError(w, http.StatusConflict, "a deploy is already in progress for this environment")
		return
	}
	if err := r.startDeploy(ctx, environment, version); err != nil {
		r.writeStartDeployError(w, ctx, environment.EnvironmentID, err)
		return
	}
	writeJSON(w, http.StatusAccepted, environment)
}

// errCrossClusterPlacementUnsupported is the actionable refusal for a runtime
// environment that names a raw kubernetesContext string rather than a
// registered contextId: this platform's placement machinery (#1112) resolves
// a cluster's server URL and admin-token credential from a `contexts` row —
// a bare context name has neither, so honoring it would still silently
// deploy to the wrong place (or nowhere real). Register the cluster with
// POST /v1/contexts first, then reference it by contextId.
var errCrossClusterPlacementUnsupported = errors.New(
	"deploying into a raw kubernetesContext name is not supported: register the cluster with POST /v1/contexts and reference it by contextId, or leave both unset to place into the platform's own cluster",
)

// errPlacementContextNotFound is the request-time refusal for a contextId
// that does not resolve for the caller's tenant. PlacementContextRepository's
// reads are RLS-scoped, so a context belonging to another tenant already
// reads as not-found here — the same enforcement environments.context_id's
// composite FK gives Create, surfaced synchronously instead of as an insert
// error.
var errPlacementContextNotFound = errors.New("named context was not found for this tenant")

// placementCapacityError names a placement candidate that has no room, so a
// caller can tell "this context is full" (409, actionable: raise its
// capacity or pick another) apart from every other placement failure.
type placementCapacityError struct{ detail string }

func (e *placementCapacityError) Error() string { return e.detail }

// resolvedPlacement is the target cluster a runtime environment's deploy/
// stop/delete Job authenticates against. The zero value places into the
// platform's own cluster — the only option before #1112, and still the
// default for a tenant that has registered no context of its own.
type resolvedPlacement struct {
	ContextID         string
	KubernetesContext string
	ServerURL         string
}

// contextStatusRunning is the only status a placement candidate may be in;
// a context still provisioning or one that failed to bootstrap has no live
// cluster or custodied credential to place into.
const contextStatusRunning = "running"

// placementServerURL is the k3s API server a bootstrapped context answers on
// (eruncommon.configureCloudKubeContext targets every cloud context the same
// way).
func placementServerURL(publicIP string) string {
	return "https://" + strings.TrimSpace(publicIP) + ":6443"
}

func placementFromContext(cloudContext model.Context) resolvedPlacement {
	kubernetesContext := strings.TrimSpace(cloudContext.KubernetesContext)
	if kubernetesContext == "" {
		kubernetesContext = cloudContext.Name
	}
	return resolvedPlacement{
		ContextID:         cloudContext.ContextID,
		KubernetesContext: kubernetesContext,
		ServerURL:         placementServerURL(cloudContext.PublicIP),
	}
}

// resolvePlacement decides which cluster (if any) a runtime environment's
// deploy targets (#1112). Only runtime environments are ever server-side
// deployed, so a non-runtime environment's context/kubernetesContext are
// opaque references with no placement decision to make — passed through
// exactly as the caller sent them, unchanged from before #1112 existed. A
// runtime environment naming a raw kubernetesContext is refused outright
// (see errCrossClusterPlacementUnsupported); one naming a contextId
// validates it resolves for the caller's tenant and has room. A runtime
// environment naming neither auto-selects the tenant's own registered
// contexts, preserving the pre-#1112 default (the platform's own cluster)
// for the common case of a tenant that has registered none.
func (r EnvironmentRoutes) resolvePlacement(ctx context.Context, environment model.Environment) (resolvedPlacement, error) {
	if environment.Type != model.EnvironmentTypeRuntime {
		return resolvedPlacement{ContextID: strings.TrimSpace(environment.ContextID)}, nil
	}
	if strings.TrimSpace(environment.KubernetesContext) != "" {
		return resolvedPlacement{}, errCrossClusterPlacementUnsupported
	}
	contextID := strings.TrimSpace(environment.ContextID)
	if contextID == "" {
		return r.autoSelectPlacement(ctx)
	}
	cloudContext, err := r.contexts.Get(ctx, contextID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return resolvedPlacement{}, errPlacementContextNotFound
		}
		return resolvedPlacement{}, err
	}
	if err := r.validatePlacementCapacity(ctx, cloudContext); err != nil {
		return resolvedPlacement{}, err
	}
	return placementFromContext(cloudContext), nil
}

// autoSelectPlacement picks the first of the tenant's own running contexts
// with room, in PlacementContextRepository.List's own deterministic order.
// A tenant with no registered contexts places into the platform's own
// cluster — a real, working placement, not a failure. A tenant that HAS
// registered contexts but none currently qualify (none running, or all at
// capacity) fails clearly instead of silently falling back, matching #1112's
// "or fail clearly (naming why) when none qualifies": once a tenant has
// opted into multi-cluster placement, a request that cannot honor it must
// say so rather than land somewhere the caller did not ask for.
func (r EnvironmentRoutes) autoSelectPlacement(ctx context.Context) (resolvedPlacement, error) {
	contexts, err := r.contexts.List(ctx)
	if err != nil {
		return resolvedPlacement{}, err
	}
	if len(contexts) == 0 {
		return resolvedPlacement{}, nil
	}
	running := 0
	for _, cloudContext := range contexts {
		if cloudContext.Status != contextStatusRunning {
			continue
		}
		running++
		if err := r.validatePlacementCapacity(ctx, cloudContext); err != nil {
			var capacityErr *placementCapacityError
			if errors.As(err, &capacityErr) {
				continue
			}
			return resolvedPlacement{}, err
		}
		return placementFromContext(cloudContext), nil
	}
	if running == 0 {
		return resolvedPlacement{}, &placementCapacityError{detail: fmt.Sprintf("no registered context is running yet; %d registered but still provisioning or failed", len(contexts))}
	}
	return resolvedPlacement{}, &placementCapacityError{detail: fmt.Sprintf("no registered context has room for a new environment: all %d running context(s) are at capacity", running)}
}

// validatePlacementCapacity fails clearly, naming the context and its
// ceiling, when a placement candidate has no room — the "or fail clearly"
// half of #1112's ask, rather than accepting the request and failing inside
// the Job.
func (r EnvironmentRoutes) validatePlacementCapacity(ctx context.Context, cloudContext model.Context) error {
	count, err := r.environments.CountByContext(ctx, cloudContext.ContextID)
	if err != nil {
		return err
	}
	if count >= cloudContext.MaxEnvironments {
		return &placementCapacityError{detail: fmt.Sprintf("context %q is at capacity: %d of %d environments already placed; raise its maxEnvironments or choose another context", cloudContext.Name, count, cloudContext.MaxEnvironments)}
	}
	return nil
}

// writePlacementError maps a resolvePlacement failure to its HTTP status: a
// caller mistake (unknown context, a raw kubernetesContext) is a 400; no
// candidate having room (an explicit context at capacity, or the whole
// auto-select inventory exhausted) is a 409, matching the existing
// quota-exceeded shape; anything else is a repository-layer failure.
func writePlacementError(w http.ResponseWriter, err error) {
	var capacityErr *placementCapacityError
	switch {
	case errors.As(err, &capacityErr):
		writeError(w, http.StatusConflict, capacityErr.Error())
	case errors.Is(err, errPlacementContextNotFound), errors.Is(err, errCrossClusterPlacementUnsupported):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeRepositoryError(w, err)
	}
}

// resourceQuotaExceededError marks a resourceQuota re-check failure as a
// caller-facing 409 (naming which cap/budget is short), distinct from a
// repository failure while resolving the inputs, which is a 500.
type resourceQuotaExceededError struct{ detail string }

func (e *resourceQuotaExceededError) Error() string { return e.detail }

// revalidateResourceQuota re-runs the two resource-quota checks a redeploy
// must still satisfy: the per-environment floor and the tenant's aggregate
// budget (#1113). A redeploy does not add a new environment — it is already
// counted — so the aggregate projection uses the runtime count as-is, not
// +1 the way a create does.
func (r EnvironmentRoutes) revalidateResourceQuota(ctx context.Context) error {
	quota, err := r.quotas.Get(ctx)
	if err != nil {
		return err
	}
	if err := validateNamespaceQuotaFloor(quota); err != nil {
		return &resourceQuotaExceededError{detail: err.Error()}
	}
	runtimeCount, err := r.environments.CountByType(ctx, model.EnvironmentTypeRuntime)
	if err != nil {
		return err
	}
	if err := validateAggregateResourceBudget(runtimeCount, quota); err != nil {
		return &resourceQuotaExceededError{detail: err.Error()}
	}
	return nil
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

func (r EnvironmentRoutes) listEnvironments(w http.ResponseWriter, req *http.Request) {
	environments, err := r.environments.List(req.Context())
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, environments)
}

func (r EnvironmentRoutes) getEnvironment(w http.ResponseWriter, req *http.Request) {
	environment, err := r.environments.Get(req.Context(), req.PathValue("environment_id"))
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, environment)
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

// minRuntimeCPUMillicores/MemoryMB/StorageGB are the smallest tenant quota
// that can host a stock runtime environment, derived from
// eruncommon.MinimumRuntimeNamespaceQuota rather than restated as independent
// literals — the erun-devops chart's own runtime pod, summed across BOTH its
// containers (erun-devops + erun-dind; a ResourceQuota counts every container
// in the pod), plus its PVCs. A tenant quota below this floor cannot host a
// stock runtime environment: Kubernetes would reject the pod at admission
// once the namespace ResourceQuota is applied. Checking it before the deploy
// Job starts (validateNamespaceQuotaFloor, called from both the create and
// the deploy path) turns that into a clear 409 naming the quota and the
// shortfall, instead of a Job whose Deployment sits at 0/1 until the rollout
// times out five minutes later with the real cause in a FailedCreate event on
// a ReplicaSet the caller never sees (#1061).
var minRuntimeCPUMillicores, minRuntimeMemoryMB, minRuntimeStorageGB = func() (int, int, int) {
	cpu, memory, storage := eruncommon.MinimumRuntimeNamespaceQuota()
	return int(cpu), int(memory), int(storage)
}()

// validateNamespaceQuotaFloor rejects a tenant quota that cannot fit the
// stock runtime pod, naming which cap is insufficient and by how much.
func validateNamespaceQuotaFloor(quota model.TenantQuota) error {
	switch {
	case quota.MaxCPUMillicores < minRuntimeCPUMillicores:
		return fmt.Errorf("tenant CPU quota (%dm) is below the runtime environment's minimum (%dm), short by %dm; raise maxCpuMillicores", quota.MaxCPUMillicores, minRuntimeCPUMillicores, minRuntimeCPUMillicores-quota.MaxCPUMillicores)
	case quota.MaxMemoryMB < minRuntimeMemoryMB:
		return fmt.Errorf("tenant memory quota (%dMi) is below the runtime environment's minimum (%dMi), short by %dMi; raise maxMemoryMb", quota.MaxMemoryMB, minRuntimeMemoryMB, minRuntimeMemoryMB-quota.MaxMemoryMB)
	case quota.MaxStorageGB < minRuntimeStorageGB:
		return fmt.Errorf("tenant storage quota (%dGi) is below the runtime environment's minimum (%dGi), short by %dGi; raise maxStorageGb", quota.MaxStorageGB, minRuntimeStorageGB, minRuntimeStorageGB-quota.MaxStorageGB)
	}
	return nil
}

// validateAggregateResourceBudget refuses admitting a runtime environment
// that would push the tenant's projected total CPU/memory/storage past its
// aggregate budget (#1113). Every runtime environment gets the SAME
// per-environment cap (quota.MaxCPUMillicores etc.), so the projection is
// exact, not an estimate: projectedRuntimeCount * the per-environment cap.
// The caller computes the count: existingCount+1 for a new environment not
// yet persisted, or existingCount as-is for a redeploy of one already
// counted. This is a tenant-wide ceiling distinct from the per-environment
// floor validateNamespaceQuotaFloor checks above.
func validateAggregateResourceBudget(projected int, quota model.TenantQuota) error {
	switch {
	case projected*quota.MaxCPUMillicores > quota.MaxTotalCPUMillicores:
		return fmt.Errorf("tenant CPU budget (%dm) would be exceeded: %d runtime environment(s) at %dm each project to %dm; raise maxTotalCpuMillicores or lower maxCpuMillicores", quota.MaxTotalCPUMillicores, projected, quota.MaxCPUMillicores, projected*quota.MaxCPUMillicores)
	case projected*quota.MaxMemoryMB > quota.MaxTotalMemoryMB:
		return fmt.Errorf("tenant memory budget (%dMi) would be exceeded: %d runtime environment(s) at %dMi each project to %dMi; raise maxTotalMemoryMb or lower maxMemoryMb", quota.MaxTotalMemoryMB, projected, quota.MaxMemoryMB, projected*quota.MaxMemoryMB)
	case projected*quota.MaxStorageGB > quota.MaxTotalStorageGB:
		return fmt.Errorf("tenant storage budget (%dGi) would be exceeded: %d runtime environment(s) at %dGi each project to %dGi; raise maxTotalStorageGb or lower maxStorageGb", quota.MaxTotalStorageGB, projected, quota.MaxStorageGB, projected*quota.MaxStorageGB)
	}
	return nil
}

// environmentQuotaUsage reports how many environments the caller's tenant has
// and its full quota row. Creation enforces the env-count cap; the provision
// preview reports it; the deploy path threads the resource caps to the Job.
func environmentQuotaUsage(ctx context.Context, environments EnvironmentRepository, quotas TenantQuotaRepository) (count int, quota model.TenantQuota, err error) {
	quota, err = quotas.Get(ctx)
	if err != nil {
		return 0, model.TenantQuota{}, err
	}
	count, err = environments.Count(ctx)
	if err != nil {
		return 0, model.TenantQuota{}, err
	}
	return count, quota, nil
}

var validEnvironmentTypes = map[model.EnvironmentType]struct{}{
	model.EnvironmentTypeRuntime:     {},
	model.EnvironmentTypeRemoteAgent: {},
	model.EnvironmentTypeLocalAgent:  {},
}

// decodeCreateEnvironmentInput validates the create body and returns the
// environment it describes plus whether the caller asked for a preview.
// Its error message is the operator-facing 400 reason.
func decodeCreateEnvironmentInput(req *http.Request) (model.Environment, bool, error) {
	var body createEnvironmentRequest
	if err := decodeJSON(req, &body); err != nil {
		return model.Environment{}, false, errors.New("invalid request body")
	}
	name := strings.TrimSpace(body.Name)
	// The tenant is hyphen-free (enforced at tenant registration), so allowing
	// internal hyphens in the env keeps the first-hyphen split of the
	// <tenant>-<env> namespace unambiguous.
	if !validNamespaceLabel(name) {
		return model.Environment{}, false, errors.New("name must be a DNS-1123 label: lowercase letters, digits, and internal hyphens, not starting or ending with a hyphen, at most 63 characters")
	}
	envType := model.EnvironmentType(strings.TrimSpace(body.Type))
	if _, ok := validEnvironmentTypes[envType]; !ok {
		return model.Environment{}, false, errors.New("type must be one of runtime, remote-agent, local-agent")
	}
	return model.Environment{
		Name:              name,
		Type:              envType,
		ContextID:         strings.TrimSpace(body.ContextID),
		KubernetesContext: strings.TrimSpace(body.KubernetesContext),
		RuntimeVersion:    strings.TrimSpace(body.RuntimeVersion),
	}, body.Preview, nil
}

func (r EnvironmentRoutes) createEnvironment(w http.ResponseWriter, req *http.Request) {
	environment, preview, err := decodeCreateEnvironmentInput(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Placement (#1112) is decided once, here, and persisted: an explicit
	// contextId is validated and capacity-checked, an unset one is
	// auto-selected from the tenant's own registered contexts (or resolves to
	// the platform's own cluster when the tenant has none), and a raw
	// kubernetesContext is refused. The resolved contextId — not necessarily
	// what the caller sent — is what gets persisted and deployed.
	placement, err := r.resolvePlacement(req.Context(), environment)
	if err != nil {
		writePlacementError(w, err)
		return
	}
	environment.ContextID = placement.ContextID

	count, quota, err := environmentQuotaUsage(req.Context(), r.environments, r.quotas)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	if preview {
		r.previewCreateEnvironment(w, req, environment, count, quota)
		return
	}
	if count >= quota.MaxEnvironments {
		writeError(w, http.StatusConflict, fmt.Sprintf("environment quota reached: this tenant already has %d of %d environments", count, quota.MaxEnvironments))
		return
	}
	if environment.Type == model.EnvironmentTypeRuntime {
		if err := validateNamespaceQuotaFloor(quota); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		runtimeCount, err := r.environments.CountByType(req.Context(), model.EnvironmentTypeRuntime)
		if err != nil {
			writeRepositoryError(w, err)
			return
		}
		if err := validateAggregateResourceBudget(runtimeCount+1, quota); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
	}
	r.persistAndMaybeProvision(w, req, environment, placement)
}

// persistAndMaybeProvision creates the row and, for a runtime env with a
// pinned version and a wired provisioner, starts the durable server-side
// deploy — the non-preview tail of createEnvironment. placement is the
// decision resolvePlacement already made; created.ContextID (an FK, so a
// context belonging to another tenant is rejected here too, the enforcement
// point for tenant isolation on the reference) should always echo it back.
func (r EnvironmentRoutes) persistAndMaybeProvision(w http.ResponseWriter, req *http.Request, environment model.Environment, placement resolvedPlacement) {
	created, err := r.environments.Create(req.Context(), environment)
	if err != nil {
		writeRepositoryError(w, err)
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
		writeStartProvisioningError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, created)
}

// writeStartProvisioningError answers a failure to enqueue the durable
// workflow. The environment row stays exactly as persistAndMaybeProvision
// already left it (registered, not provisioning) — nothing was attempted, so
// nothing needs unwinding. A missing tenant runtime image is no longer this
// path: it now selects the canonical-image bootstrap instead of failing here.
func writeStartProvisioningError(w http.ResponseWriter, _ error) {
	writeError(w, http.StatusInternalServerError, "failed to start provisioning")
}

// writeStartDeployError marks the environment failed and answers 500:
// ClaimDeploy already moved the row to provisioning before startDeploy ran, so
// any failure to even enqueue the durable workflow would otherwise strand the
// environment there with no workflow run left to move it out.
func (r EnvironmentRoutes) writeStartDeployError(w http.ResponseWriter, ctx context.Context, environmentID string, err error) {
	_ = r.environments.MarkDeployFailed(ctx, environmentID, err.Error())
	writeError(w, http.StatusInternalServerError, "failed to start deploy")
}

// previewCreateEnvironment resolves and returns the same ordered plan
// POST /v1/provision renders for this environment, without creating the row.
// Since resolvePlacement already ran before this is reached, environment.ContextID
// is already the resolved placement decision, and the only way this diverges
// from what a non-preview call would do is the quota check, which — like
// /v1/provision — reports rather than 409s so the full intended plan is
// still visible.
func (r EnvironmentRoutes) previewCreateEnvironment(w http.ResponseWriter, req *http.Request, environment model.Environment, count int, quota model.TenantQuota) {
	tenant, err := r.tenants.Current(req.Context())
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	runtimeCount, err := r.environments.CountByType(req.Context(), model.EnvironmentTypeRuntime)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	plan := provisionPlanInput{
		tenantName:        strings.TrimSpace(tenant.Name),
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

// startDeploy kicks off the durable deploy workflow for an explicit deploy,
// tagged with a fresh attempt id so it is a real re-run rather than a replay of
// the environment's first deploy. It targets the cluster the environment was
// already placed on at create time (resolvePlacementCoordinates), never a
// freshly auto-selected one.
func (r EnvironmentRoutes) startDeploy(ctx context.Context, environment model.Environment, version string) error {
	placement, err := r.resolvePlacementCoordinates(ctx, environment.ContextID)
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
		TenantID:                   securityContext.TenantID,
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
