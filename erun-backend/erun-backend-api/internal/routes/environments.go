package routes

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

type EnvironmentRepository interface {
	List(ctx context.Context) ([]model.Environment, error)
	Get(ctx context.Context, environmentID string) (model.Environment, error)
	Create(ctx context.Context, environment model.Environment) (model.Environment, error)
	Count(ctx context.Context) (int, error)
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

func RegisterEnvironmentRoutes(register ProtectedRouteRegistrar, environments EnvironmentRepository, quotas TenantQuotaRepository) {
	routes := EnvironmentRoutes{environments: environments, quotas: quotas}
	register(http.MethodGet, "/v1/environments", http.HandlerFunc(routes.listEnvironments))
	register(http.MethodPost, "/v1/environments", http.HandlerFunc(routes.createEnvironment))
	register(http.MethodGet, "/v1/environments/{environment_id}", http.HandlerFunc(routes.getEnvironment))
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
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return true
}

var validEnvironmentTypes = map[model.EnvironmentType]struct{}{
	model.EnvironmentTypeRuntime:     {},
	model.EnvironmentTypeRemoteAgent: {},
	model.EnvironmentTypeLocalAgent:  {},
}

func (r EnvironmentRoutes) createEnvironment(w http.ResponseWriter, req *http.Request) {
	var body createEnvironmentRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	name := strings.TrimSpace(body.Name)
	// The tenant is hyphen-free (enforced at tenant registration), so allowing
	// internal hyphens in the env keeps the first-hyphen split of the
	// <tenant>-<env> namespace unambiguous.
	if !validNamespaceLabel(name) {
		writeError(w, http.StatusBadRequest, "name must be a DNS-1123 label: lowercase letters, digits, and internal hyphens, not starting or ending with a hyphen, at most 63 characters")
		return
	}
	envType := model.EnvironmentType(strings.TrimSpace(body.Type))
	if _, ok := validEnvironmentTypes[envType]; !ok {
		writeError(w, http.StatusBadRequest, "type must be one of runtime, remote-agent, local-agent")
		return
	}

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
		// A context belonging to another tenant is rejected here — the
		// enforcement point for tenant isolation on the context reference.
		writeRepositoryError(w, err)
		return
	}

	// TODO(live): provision the <tenant>-<env> namespace and deploy the runtime
	// chart server-side; needs a live cluster, so for now the row is only
	// registered config, not a running environment.

	writeJSON(w, http.StatusCreated, created)
}
