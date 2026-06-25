package routes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/deploy"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type EnvironmentRepository interface {
	List(ctx context.Context) ([]model.Environment, error)
	Get(ctx context.Context, environmentID string) (model.Environment, error)
	Create(ctx context.Context, environment model.Environment) (model.Environment, error)
	Count(ctx context.Context) (int, error)
	UpdateDeployResult(ctx context.Context, environmentID, status, deployedVersion, deployError string) error
}

// EnvironmentDeployer starts the durable runtime deploy of an environment into
// its provisioned context (issue #680). Optional: when nil, POST
// /v1/environments/{id}/deploy returns 501 — the row stands as registered
// config with no live deploy (the pre-#680 behaviour).
type EnvironmentDeployer interface {
	Start(deploy.DeployInput) error
}

// TenantQuotaRepository reports the caller's environment-count cap. When the
// tenant has no quota row the repository returns the default cap, so the route
// never needs to distinguish "unconfigured" from "explicit".
type TenantQuotaRepository interface {
	MaxEnvironments(ctx context.Context) (int, error)
}

type EnvironmentRoutes struct {
	environments EnvironmentRepository
	quotas       TenantQuotaRepository
	contexts     ContextRepository
	deployer     EnvironmentDeployer
}

// createEnvironmentRequest is the env-registration body. The tenant is resolved
// from the caller's token (RLS scopes the write), never from the body, so it is
// absent here. The env references its context by contextId; the composite
// foreign key enforces that the context belongs to the caller's tenant.
type createEnvironmentRequest struct {
	Name              string `json:"name"`
	Type              string `json:"type"`
	ContextID         string `json:"contextId"`
	KubernetesContext string `json:"kubernetesContext"`
	RuntimeVersion    string `json:"runtimeVersion"`
}

