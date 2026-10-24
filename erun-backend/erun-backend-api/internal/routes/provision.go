package routes

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-common/base/config"
)

// ProvisionRoutes serves the provision preview; it never executes or writes.
type ProvisionRoutes struct {
	tenants      ConfigTenantRepository
	environments EnvironmentRepository
	quotas       TenantQuotaRepository
}

// provisionRequest takes the tenant from the token. context means a new cluster,
// kubernetesContext an existing one (ignored when context is set).
type provisionRequest struct {
	Environment       provisionEnvironment `json:"environment"`
	Context           *provisionContext    `json:"context,omitempty"`
	KubernetesContext string               `json:"kubernetesContext"`
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

// provisionResponse returns the full plan even when quotaOk is false.
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Same raw-kubernetesContext refusal as POST /v1/environments.
	placementContext := strings.TrimSpace(body.KubernetesContext)
	if newCluster != nil {
		placementContext = newCluster.name
	}
	if err := validateProvisionPlacement(envType, placementContext); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	tenant, err := r.tenants.Current(req.Context())
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}

	count, quota, err := environmentQuotaUsage(req.Context(), r.environments, r.quotas)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	runtimeCount, err := r.environments.CountByType(req.Context(), model.EnvironmentTypeRuntime)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}

	// TODO(live): execute this plan. Until then it stays side-effect free; the
	// discrete context/environment endpoints own the writes.
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

func validateProvisionEnvironment(environment provisionEnvironment) (string, model.EnvironmentType, error) {
	envName := strings.TrimSpace(environment.Name)
	if !validNamespaceLabel(envName) {
		return "", "", errors.New("environment.name must be a DNS-1123 label: lowercase letters, digits, and internal hyphens, not starting or ending with a hyphen, at most 63 characters")
	}
	envType := model.EnvironmentType(strings.TrimSpace(environment.Type))
	if _, ok := validEnvironmentTypes[envType]; !ok {
		return "", "", errors.New("environment.type must be one of runtime, remote-agent, local-agent")
	}
	return envName, envType, nil
}

// validateProvisionPlacement mirrors resolvePlacement's raw-kubernetesContext refusal.
func validateProvisionPlacement(envType model.EnvironmentType, kubernetesContext string) error {
	if envType != model.EnvironmentTypeRuntime {
		return nil
	}
	if strings.TrimSpace(kubernetesContext) != "" {
		return errCrossClusterPlacementUnsupported
	}
	return nil
}

// resolveProvisionContext returns nils for an existing context.
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

// provisionPlanInput.newCluster is nil for an existing kubernetes context.
type provisionPlanInput struct {
	tenantName        string
	envName           string
	envType           model.EnvironmentType
	newCluster        *contextBootstrapParams
	contextPlan       []string
	kubernetesContext string
	count             int
	runtimeCount      int
	quota             model.TenantQuota
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

// provisionPlan is the single renderer for both preview paths, so the audited plan
// cannot diverge from execution.
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

	namespace := config.KubernetesNamespaceName(in.tenantName, in.envName)
	plan = append(plan, fmt.Sprintf("namespace: would create %s", namespace))

	plan = append(plan, fmt.Sprintf("register: would persist environment %s (%s) in tenant %s referencing context %s", in.envName, in.envType, in.tenantName, contextRef))

	if in.envType != model.EnvironmentTypeRuntime {
		return plan
	}
	plan = append(plan, fmt.Sprintf("deploy: would helm install the erun-devops runtime chart (release %s) into %s", config.RuntimeReleaseName(in.tenantName), namespace))
	plan = append(plan, "auth: would inject this backend's MCP-signing public key so the runtime's MCP edge trusts tokens minted for the console (skipped when the backend has no MCP signing key configured)")
	plan = append(plan, fmt.Sprintf("expose: would wire mcp.%s.<services zone> via a per-env wildcard DNS record and Host-routing Ingress (skipped when the platform has no services zone configured)", namespace))
	return append(plan, "tls: would provision a per-env wildcard certificate through the DNS-01 broker (skipped when the platform has no ACME email or DNS-01 broker configured)")
}

// adoptPlan is separate because provisionPlan always renders a deploy for
// runtime; the deploy is named "skipped" so preview and create agree.
func adoptPlan(tenantName, envName string, envType model.EnvironmentType, kubernetesContext string, count int, quota model.TenantQuota) []string {
	plan := []string{fmt.Sprintf("adopt: tenant %s (resolved from token)", tenantName)}

	quotaLine := fmt.Sprintf("quota: tenant has %d of %d environments", count, quota.MaxEnvironments)
	if count < quota.MaxEnvironments {
		quotaLine += " — within quota"
	} else {
		quotaLine += " — WOULD EXCEED, registering blocked"
	}
	plan = append(plan, quotaLine)

	plan = append(plan, fmt.Sprintf("register: would persist environment %s (%s) in tenant %s referencing kubernetes context %s — describes an environment that already exists", envName, envType, tenantName, kubernetesContext))
	return append(plan, "deploy: skipped — adopting an existing environment never starts a deploy")
}

// contextPlanLine only sees an explicit context for non-runtime environments.
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
