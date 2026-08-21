package eruncommon

import (
	"fmt"
	"strings"
)

// UnexposeParams are the inputs to removing an environment's per-env wildcard
// DNS record — the counterpart `erun expose` never had (#1094). Removing the
// record is the only teardown action needed here: the Ingress that referenced
// it lives in the env's own namespace and is torn down with it.
type UnexposeParams struct {
	Tenant      string
	Environment string
	ProjectRoot string
	// SkipIfUnconfigured mirrors expose's own flag: succeed as a no-op instead
	// of failing when the project declares no platform block, for a caller
	// (the delete Job) composing unexpose without knowing whether the target
	// was ever exposed at all.
	SkipIfUnconfigured bool
	// ServicesZone/PlatformNamespace override the platform coordinates
	// unexpose would otherwise read from ProjectRoot, exactly like expose's
	// own fields of the same name — the sourceless delete Job supplies these
	// directly since it has no git checkout to resolve a project from.
	ServicesZone      string
	PlatformNamespace string
}

// UnexposeResult is the resolved teardown plan: the per-env wildcard record
// name and the platform coordinates the delete targets.
type UnexposeResult struct {
	Tenant                     string `json:"tenant"`
	Environment                string `json:"environment"`
	WildcardName               string `json:"wildcardName"`
	ServicesZone               string `json:"servicesZone"`
	PlatformNamespace          string `json:"platformNamespace"`
	PlatformPowerDNSDeployment string `json:"platformPowerdnsDeployment"`
	PlatformContext            string `json:"platformContext,omitempty"`
}

// RunUnexposeService resolves and (unless dry-run) removes the per-env
// wildcard A record `erun expose` created, so records don't accumulate for
// environments that no longer exist and a later environment reusing the same
// name doesn't inherit a stale one (#1094). Every action and decision is
// traced before execution so a dry-run is a complete, side-effect-free plan.
func RunUnexposeService(ctx Context, params UnexposeParams, store ExposeStore, deleteDNSRecord DNSRecordDeleterFunc) (UnexposeResult, error) {
	if store == nil {
		store = ConfigStore{}
	}
	if deleteDNSRecord == nil {
		deleteDNSRecord = deletePowerDNSRecord
	}

	if params.SkipIfUnconfigured && !unexposePlatformConfigured(params) {
		ctx.Trace("unexpose: skipped, no platform block configured")
		return UnexposeResult{}, nil
	}

	result, err := resolveUnexposeServicePlan(params, store)
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
	ctx.Trace(fmt.Sprintf("unexpose: platform powerdns namespace %s", result.PlatformNamespace))
	ctx.TraceCommand("", "kubectl", powerDNSDeleteArgs(deleteParams)...)
	if ctx.DryRun {
		return result, nil
	}

	if err := deleteDNSRecord(deleteParams); err != nil {
		return result, fmt.Errorf("delete wildcard DNS record %s: %w", result.WildcardName, err)
	}
	return result, nil
}

// resolveUnexposeServicePlan does no tracing or mutation, mirroring
// resolveExposeServicePlan.
func resolveUnexposeServicePlan(params UnexposeParams, store ExposeStore) (UnexposeResult, error) {
	tenant := strings.TrimSpace(params.Tenant)
	environment := strings.TrimSpace(params.Environment)
	if tenant == "" || environment == "" {
		return UnexposeResult{}, fmt.Errorf("tenant and environment are required")
	}
	if err := ValidateTenantName(tenant); err != nil {
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

	envLabel := KubernetesNamespaceName(tenant, environment)
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

// unexposePlatformConfigured mirrors exposePlatformConfigured: whether
// unexpose has enough platform information to resolve a wildcard record name,
// either from an explicit override or ProjectRoot's platform block.
func unexposePlatformConfigured(params UnexposeParams) bool {
	if strings.TrimSpace(params.ServicesZone) != "" && strings.TrimSpace(params.PlatformNamespace) != "" {
		return true
	}
	return projectHasExposablePlatform(params.ProjectRoot)
}
