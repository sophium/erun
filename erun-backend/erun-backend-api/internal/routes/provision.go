package routes

import (
	"errors"
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

	envName, envType, err := validateProvisionEnvironment(body.Environment)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	newCluster, contextPlan, err := resolveProvisionContext(body.Context)
	if err != nil {
		// A dry-run bootstrap failure is an input-resolution error (e.g. an
		// unsupported region/instance type), not a server fault.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Reuses the one placement decision that still applies identically to both
	// this preview and the executing POST /v1/environments (#1112): a raw
	// kubernetesContext name has no known credential to authenticate with, so
	// it stays refused everywhere. This preview has no contextId field of its
	// own (it composes a fresh context bootstrap inline via the context
	// block, or names nothing), so the capacity-aware placement decision
	// EnvironmentRoutes.resolvePlacement makes has nothing further to preview
	// here.
	placementContext := strings.TrimSpace(body.KubernetesContext)
	if newCluster != nil {
		placementContext = newCluster.name
	}
	if err := validateProvisionPlacement(envType, placementContext); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// The tenant name (not the UUID) forms the <tenant>-<env> namespace and the
	// runtime release name; it is RLS-scoped to the caller, so it always belongs
	// to the caller's tenant.
	tenant, err := r.tenants.Current(req.Context())
	if err != nil {
		writeRepositoryError(w, err)
		return
	}

	count, quota, err := environmentQuotaUsage(req.Context(), r.environments, r.quotas)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	runtimeCount, err := r.environments.CountByType(req.Context(), model.EnvironmentTypeRuntime)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}

	// TODO(live): execute this plan (cluster bootstrap → namespace → runtime
	// deploy → exposure + auth edge); needs a live AWS account + cluster. Do not
	// persist here — the discrete POST /v1/contexts and POST /v1/environments
	// endpoints own the config writes; this preview composes their plans without
	// side effects.
	preview := provisionPlanInput{
		tenantName:        strings.TrimSpace(tenant.Name),
		envName:           envName,
		envType:           envType,
		newCluster:        newCluster,
		contextPlan:       contextPlan,
		kubernetesContext: strings.TrimSpace(body.KubernetesContext),
		count:             count,
		runtimeCount:      runtimeCount,
		quota:             quota,
	}
	writeJSON(w, http.StatusOK, provisionResponse{Plan: provisionPlan(preview), QuotaOk: preview.quotaOk()})
}

// validateProvisionEnvironment returns the requested env name and type. Its error
// message is the operator-facing 400 reason.
func validateProvisionEnvironment(environment provisionEnvironment) (string, model.EnvironmentType, error) {
	envName := strings.TrimSpace(environment.Name)
	// The env name forms the <tenant>-<env> namespace, so it must be a DNS-1123
	// label. Tenants are hyphen-free, so the first-hyphen split of the namespace
	// stays unambiguous.
	if !validNamespaceLabel(envName) {
		return "", "", errors.New("environment.name must be a DNS-1123 label: lowercase letters, digits, and internal hyphens, not starting or ending with a hyphen, at most 63 characters")
	}
	envType := model.EnvironmentType(strings.TrimSpace(environment.Type))
	if _, ok := validEnvironmentTypes[envType]; !ok {
		return "", "", errors.New("environment.type must be one of runtime, remote-agent, local-agent")
	}
	return envName, envType, nil
}

// validateProvisionPlacement mirrors the "a raw kubernetesContext name is
// refused" half of EnvironmentRoutes.resolvePlacement (#1112): naming an
// existing cluster is by contextId (POST /v1/environments), never a bare
// kubernetesContext string, which has no known credential to authenticate
// with. Only runtime environments are ever server-side deployed.
func validateProvisionPlacement(envType model.EnvironmentType, kubernetesContext string) error {
	if envType != model.EnvironmentTypeRuntime {
		return nil
	}
	if strings.TrimSpace(kubernetesContext) != "" {
		return errCrossClusterPlacementUnsupported
	}
	return nil
}

// resolveProvisionContext plans the new cluster a context block asks for. Both
// results are empty for an existing-context provision, which bootstraps nothing.
func resolveProvisionContext(block *provisionContext) (*contextBootstrapParams, []string, error) {
	if block == nil {
		return nil, nil, nil
	}
	bootstrap := contextBootstrapParams{
		name:         strings.TrimSpace(block.Name),
		alias:        strings.TrimSpace(block.CloudProviderAlias),
		region:       strings.TrimSpace(block.Region),
		instanceType: strings.TrimSpace(block.InstanceType),
		diskType:     strings.TrimSpace(block.DiskType),
		diskSizeGB:   block.DiskSizeGB,
	}
	if bootstrap.name == "" || bootstrap.alias == "" || bootstrap.region == "" {
		return nil, nil, errors.New("context.name, context.cloudProviderAlias, and context.region are required when a context block is present")
	}
	contextPlan, err := buildContextBootstrapPlan(bootstrap)
	if err != nil {
		return nil, nil, err
	}
	return &bootstrap, contextPlan, nil
}

