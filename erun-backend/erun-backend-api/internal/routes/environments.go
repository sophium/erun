package routes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/provision"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type EnvironmentRepository interface {
	List(ctx context.Context) ([]model.Environment, error)
	Get(ctx context.Context, environmentID string) (model.Environment, error)
	Create(ctx context.Context, environment model.Environment) (model.Environment, error)
	Count(ctx context.Context) (int, error)
}

// EnvironmentProvisioner starts the durable server-side deploy of a
// freshly-created environment. Optional: when nil, POST /v1/environments only
// registers the row (no live deploy), matching the pre-executor behavior.
type EnvironmentProvisioner interface {
	Start(provision.EnvProvisionInput) error
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

// startProvisioning kicks off the durable deploy workflow for a runtime env. The
// deploy needs the tenant name (RLS-scoped to the caller, so always the caller's
// own) plus the request-scoped identity so the workflow's status writes rebind
// to the right tenant.
func (r EnvironmentRoutes) startProvisioning(ctx context.Context, created model.Environment) error {
	tenant, err := r.tenants.Current(ctx)
	if err != nil {
		return err
	}
	securityContext, ok := security.FromContext(ctx)
	if !ok {
		return fmt.Errorf("missing security context")
	}
	return r.provisioner.Start(provision.EnvProvisionInput{
		TenantID:      securityContext.TenantID,
		TenantType:    securityContext.TenantType,
		ErunUserID:    securityContext.ErunUserID,
		EnvironmentID: created.EnvironmentID,
		Tenant:        strings.TrimSpace(tenant.Name),
		Environment:   created.Name,
		Version:       created.RuntimeVersion,
	})
}
