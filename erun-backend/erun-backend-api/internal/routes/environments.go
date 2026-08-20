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
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type EnvironmentRepository interface {
	List(ctx context.Context) ([]model.Environment, error)
	Get(ctx context.Context, environmentID string) (model.Environment, error)
	Create(ctx context.Context, environment model.Environment) (model.Environment, error)
	Count(ctx context.Context) (int, error)
	ClaimDeploy(ctx context.Context, environmentID string, staleAfter time.Duration) (bool, error)
	// MarkDeployFailed records a deploy claim that never reached the durable
	// workflow (see writeStartProvisioningError), so the environment does not
	// stay stranded in provisioning.
	MarkDeployFailed(ctx context.Context, environmentID, reason string) error
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

func RegisterEnvironmentRoutes(register ProtectedRouteRegistrar, environments EnvironmentRepository, quotas TenantQuotaRepository, tenants ConfigTenantRepository, provisioner EnvironmentProvisioner, lifecycle EnvironmentLifecycle) {
	routes := EnvironmentRoutes{environments: environments, quotas: quotas, tenants: tenants, provisioner: provisioner, lifecycle: lifecycle}
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
// name (RLS-scoped to the caller) and the version the environment last
// actually deployed, mirroring deployInput's tenant resolution.
func (r EnvironmentRoutes) lifecycleInput(ctx context.Context, environment model.Environment) (provision.EnvLifecycleInput, error) {
	tenant, err := r.tenants.Current(ctx)
	if err != nil {
		return provision.EnvLifecycleInput{}, err
	}
	version := environment.DeployedVersion
	if version == "" {
		version = environment.RuntimeVersion
	}
	return provision.EnvLifecycleInput{
		Tenant:         strings.TrimSpace(tenant.Name),
		Environment:    environment.Name,
		EnvironmentID:  environment.EnvironmentID,
		RunningVersion: version,
	}, nil
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
	// floor. Failing here is a clear 409 instead of the Job's Deployment
	// sitting at 0/1 until the rollout times out (#1061).
	quota, err := r.quotas.Get(ctx)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	if err := validateNamespaceQuotaFloor(quota); err != nil {
		writeError(w, http.StatusConflict, err.Error())
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

// errCrossClusterPlacementUnsupported is the actionable v1 refusal for a
// runtime environment that names a cluster context: the deploy Job has no
// mechanism to target any cluster but the one the control plane itself runs
// in (see deployexec.Launcher and provision.deployJobParams, which never
// reference a context at all), so honoring a different one would silently
// deploy to the wrong place instead. Refusing at request time — rather than
// accepting the field and failing the async deploy later — gives the caller
// the answer synchronously instead of a poll loop's eventual "failed" status.
var errCrossClusterPlacementUnsupported = errors.New(
	"deploying into a specific cluster context is not supported yet: this platform can only deploy runtime environments into its own cluster (v1 single-cluster placement); leave context/kubernetesContext unset",
)

// resolveDeployPlacement is the explicit v1 single-cluster placement decision
// (#605): a runtime environment that names a context or kubernetes context is
// refused, rather than the field being silently accepted and ignored. Only
// applies to runtime environments — the only type this platform ever
// server-side deploys.
func resolveDeployPlacement(environment model.Environment) error {
	if environment.Type != model.EnvironmentTypeRuntime {
		return nil
	}
	if strings.TrimSpace(environment.ContextID) != "" || strings.TrimSpace(environment.KubernetesContext) != "" {
		return errCrossClusterPlacementUnsupported
	}
	return nil
}

// resolveDeployVersion picks the version to deploy and rejects the requests that
// have nothing deployable. Its error message is the operator-facing 400 reason.
func resolveDeployVersion(req *http.Request, environment model.Environment) (string, error) {
	if environment.Type != model.EnvironmentTypeRuntime {
		return "", errors.New("only a runtime environment can be deployed")
	}
	if err := resolveDeployPlacement(environment); err != nil {
		return "", err
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
	if err := resolveDeployPlacement(environment); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

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
	}
	r.persistAndMaybeProvision(w, req, environment)
}

// persistAndMaybeProvision creates the row and, for a runtime env with a
// pinned version and a wired provisioner, starts the durable server-side
// deploy — the non-preview tail of createEnvironment.
func (r EnvironmentRoutes) persistAndMaybeProvision(w http.ResponseWriter, req *http.Request, environment model.Environment) {
	created, err := r.environments.Create(req.Context(), environment)
	if err != nil {
		// A context belonging to another tenant is rejected here — the
		// enforcement point for tenant isolation on the context reference.
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
	if err := r.startProvisioning(req.Context(), created); err != nil {
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
// Since resolveDeployPlacement already ran before this is reached, the only
// way this diverges from what a non-preview call would do is the quota
// check, which — like /v1/provision — reports rather than 409s so the full
// intended plan is still visible.
func (r EnvironmentRoutes) previewCreateEnvironment(w http.ResponseWriter, req *http.Request, environment model.Environment, count int, quota model.TenantQuota) {
	tenant, err := r.tenants.Current(req.Context())
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	contextRef := environment.KubernetesContext
	if contextRef == "" {
		contextRef = environment.ContextID
	}
	plan := provisionPlanInput{
		tenantName:        strings.TrimSpace(tenant.Name),
		envName:           environment.Name,
		envType:           environment.Type,
		kubernetesContext: contextRef,
		count:             count,
		quota:             quota,
	}
	writeJSON(w, http.StatusOK, provisionResponse{Plan: provisionPlan(plan), QuotaOk: plan.quotaOk()})
}

// startProvisioning kicks off the durable deploy workflow for a newly-created
// runtime env, keyed by the environment so a retried create never double-deploys.
func (r EnvironmentRoutes) startProvisioning(ctx context.Context, created model.Environment) error {
	input, err := r.deployInput(ctx, created, created.RuntimeVersion)
	if err != nil {
		return err
	}
	return r.provisioner.Start(input)
}

// startDeploy kicks off the durable deploy workflow for an explicit deploy,
// tagged with a fresh attempt id so it is a real re-run rather than a replay of
// the environment's first deploy.
func (r EnvironmentRoutes) startDeploy(ctx context.Context, environment model.Environment, version string) error {
	input, err := r.deployInput(ctx, environment, version)
	if err != nil {
		return err
	}
	input.DeployID = uuid.NewString()
	return r.provisioner.StartDeploy(input)
}

// deployInput assembles the durable workflow input. The deploy needs the tenant
// name (RLS-scoped to the caller, so always the caller's own) plus the
// request-scoped identity so the workflow's status writes rebind to the right
// tenant.
func (r EnvironmentRoutes) deployInput(ctx context.Context, environment model.Environment, version string) (provision.EnvProvisionInput, error) {
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
		TenantID:         securityContext.TenantID,
		TenantType:       securityContext.TenantType,
		ErunUserID:       securityContext.ErunUserID,
		EnvironmentID:    environment.EnvironmentID,
		Tenant:           strings.TrimSpace(tenant.Name),
		Environment:      environment.Name,
		Version:          version,
		MaxCPUMillicores: quota.MaxCPUMillicores,
		MaxMemoryMB:      quota.MaxMemoryMB,
		MaxStorageGB:     quota.MaxStorageGB,
	}, nil
}
