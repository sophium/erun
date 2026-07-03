package routes

import (
	"fmt"
	"net/http"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

// ProvisionRoutes serves the combined "provision a hosted env" preview: the
// single auditable plan the operator sees before provisioning. Plan-only — it
// resolves and shows the concrete actions but never executes them and never
// writes to the database.
type ProvisionRoutes struct {
	tenants      ConfigTenantRepository
	environments EnvironmentRepository
	quotas       TenantQuotaRepository
}

// provisionRequest carries the "provision a hosted env" inputs. The tenant is
// resolved from the caller's token, never from the body. Exactly one of context
// (a NEW cluster) or kubernetesContext (an EXISTING context) describes where the
// env lands.
type provisionRequest struct {
	Environment provisionEnvironment `json:"environment"`
	Context     *provisionContext    `json:"context,omitempty"`
	// Ignored when a context block is present.
	KubernetesContext string `json:"kubernetesContext"`
}

type provisionEnvironment struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type provisionContext struct {
	Name               string `json:"name"`
	CloudProviderAlias string `json:"cloudProviderAlias"`
	Region             string `json:"region"`
	InstanceType       string `json:"instanceType"`
	DiskType           string `json:"diskType"`
	DiskSizeGB         int    `json:"diskSizeGb"`
}

// provisionResponse is the preview result. quotaOk is false when the provision
// would exceed the cap, but the plan is still returned in full — a preview
// surfaces the blocking decision rather than 4xx-ing it.
type provisionResponse struct {
	Plan    []string `json:"plan"`
	QuotaOk bool     `json:"quotaOk"`
}

func RegisterProvisionRoute(register ProtectedRouteRegistrar, tenants ConfigTenantRepository, environments EnvironmentRepository, quotas TenantQuotaRepository) {
	routes := ProvisionRoutes{tenants: tenants, environments: environments, quotas: quotas}
	register(http.MethodPost, "/v1/provision", http.HandlerFunc(routes.provision))
}

func (r ProvisionRoutes) provision(w http.ResponseWriter, req *http.Request) {
	var body provisionRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	envName := strings.TrimSpace(body.Environment.Name)
	// The env name forms the <tenant>-<env> namespace, so it must be a DNS-1123
	// label. Tenants are hyphen-free, so the first-hyphen split of the namespace
	// stays unambiguous.
	if !validNamespaceLabel(envName) {
		writeError(w, http.StatusBadRequest, "environment.name must be a DNS-1123 label: lowercase letters, digits, and internal hyphens, not starting or ending with a hyphen, at most 63 characters")
		return
	}
	envType := model.EnvironmentType(strings.TrimSpace(body.Environment.Type))
	if _, ok := validEnvironmentTypes[envType]; !ok {
		writeError(w, http.StatusBadRequest, "environment.type must be one of runtime, remote-agent, local-agent")
		return
	}

	var (
		ctxName     string
		ctxAlias    string
		ctxRegion   string
		contextPlan []string
	)
	if body.Context != nil {
		ctxName = strings.TrimSpace(body.Context.Name)
		ctxAlias = strings.TrimSpace(body.Context.CloudProviderAlias)
		ctxRegion = strings.TrimSpace(body.Context.Region)
		if ctxName == "" || ctxAlias == "" || ctxRegion == "" {
			writeError(w, http.StatusBadRequest, "context.name, context.cloudProviderAlias, and context.region are required when a context block is present")
			return
		}
		plan, err := buildContextBootstrapPlan(createContextRequest{
			InstanceType: strings.TrimSpace(body.Context.InstanceType),
			DiskType:     strings.TrimSpace(body.Context.DiskType),
			DiskSizeGB:   body.Context.DiskSizeGB,
		}, ctxName, ctxAlias, ctxRegion)
		if err != nil {
			// A dry-run bootstrap failure is an input-resolution error (e.g. an
			// unsupported region/instance type), not a server fault.
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		contextPlan = plan
	}

	// The tenant name (not the UUID) forms the <tenant>-<env> namespace and the
	// runtime release name; it is RLS-scoped to the caller, so it always belongs
	// to the caller's tenant.
	tenant, err := r.tenants.Current(req.Context())
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	tenantName := strings.TrimSpace(tenant.Name)

	plan := make([]string, 0, 8+len(contextPlan))

	plan = append(plan, fmt.Sprintf("provision: tenant %s (resolved from token)", tenantName))

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
	quotaOk := count < maxEnvironments
	quotaLine := fmt.Sprintf("quota: tenant has %d of %d environments", count, maxEnvironments)
	if quotaOk {
		quotaLine += " — within quota"
	} else {
		quotaLine += " — WOULD EXCEED, provisioning blocked"
	}
	plan = append(plan, quotaLine)

	if body.Context != nil {
		plan = append(plan, fmt.Sprintf("context: bootstrap cluster %s via alias %s", ctxName, ctxAlias))
		plan = append(plan, contextPlan...)
	} else {
		plan = append(plan, fmt.Sprintf("context: reuse existing kubernetes context %s", strings.TrimSpace(body.KubernetesContext)))
	}

	namespace := eruncommon.KubernetesNamespaceName(tenantName, envName)
	plan = append(plan, fmt.Sprintf("namespace: would create %s", namespace))

	contextRef := strings.TrimSpace(body.KubernetesContext)
	if body.Context != nil {
		contextRef = ctxName
	}
	plan = append(plan, fmt.Sprintf("register: would persist environment %s (%s) in tenant %s referencing context %s", envName, envType, tenantName, contextRef))

	plan = append(plan, fmt.Sprintf("deploy: would helm install the erun-devops runtime chart (release %s) into %s", eruncommon.RuntimeReleaseName(tenantName), namespace))

	// TODO(live): execute this plan (cluster bootstrap → namespace → runtime
	// deploy → exposure + auth edge); needs a live AWS account + cluster. Do not
	// persist here — the discrete POST /v1/contexts and POST /v1/environments
	// endpoints own the config writes; this preview composes their plans without
	// side effects.

	writeJSON(w, http.StatusOK, provisionResponse{Plan: plan, QuotaOk: quotaOk})
}
