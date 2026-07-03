package routes

import (
	"fmt"
	"net/http"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

// ProvisionRoutes serves the combined "provision a hosted env" preview: the
// single auditable plan the operator/console sees before provisioning. It
// composes the same dry-run primitives the discrete endpoints expose — the
// per-tenant quota check (POST /v1/environments), the cluster-bootstrap plan
// (POST /v1/contexts), the namespace + env registration, and the runtime
// deploy — into one ordered, human-readable plan. This endpoint is plan-only:
// it resolves and shows the concrete actions but never executes them and never
// writes to the database.
type ProvisionRoutes struct {
	tenants      ConfigTenantRepository
	environments EnvironmentRepository
	quotas       TenantQuotaRepository
}

// provisionRequest is the operator-authored "provision a hosted env" body. The
// tenant is resolved from the caller's token (the security context), never from
// the body. Exactly one of context (provision a NEW cluster) or
// kubernetesContext (reference an EXISTING context) describes where the env
// lands; environment is always required.
type provisionRequest struct {
	Environment provisionEnvironment `json:"environment"`
	// Context, when present, provisions a NEW cluster: its bootstrap plan is the
	// real InitCloudContext dry-run argv, the same plan POST /v1/contexts returns.
	Context *provisionContext `json:"context,omitempty"`
	// KubernetesContext references an EXISTING context instead of bootstrapping a
	// new cluster. Ignored when a context block is present.
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

// provisionResponse is the preview result: the ordered, audit-style plan and
// whether the tenant is within its environment quota. quotaOk is false when the
// provision would exceed the cap; the plan is still returned in full (a preview
// surfaces the blocking decision rather than 4xx-ing it).
type provisionResponse struct {
	Plan    []string `json:"plan"`
	QuotaOk bool     `json:"quotaOk"`
}

func RegisterProvisionRoute(register ProtectedRouteRegistrar, tenants ConfigTenantRepository, environments EnvironmentRepository, quotas TenantQuotaRepository) {
	routes := ProvisionRoutes{tenants: tenants, environments: environments, quotas: quotas}
	register(http.MethodPost, "/v1/provision", http.HandlerFunc(routes.provision))
}

// provision resolves and returns the complete, ordered plan to provision a
// hosted env for the caller's tenant. It composes the quota check, context
// (cluster) bootstrap, namespace creation, env registration, and runtime
// deploy into one auditable preview. It NEVER executes the plan and NEVER
// writes to the database — this endpoint is plan-only in this build (see the
// TODO(live) below for the real orchestration).
func (r ProvisionRoutes) provision(w http.ResponseWriter, req *http.Request) {
	var body provisionRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	envName := strings.TrimSpace(body.Environment.Name)
	// The env name forms the <tenant>-<env> namespace, so it must be a DNS-1123
	// label (same guardrail as POST /v1/environments). The tenant is hyphen-free
	// (ValidateTenantName at tenant registration), so the first-hyphen split of
	// the namespace stays unambiguous (injective-namespace guardrail).
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
		// Reuse the same dry-run cluster-bootstrap plan POST /v1/contexts builds:
		// it runs eruncommon.InitCloudContext in DryRun mode against an in-memory
		// CloudStore seeded with the alias, so it resolves the provider and emits
		// the EC2/IAM/k3s argv the real bootstrap would run, without touching AWS.
		plan, err := buildContextBootstrapPlan(createContextRequest{
			InstanceType: strings.TrimSpace(body.Context.InstanceType),
			DiskType:     strings.TrimSpace(body.Context.DiskType),
			DiskSizeGB:   body.Context.DiskSizeGB,
		}, ctxName, ctxAlias, ctxRegion)
		if err != nil {
			// A dry-run InitCloudContext failure is an input-resolution error
			// (e.g. an unsupported region/instance type), not a server fault.
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		contextPlan = plan
	}

	// The tenant name (not the UUID) is the human identity that forms the
	// <tenant>-<env> namespace and the runtime release name. It is read RLS-/
	// security-scoped to the caller, so it always belongs to the caller's tenant.
	tenant, err := r.tenants.Current(req.Context())
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	tenantName := strings.TrimSpace(tenant.Name)

	plan := make([]string, 0, 8+len(contextPlan))

	// 1. authz/tenant: the tenant the whole plan is scoped to, resolved from the
	//    token's security context (never from the body).
	plan = append(plan, fmt.Sprintf("provision: tenant %s (resolved from token)", tenantName))

	// 2. quota: read the cap + current count and state whether this provision
	//    fits. Over the cap, the plan is still returned (preview), but quotaOk is
	//    false and the line names the block — consistent with a dry-run surfacing
	//    decisions rather than 4xx-ing them.
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

	// 3. context: either bootstrap a NEW cluster (append the InitCloudContext
	//    dry-run argv under a header line) or reuse an EXISTING context.
	if body.Context != nil {
		plan = append(plan, fmt.Sprintf("context: bootstrap cluster %s via alias %s", ctxName, ctxAlias))
		plan = append(plan, contextPlan...)
	} else {
		plan = append(plan, fmt.Sprintf("context: reuse existing kubernetes context %s", strings.TrimSpace(body.KubernetesContext)))
	}

	// 4. namespace: the <tenant>-<env> namespace the env runs in.
	namespace := eruncommon.KubernetesNamespaceName(tenantName, envName)
	plan = append(plan, fmt.Sprintf("namespace: would create %s", namespace))

	// 5. register: persist the env row (config-only; the live deploy is step 6).
	contextRef := strings.TrimSpace(body.KubernetesContext)
	if body.Context != nil {
		contextRef = ctxName
	}
	plan = append(plan, fmt.Sprintf("register: would persist environment %s (%s) in tenant %s referencing context %s", envName, envType, tenantName, contextRef))

	// 6. deploy: helm-install the runtime chart for the tenant into the namespace.
	plan = append(plan, fmt.Sprintf("deploy: would helm install the erun-devops runtime chart (release %s) into %s", eruncommon.RuntimeReleaseName(tenantName), namespace))

	// TODO(live): execute this plan — InitCloudContext (DryRun=false) →
	// ensure namespace → RunBootstrapInitWithDependencies/deploy the runtime
	// chart → wire exposure + the auth edge. Requires a live AWS account +
	// cluster; this endpoint is plan-only in this build. Do not persist here —
	// the discrete POST /v1/contexts and POST /v1/environments endpoints own the
	// config writes; this preview composes their plans without side effects.

	writeJSON(w, http.StatusOK, provisionResponse{Plan: plan, QuotaOk: quotaOk})
}