func RegisterEnvironmentRoutes(register ProtectedRouteRegistrar, environments EnvironmentRepository, quotas TenantQuotaRepository, contexts ContextRepository, deployer EnvironmentDeployer) {
	routes := EnvironmentRoutes{environments: environments, quotas: quotas, contexts: contexts, deployer: deployer}
	register(http.MethodGet, "/v1/environments", http.HandlerFunc(routes.listEnvironments))
	register(http.MethodPost, "/v1/environments", http.HandlerFunc(routes.createEnvironment))
	register(http.MethodGet, "/v1/environments/{environment_id}", http.HandlerFunc(routes.getEnvironment))
	register(http.MethodPost, "/v1/environments/{environment_id}/deploy", http.HandlerFunc(routes.deployEnvironment))
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
// env component of the <tenant>-<env> runtime namespace: lowercase letters,
// digits, and internal hyphens, not hyphen-bounded, at most 63 characters.
func validNamespaceLabel(s string) bool {
	if s == "" || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return true
}

// validEnvironmentTypes is the closed set of env types the registration API
// accepts, matching model.EnvironmentType.
var validEnvironmentTypes = map[model.EnvironmentType]struct{}{
	model.EnvironmentTypeRuntime:     {},
	model.EnvironmentTypeRemoteAgent: {},
	model.EnvironmentTypeLocalAgent:  {},
}

// createEnvironment registers an environment in the caller's tenant (resolved
// from the token; RLS scopes the write) and returns the persisted row. It
// validates the operator-authored input, then persists; the env references its
// context by contextId, and the composite foreign key enforces that the context
// belongs to the caller's tenant.
func (r EnvironmentRoutes) createEnvironment(w http.ResponseWriter, req *http.Request) {
	var body createEnvironmentRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	name := strings.TrimSpace(body.Name)
	// The env name forms the <tenant>-<env> namespace, so it must be a DNS-1123
	// label. The tenant is already hyphen-free (ValidateTenantName on tenant
	// registration), so the env may itself contain internal hyphens and the
	// first-hyphen split stays unambiguous (#605 injective-namespace guardrail).
	if !validNamespaceLabel(name) {
		writeError(w, http.StatusBadRequest, "name must be a DNS-1123 label: lowercase letters, digits, and internal hyphens, not starting or ending with a hyphen, at most 63 characters")
		return
	}
	envType := model.EnvironmentType(strings.TrimSpace(body.Type))
	if _, ok := validEnvironmentTypes[envType]; !ok {
		writeError(w, http.StatusBadRequest, "type must be one of runtime, remote-agent, local-agent")
		return
	}

	// Enforce the per-tenant environment-count quota before persisting. The cap
	// is the tenant's tenant_quotas.max_environments (default 10 when no row
	// exists); the count is the tenant's existing environments. Both reads are
	// RLS-scoped to the caller's tenant. At or over the cap, reject with 409 and
	// do not run Create.
	maxEnvironments, err := r.quotas.MaxEnvironments(req.Context())
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	count, err := r.environments.Count(req.Context())
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	if count >= maxEnvironments {
		writeError(w, http.StatusConflict, fmt.Sprintf("environment quota reached: this tenant already has %d of %d environments", count, maxEnvironments))
		return
	}

	created, err := r.environments.Create(req.Context(), model.Environment{
		Name:              name,
		Type:              envType,
		ContextID:         strings.TrimSpace(body.ContextID),
		KubernetesContext: strings.TrimSpace(body.KubernetesContext),
		RuntimeVersion:    strings.TrimSpace(body.RuntimeVersion),
	})
	if err != nil {
		// A context_id that does not belong to the caller's tenant violates the
		// composite (tenant_id, context_id) foreign key; surface it as the
		// repository error (a clean 4xx/5xx) rather than leaking the SQL detail.
		writeRepositoryError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, created)
}

// deployEnvironmentRequest is the deploy-action body. version is optional: when
// absent the env's persisted runtimeVersion is used. The version is an explicit
// input, never minted here — deploy installs an already-pushed version (root
// AGENTS.md § "Command primitives vs orchestration").
type deployEnvironmentRequest struct {
	Version string `json:"version"`
}

// deployEnvironment starts the durable runtime deploy of an environment into its
// provisioned context (issue #680). It validates that the env has a running
// context and a resolvable version, optimistically flips the env to
// deploy_status="deploying" so the console reflects it immediately, then kicks
// off the durable DBOS workflow asynchronously (it runs for minutes, so the
// handler must not block) and returns 202 Accepted; the caller polls GET
// /v1/environments/{id} for deploy status. The executor composes the pure
// deploy primitive and threads the explicit version; it never builds or pushes.
func (r EnvironmentRoutes) deployEnvironment(w http.ResponseWriter, req *http.Request) {
	if r.deployer == nil {
		writeError(w, http.StatusNotImplemented, "live deploy is not configured")
		return
	}

	environment, err := r.environments.Get(req.Context(), req.PathValue("environment_id"))
	if err != nil {
		writeRepositoryError(w, err)
		return
	}

	// The deploy body is optional; an empty body means "use the env's version".
	var body deployEnvironmentRequest
	if err := decodeJSON(req, &body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	version := strings.TrimSpace(body.Version)
	if version == "" {
		version = strings.TrimSpace(environment.RuntimeVersion)
	}
	if version == "" {
		writeError(w, http.StatusBadRequest, "runtime version is required: set the environment's runtimeVersion or pass version in the body")
		return
	}
	if strings.TrimSpace(environment.ContextID) == "" {
		writeError(w, http.StatusBadRequest, "environment has no context to deploy into")
		return
	}

	// Fast precondition check: the context must be provisioned (running). The
	// executor re-checks this inside its step against the current row, but failing
	// here gives the operator a clear 409 instead of a deploying -> failed flicker.
	cloudContext, err := r.contexts.Get(req.Context(), environment.ContextID)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	if cloudContext.Status != "running" {
		writeError(w, http.StatusConflict, fmt.Sprintf("context is not provisioned (status %q)", cloudContext.Status))
		return
	}

	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
		return
	}

	// Optimistically reflect the in-flight deploy so a poll right after the 202
	// sees "deploying" rather than the prior status.
	if err := r.environments.UpdateDeployResult(req.Context(), environment.EnvironmentID, "deploying", "", ""); err != nil {
		writeRepositoryError(w, err)
		return
	}
	if err := r.deployer.Start(deploy.DeployInput{
		TenantID:      securityContext.TenantID,
		TenantType:    securityContext.TenantType,
		ErunUserID:    securityContext.ErunUserID,
		EnvironmentID: environment.EnvironmentID,
		Version:       version,
	}); err != nil {
		// Do not leave the env stuck in "deploying" if the workflow never started.
		_ = r.environments.UpdateDeployResult(req.Context(), environment.EnvironmentID, "failed", "", "failed to start deploy")
		writeError(w, http.StatusInternalServerError, "failed to start deploy")
		return
	}

	// Return the env reflecting the in-flight deploy (UpdateDeployResult cleared
	// any prior deployed_version / error).
	environment.DeployStatus = "deploying"
	environment.DeployedVersion = ""
	environment.DeployError = ""
	writeJSON(w, http.StatusAccepted, environment)
}