// provisionPlanInput is the resolved provision, ready to render as a plan.
// newCluster is nil when the env lands on an existing kubernetes context.
type provisionPlanInput struct {
	tenantName        string
	envName           string
	envType           model.EnvironmentType
	newCluster        *contextBootstrapParams
	contextPlan       []string
	kubernetesContext string
	count             int
	// runtimeCount is the tenant's existing runtime-environment count, for
	// the aggregate resource-budget projection (#1113); unused for a
	// non-runtime environment.
	runtimeCount int
	quota        model.TenantQuota
}

func (in provisionPlanInput) quotaOk() bool {
	if in.count >= in.quota.MaxEnvironments {
		return false
	}
	if in.envType == model.EnvironmentTypeRuntime {
		if validateNamespaceQuotaFloor(in.quota) != nil {
			return false
		}
		if validateAggregateResourceBudget(in.runtimeCount+1, in.quota) != nil {
			return false
		}
	}
	return true
}

// provisionPlan renders the ordered preview: authz (the resolved tenant),
// quota, placement, namespace, registration, and — for a runtime environment
// only, since that is the only type this platform ever server-side deploys —
// the deploy, its auth-edge wiring, and its exposure (DNS + Ingress for the
// env's MCP edge). This is the single plan renderer both POST /v1/provision
// and POST /v1/environments?preview=true call, so the plan an operator audits
// can never diverge from what the executing path does.
func provisionPlan(in provisionPlanInput) []string {
	plan := make([]string, 0, 8+len(in.contextPlan))

	plan = append(plan, fmt.Sprintf("provision: tenant %s (resolved from token)", in.tenantName))

	quotaLine := fmt.Sprintf("quota: tenant has %d of %d environments", in.count, in.quota.MaxEnvironments)
	if in.count < in.quota.MaxEnvironments {
		quotaLine += " — within quota"
	} else {
		quotaLine += " — WOULD EXCEED, provisioning blocked"
	}
	plan = append(plan, quotaLine)
	if in.envType == model.EnvironmentTypeRuntime {
		floorLine := fmt.Sprintf("quota: namespace capped at %dm CPU / %dMi memory / %dGi storage", in.quota.MaxCPUMillicores, in.quota.MaxMemoryMB, in.quota.MaxStorageGB)
		if err := validateNamespaceQuotaFloor(in.quota); err != nil {
			floorLine += fmt.Sprintf(" — BELOW RUNTIME MINIMUM, provisioning blocked: %s", err.Error())
		} else {
			floorLine += " — meets the runtime environment's minimum"
		}
		plan = append(plan, floorLine)
		projected := in.runtimeCount + 1
		budgetLine := fmt.Sprintf("quota: %d runtime environment(s) at that cap project to %dm CPU / %dMi memory / %dGi storage against a tenant budget of %dm / %dMi / %dGi",
			projected, projected*in.quota.MaxCPUMillicores, projected*in.quota.MaxMemoryMB, projected*in.quota.MaxStorageGB,
			in.quota.MaxTotalCPUMillicores, in.quota.MaxTotalMemoryMB, in.quota.MaxTotalStorageGB)
		if validateAggregateResourceBudget(projected, in.quota) != nil {
			budgetLine += " — WOULD EXCEED, provisioning blocked"
		} else {
			budgetLine += " — within budget"
		}
		plan = append(plan, budgetLine)
	}

	contextRef, contextLine := contextPlanLine(in)
	plan = append(plan, contextLine)
	if in.newCluster != nil {
		plan = append(plan, in.contextPlan...)
	}

	namespace := eruncommon.KubernetesNamespaceName(in.tenantName, in.envName)
	plan = append(plan, fmt.Sprintf("namespace: would create %s", namespace))

	plan = append(plan, fmt.Sprintf("register: would persist environment %s (%s) in tenant %s referencing context %s", in.envName, in.envType, in.tenantName, contextRef))

	if in.envType != model.EnvironmentTypeRuntime {
		return plan
	}
	plan = append(plan, fmt.Sprintf("deploy: would helm install the erun-devops runtime chart (release %s) into %s", eruncommon.RuntimeReleaseName(in.tenantName), namespace))
	plan = append(plan, "auth: would inject this backend's MCP-signing public key so the runtime's MCP edge trusts tokens minted for the console (skipped when the backend has no MCP signing key configured)")
	plan = append(plan, fmt.Sprintf("expose: would wire mcp.%s.<services zone> via a per-env wildcard DNS record and Host-routing Ingress (skipped when the platform has no services zone configured)", namespace))
	return append(plan, "tls: would provision a per-env wildcard certificate through the DNS-01 broker (skipped when the platform has no ACME email or DNS-01 broker configured)")
}

// contextPlanLine describes where the environment lands and the reference
// the register line carries. An explicit context or kubernetes context is
// only reachable here for a non-runtime environment: resolveDeployPlacement
// already refused the request before provisionPlan runs for a runtime one.
func contextPlanLine(in provisionPlanInput) (contextRef, line string) {
	switch {
	case in.newCluster != nil:
		return in.newCluster.name, fmt.Sprintf("context: bootstrap cluster %s via alias %s", in.newCluster.name, in.newCluster.alias)
	case in.kubernetesContext != "":
		return in.kubernetesContext, fmt.Sprintf("context: reuse existing kubernetes context %s", in.kubernetesContext)
	case in.envType == model.EnvironmentTypeRuntime:
		return "", "context: deploys into this platform's own cluster (v1 single-cluster placement)"
	default:
		return "", "context: none (not server-side deployed)"
	}
}
