package lifecycle

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/orchestration/clusterread"
)

// EnvironmentVersionStatus is one environment's erun version against the tenant's newest.
type EnvironmentVersionStatus struct {
	Environment string `json:"environment"`
	// Version comes from cached config, else a live helm read. Empty is a confirmed absence unless
	// VersionUnresolved is set.
	Version string `json:"version,omitempty"`
	// BehindMax is set only when both versions parse as plain semver.
	BehindMax bool `json:"behindMax,omitempty"`
	// VersionUnresolved means neither config nor the cluster could answer; excluded from MaxVersion.
	VersionUnresolved       bool   `json:"versionUnresolved,omitempty"`
	VersionUnresolvedReason string `json:"versionUnresolvedReason,omitempty"`
}

// TenantVersionDrift compares environments within one tenant, not against the registry.
// GateEnvironment additionally flags a merge-queue gate older than an environment it gates.
type TenantVersionDrift struct {
	Tenant       string                     `json:"tenant"`
	Environments []EnvironmentVersionStatus `json:"environments,omitempty"`
	MaxVersion   string                     `json:"maxVersion,omitempty"`

	GateEnvironment string `json:"gateEnvironment,omitempty"`
	GateVersion     string `json:"gateVersion,omitempty"`
	// GateVersionUnresolved means GateBehind is not a real verdict.
	GateVersionUnresolved bool `json:"gateVersionUnresolved,omitempty"`
	// GateVersionUnresolvedReason is also empty for a parsed non-semver (snapshot) version.
	GateVersionUnresolvedReason string `json:"gateVersionUnresolvedReason,omitempty"`
	GateBehind                  bool   `json:"gateBehind,omitempty"`
	// GateOutdatedBy names environments running a newer erun version than the gate.
	GateOutdatedBy []string `json:"gateOutdatedBy,omitempty"`
}

// ResolveTenantVersionDrift compares every environment in tenant. An unknown gateEnvironment is an
// error. Dry-run never reads versions live.
func ResolveTenantVersionDrift(ctx core.Context, result ListResult, tenant, gateEnvironment string) (TenantVersionDrift, error) {
	tenant = strings.TrimSpace(tenant)
	gateEnvironment = strings.TrimSpace(gateEnvironment)

	tenantResult, ok := findListTenant(result, tenant)
	if !ok {
		return TenantVersionDrift{}, fmt.Errorf("tenant %q not found", tenant)
	}

	resolutions := make(map[string]environmentVersionResolution, len(tenantResult.Environments))
	for _, env := range tenantResult.Environments {
		resolutions[env.Name] = resolveEnvironmentVersionForDrift(ctx, tenant, env)
	}

	drift := TenantVersionDrift{Tenant: tenant}
	maxVersion, hasMax := maxResolvedVersion(tenantResult.Environments, resolutions)
	if hasMax {
		drift.MaxVersion = registry.FormatSemver(maxVersion)
	}
	for _, env := range tenantResult.Environments {
		drift.Environments = append(drift.Environments, environmentVersionStatus(env.Name, resolutions[env.Name], maxVersion, hasMax))
	}

	if gateEnvironment == "" {
		return drift, nil
	}
	return addGateVerdict(drift, tenantResult, tenant, gateEnvironment, resolutions)
}

// Neither a confirmed absence nor an unknown can be behind.
func environmentVersionStatus(name string, resolution environmentVersionResolution, maxVersion registry.Semver, hasMax bool) EnvironmentVersionStatus {
	status := EnvironmentVersionStatus{Environment: name, Version: resolution.Version}
	if resolution.Version == "" {
		status.VersionUnresolved = !resolution.ConfirmedAbsent
		status.VersionUnresolvedReason = resolution.UnresolvedReason
		return status
	}
	if hasMax {
		if parsed, parsedOK := registry.ParseRegistryStableVersion(resolution.Version); parsedOK {
			status.BehindMax = registry.CompareSemver(parsed, maxVersion) < 0
		}
	}
	return status
}

