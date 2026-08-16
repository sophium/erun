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

type EnvironmentRepository interface {
	List(ctx context.Context) ([]model.Environment, error)
	Get(ctx context.Context, environmentID string) (model.Environment, error)
	Create(ctx context.Context, environment model.Environment) (model.Environment, error)
	Count(ctx context.Context) (int, error)
	ClaimDeploy(ctx context.Context, environmentID string, staleAfter time.Duration) (bool, error)
}

// EnvironmentProvisioner starts the durable server-side deploy of an
// environment — once on create, and again for each explicit deploy. Optional:
// when nil, the environment routes only register rows (no live deploy),
// matching the pre-executor behavior.
type EnvironmentProvisioner interface {
	Start(provision.EnvProvisionInput) error
	StartDeploy(provision.EnvProvisionInput) error
}

// deployClaimStaleAfter bounds how long a deploy may hold its environment's
// claim. It is longer than the deploy Job's own 30-minute deadline, so a claim
// only looks stale once the run behind it cannot still be live.
const deployClaimStaleAfter = 45 * time.Minute

// TenantQuotaRepository reports the caller's environment-count cap. When the
// tenant has no quota row the repository returns the default cap, so the route
// never needs to distinguish "unconfigured" from "explicit".
type TenantQuotaRepository interface {
	MaxEnvironments(ctx context.Context) (int, error)
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
}

// createEnvironmentRequest carries only operator-authored fields; the tenant is
// resolved from the caller's token, never trusted from the body.
type createEnvironmentRequest struct {
	Name              string `json:"name"`
	Type              string `json:"type"`
	ContextID         string `json:"contextId"`
	KubernetesContext string `json:"kubernetesContext"`
	RuntimeVersion    string `json:"runtimeVersion"`
}

func RegisterEnvironmentRoutes(register ProtectedRouteRegistrar, environments EnvironmentRepository, quotas TenantQuotaRepository, tenants ConfigTenantRepository, provisioner EnvironmentProvisioner) {
	routes := EnvironmentRoutes{environments: environments, quotas: quotas, tenants: tenants, provisioner: provisioner}
	register(http.MethodGet, "/v1/environments", http.HandlerFunc(routes.listEnvironments))
	register(http.MethodPost, "/v1/environments", http.HandlerFunc(routes.createEnvironment))
	register(http.MethodGet, "/v1/environments/{environment_id}", http.HandlerFunc(routes.getEnvironment))
	register(http.MethodPost, "/v1/environments/{environment_id}/deploy", http.HandlerFunc(routes.deployEnvironment))
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
		writeError(w, http.StatusInternalServerError, "failed to start deploy")
		return
	}
	writeJSON(w, http.StatusAccepted, environment)
}

// resolveDeployVersion picks the version to deploy and rejects the requests that
// have nothing deployable. Its error message is the operator-facing 400 reason.
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

// environmentQuotaUsage reports how many environments the caller's tenant has
// and its cap. Creation enforces the cap; the provision preview reports it.
func environmentQuotaUsage(ctx context.Context, environments EnvironmentRepository, quotas TenantQuotaRepository) (count int, maxEnvironments int, err error) {
	maxEnvironments, err = quotas.MaxEnvironments(ctx)
	if err != nil {
		return 0, 0, err
	}
	count, err = environments.Count(ctx)
	if err != nil {
		return 0, 0, err
	}
	return count, maxEnvironments, nil
}

var validEnvironmentTypes = map[model.EnvironmentType]struct{}{
	model.EnvironmentTypeRuntime:     {},
	model.EnvironmentTypeRemoteAgent: {},
	model.EnvironmentTypeLocalAgent:  {},
}

// decodeCreateEnvironmentInput validates the create body and returns the
// environment it describes. Its error message is the operator-facing 400 reason.
func decodeCreateEnvironmentInput(req *http.Request) (model.Environment, error) {
	var body createEnvironmentRequest
	if err := decodeJSON(req, &body); err != nil {
		return model.Environment{}, errors.New("invalid request body")
	}
	name := strings.TrimSpace(body.Name)
	// The tenant is hyphen-free (enforced at tenant registration), so allowing
	// internal hyphens in the env keeps the first-hyphen split of the
	// <tenant>-<env> namespace unambiguous.
	if !validNamespaceLabel(name) {
		return model.Environment{}, errors.New("name must be a DNS-1123 label: lowercase letters, digits, and internal hyphens, not starting or ending with a hyphen, at most 63 characters")
	}
	envType := model.EnvironmentType(strings.TrimSpace(body.Type))
	if _, ok := validEnvironmentTypes[envType]; !ok {
		return model.Environment{}, errors.New("type must be one of runtime, remote-agent, local-agent")
	}
	return model.Environment{
		Name:              name,
		Type:              envType,
		ContextID:         strings.TrimSpace(body.ContextID),
		KubernetesContext: strings.TrimSpace(body.KubernetesContext),
		RuntimeVersion:    strings.TrimSpace(body.RuntimeVersion),
	}, nil
}

func (r EnvironmentRoutes) createEnvironment(w http.ResponseWriter, req *http.Request) {
	environment, err := decodeCreateEnvironmentInput(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	count, maxEnvironments, err := environmentQuotaUsage(req.Context(), r.environments, r.quotas)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	if count >= maxEnvironments {
		writeError(w, http.StatusConflict, fmt.Sprintf("environment quota reached: this tenant already has %d of %d environments", count, maxEnvironments))
		return
	}

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
		writeError(w, http.StatusInternalServerError, "failed to start provisioning")
		return
	}
	writeJSON(w, http.StatusAccepted, created)
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
	return provision.EnvProvisionInput{
		TenantID:      securityContext.TenantID,
		TenantType:    securityContext.TenantType,
		ErunUserID:    securityContext.ErunUserID,
		EnvironmentID: environment.EnvironmentID,
		Tenant:        strings.TrimSpace(tenant.Name),
		Environment:   environment.Name,
		Version:       version,
	}, nil
}
