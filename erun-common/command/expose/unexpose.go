package expose

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// UnexposeResult is the resolved teardown plan.
type UnexposeResult struct {
	Tenant                     string `json:"tenant"`
	Environment                string `json:"environment"`
	WildcardName               string `json:"wildcardName"`
	ServicesZone               string `json:"servicesZone"`
	PlatformNamespace          string `json:"platformNamespace"`
	PlatformPowerDNSDeployment string `json:"platformPowerdnsDeployment"`
	PlatformContext            string `json:"platformContext,omitempty"`
}

// RunUnexposeService removes the wildcard record expose created so a later env
// reusing the name does not inherit it. Dry-run traces the full plan.
func RunUnexposeService(ctx core.Context, params model.UnexposeParams, store seam.ExposeStore, cloudStore config.CloudReadStore, deps cloud.CloudDependencies, deleteDNSRecord DNSRecordDeleterFunc) (UnexposeResult, error) {
	if store == nil {
		store = config.ConfigStore{}
	}

	if params.SkipIfUnconfigured && !unexposePlatformConfigured(params) {
		ctx.Trace("unexpose: skipped, no platform block configured")
		return UnexposeResult{}, nil
	}

	result, err := resolveUnexposeServicePlan(params, store)
	if err != nil {
		return UnexposeResult{}, err
	}

	hasDirectOverride := strings.TrimSpace(params.ServicesZone) != "" || strings.TrimSpace(params.PlatformNamespace) != ""
	deleteDNSRecord, dnsProvider, err := resolveUnexposeDNSDeleter(ctx, result.Environment, params.ErunAlias, cloudStore, deps, hasDirectOverride, deleteDNSRecord)
	if err != nil {
		return UnexposeResult{}, err
	}

	deleteParams := DNSRecordDeleteParams{
		Zone:               result.ServicesZone,
		Name:               result.WildcardName,
		Type:               "A",
		PlatformNamespace:  result.PlatformNamespace,
		PowerDNSDeployment: result.PlatformPowerDNSDeployment,
		KubernetesContext:  result.PlatformContext,
	}

	ctx.Trace(fmt.Sprintf("unexpose: per-env wildcard %s (zone %s)", result.WildcardName, result.ServicesZone))
	if dnsProvider.Alias == "" {
		ctx.Trace(fmt.Sprintf("unexpose: platform powerdns namespace %s", result.PlatformNamespace))
		ctx.TraceCommand("", "kubectl", powerDNSDeleteArgs(deleteParams)...)
	} else {
		cloud.TracePlatformCall(ctx, dnsProvider, "GET", "/v1/environments", "resolve environment id for "+result.Environment)
		cloud.TracePlatformCall(ctx, dnsProvider, "DELETE", "/v1/environments/{environment_id}/hostname")
	}
	if ctx.DryRun {
		return result, nil
	}

	if err := deleteDNSRecord(deleteParams); err != nil {
		return result, fmt.Errorf("delete wildcard DNS record %s: %w", result.WildcardName, err)
	}
	return result, nil
}

func resolveUnexposeServicePlan(params model.UnexposeParams, store seam.ExposeStore) (UnexposeResult, error) {
	tenant := strings.TrimSpace(params.Tenant)
	environment := strings.TrimSpace(params.Environment)
	if err := core.ErrMissingTenantOrEnvironment("resolve unexpose service plan", tenant, environment); err != nil {
		return UnexposeResult{}, err
	}
	if err := config.ValidateTenantName(tenant); err != nil {
		return UnexposeResult{}, err
	}

	servicesZone, platformNamespace, platformPowerDNS, platformContext, err := resolveExposePlatformCoordinates(ExposeServiceParams{
		ProjectRoot:       params.ProjectRoot,
		ServicesZone:      params.ServicesZone,
		PlatformNamespace: params.PlatformNamespace,
	}, store)
	if err != nil {
		return UnexposeResult{}, err
	}

	envLabel := config.KubernetesNamespaceName(tenant, environment)
	return UnexposeResult{
		Tenant:                     tenant,
		Environment:                environment,
		WildcardName:               fmt.Sprintf("*.%s.%s", envLabel, servicesZone),
		ServicesZone:               servicesZone,
		PlatformNamespace:          platformNamespace,
		PlatformPowerDNSDeployment: platformPowerDNS,
		PlatformContext:            platformContext,
	}, nil
}

func unexposePlatformConfigured(params model.UnexposeParams) bool {
	if strings.TrimSpace(params.ServicesZone) != "" && strings.TrimSpace(params.PlatformNamespace) != "" {
		return true
	}
	return ProjectHasExposablePlatform(params.ProjectRoot)
}