func addGateVerdict(drift TenantVersionDrift, tenantResult ListTenantResult, tenant, gateEnvironment string, resolutions map[string]environmentVersionResolution) (TenantVersionDrift, error) {
	drift.GateEnvironment = gateEnvironment
	if _, found := findListEnvironment(tenantResult, gateEnvironment); !found {
		return TenantVersionDrift{}, fmt.Errorf("gate environment %q not found in tenant %q", gateEnvironment, tenant)
	}
	gateResolution := resolutions[gateEnvironment]
	drift.GateVersion = gateResolution.Version
	if drift.GateVersion == "" {
		drift.GateVersionUnresolved = true
		drift.GateVersionUnresolvedReason = gateVersionUnresolvedReason(gateResolution)
		return drift, nil
	}
	gateParsed, gateParsedOK := registry.ParseRegistryStableVersion(drift.GateVersion)
	if !gateParsedOK {
		drift.GateVersionUnresolved = true
		return drift, nil
	}
	for _, env := range tenantResult.Environments {
		if env.Name == gateEnvironment {
			continue
		}
		parsed, parsedOK := registry.ParseRegistryStableVersion(resolutions[env.Name].Version)
		if !parsedOK {
			continue
		}
		if registry.CompareSemver(parsed, gateParsed) > 0 {
			drift.GateBehind = true
			drift.GateOutdatedBy = append(drift.GateOutdatedBy, env.Name)
		}
	}
	return drift, nil
}

func gateVersionUnresolvedReason(resolution environmentVersionResolution) string {
	if resolution.ConfirmedAbsent {
		return "no runtime release is deployed for this environment"
	}
	return resolution.UnresolvedReason
}

func erunVersionString(env ListEnvironmentResult) string {
	if env.ErunVersion == nil {
		return ""
	}
	return strings.TrimSpace(env.ErunVersion.Version)
}

// ok is false when none parses: no max, not a max of zero.
func maxResolvedVersion(envs []ListEnvironmentResult, resolutions map[string]environmentVersionResolution) (version registry.Semver, ok bool) {
	for _, env := range envs {
		parsed, parsedOK := registry.ParseRegistryStableVersion(resolutions[env.Name].Version)
		if !parsedOK {
			continue
		}
		if !ok || registry.CompareSemver(parsed, version) > 0 {
			version = parsed
			ok = true
		}
	}
	return version, ok
}

// Empty Version is a confirmed absence only with ConfirmedAbsent; UnresolvedReason is empty when
// the check never ran (e.g. dry-run).
type environmentVersionResolution struct {
	Version          string
	ConfirmedAbsent  bool
	UnresolvedReason string
}

// Falls back to the cluster because config cannot tell "never deployed" from "deployed elsewhere".
func resolveEnvironmentVersionForDrift(ctx core.Context, tenant string, env ListEnvironmentResult) environmentVersionResolution {
	if version := erunVersionString(env); version != "" {
		return environmentVersionResolution{Version: version}
	}
	return resolveLiveEnvironmentVersion(ctx, tenant, env)
}

// Uses the same helm reads as `erun observe`; under dry-run they are only traced.
func resolveLiveEnvironmentVersion(ctx core.Context, tenant string, env ListEnvironmentResult) environmentVersionResolution {
	req := config.ShellLaunchParams{
		Tenant:            tenant,
		Environment:       env.Name,
		Namespace:         config.KubernetesNamespaceName(tenant, env.Name),
		KubernetesContext: strings.TrimSpace(env.KubernetesContext),
	}
	releaseName := config.RuntimeReleaseName(tenant)
	statusArgs := clusterread.ObserveHelmStatusArgs(req)
	listArgs := clusterread.ObserveHelmListArgs(req, releaseName)
	ctx.Trace("list: " + tenant + "/" + env.Name + "'s runtime version is not recorded locally -- checking its deployed release live")
	ctx.TraceCommand("", "helm", statusArgs...)
	ctx.TraceCommand("", "helm", listArgs...)
	if ctx.DryRun {
		return environmentVersionResolution{UnresolvedReason: "not checked in a dry run"}
	}
	release := clusterread.FetchObservedHelmRelease(statusArgs, listArgs, releaseName, req.Namespace)
	if release.Error != "" {
		return environmentVersionResolution{UnresolvedReason: "could not read the deployed release: " + release.Error}
	}
	if !release.Found {
		return environmentVersionResolution{ConfirmedAbsent: true}
	}
	version := strings.TrimSpace(release.AppVersion)
	if version == "" {
		return environmentVersionResolution{UnresolvedReason: fmt.Sprintf("helm release %q was found but reported no appVersion", releaseName)}
	}
	return environmentVersionResolution{Version: version}
}

func findListTenant(result ListResult, tenant string) (ListTenantResult, bool) {
	for _, candidate := range result.Tenants {
		if candidate.Name == tenant {
			return candidate, true
		}
	}
	return ListTenantResult{}, false
}

func findListEnvironment(tenant ListTenantResult, environment string) (ListEnvironmentResult, bool) {
	for _, candidate := range tenant.Environments {
		if candidate.Name == environment {
			return candidate, true
		}
	}
	return ListEnvironmentResult{}, false
}
