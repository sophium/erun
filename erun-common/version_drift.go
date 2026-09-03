package eruncommon

import (
	"fmt"
	"strings"
)

// EnvironmentVersionStatus is one environment's erun version, compared
// against the newest version any environment in the same tenant is running.
type EnvironmentVersionStatus struct {
	Environment string `json:"environment"`
	// Version is the environment's resolved erun version (ResolveErunVersion),
	// empty when it cannot be read from config alone.
	Version string `json:"version,omitempty"`
	// BehindMax is set only when both this environment's version and the
	// tenant's MaxVersion parse as plain three-part semver -- an unparseable
	// or snapshot version is reported bare rather than guessed at.
	BehindMax bool `json:"behindMax,omitempty"`
}

// TenantVersionDrift compares the erun version every environment in one
// tenant is running. MaxVersion is the newest version observed among the
// tenant's own environments -- not the newest version erun has ever
// published (that is `erun upgrade`'s / `erun version`'s registry-latest
// concern) -- because the defect this exists to catch is drift between
// environments in the same tenant, not staleness against an upstream
// release.
//
// GateEnvironment, when set, additionally answers a question erun itself has
// no stored concept of (root AGENTS.md's release-cadence policy, erun#1985
// records the design and the gap): whether the environment driving this
// tenant's merge-queue gate is running an older erun version than an
// environment it gates. A gate older than the code it gates can pass a
// change that would fail on current code.
type TenantVersionDrift struct {
	Tenant       string                     `json:"tenant"`
	Environments []EnvironmentVersionStatus `json:"environments,omitempty"`
	MaxVersion   string                     `json:"maxVersion,omitempty"`

	GateEnvironment string `json:"gateEnvironment,omitempty"`
	GateVersion     string `json:"gateVersion,omitempty"`
	// GateVersionUnresolved is set when GateEnvironment's own erun version
	// cannot be read from config alone, so GateBehind cannot be a real
	// verdict -- absent evidence is reported explicitly, never silently
	// folded into "not behind".
	GateVersionUnresolved bool `json:"gateVersionUnresolved,omitempty"`
	GateBehind            bool `json:"gateBehind,omitempty"`
	// GateOutdatedBy names every environment running a newer erun version
	// than GateEnvironment -- the concrete environments a stale gate could
	// wrongly pass a change against.
	GateOutdatedBy []string `json:"gateOutdatedBy,omitempty"`
}

// ResolveTenantVersionDrift compares the erun version of every environment in
// tenant, using an already-resolved ListResult (see ResolveListResult).
// gateEnvironment is optional; when set, it must name one of tenant's own
// environments, or resolution fails outright -- a typo silently producing no
// gate verdict is worse than an error.
func ResolveTenantVersionDrift(result ListResult, tenant, gateEnvironment string) (TenantVersionDrift, error) {
	tenant = strings.TrimSpace(tenant)
	gateEnvironment = strings.TrimSpace(gateEnvironment)

	tenantResult, ok := findListTenant(result, tenant)
	if !ok {
		return TenantVersionDrift{}, fmt.Errorf("tenant %q not found", tenant)
	}

	drift := TenantVersionDrift{Tenant: tenant}
	maxVersion, hasMax := maxEnvironmentErunVersion(tenantResult.Environments)
	if hasMax {
		drift.MaxVersion = formatSemver(maxVersion)
	}
	for _, env := range tenantResult.Environments {
		status := EnvironmentVersionStatus{Environment: env.Name, Version: erunVersionString(env)}
		if hasMax {
			if parsed, parsedOK := parseRegistryStableVersion(status.Version); parsedOK {
				status.BehindMax = compareSemver(parsed, maxVersion) < 0
			}
		}
		drift.Environments = append(drift.Environments, status)
	}

	if gateEnvironment == "" {
		return drift, nil
	}
	return addGateVerdict(drift, tenantResult, tenant, gateEnvironment)
}

// addGateVerdict fills in drift's gate fields: which environment gates
// tenant's merges, its own erun version, and whether any other environment
// in tenantResult outranks it.
func addGateVerdict(drift TenantVersionDrift, tenantResult ListTenantResult, tenant, gateEnvironment string) (TenantVersionDrift, error) {
	drift.GateEnvironment = gateEnvironment
	gateEnv, found := findListEnvironment(tenantResult, gateEnvironment)
	if !found {
		return TenantVersionDrift{}, fmt.Errorf("gate environment %q not found in tenant %q", gateEnvironment, tenant)
	}
	drift.GateVersion = erunVersionString(gateEnv)
	gateParsed, gateParsedOK := parseRegistryStableVersion(drift.GateVersion)
	if !gateParsedOK {
		drift.GateVersionUnresolved = true
		return drift, nil
	}
	for _, env := range tenantResult.Environments {
		if env.Name == gateEnvironment {
			continue
		}
		parsed, parsedOK := parseRegistryStableVersion(erunVersionString(env))
		if !parsedOK {
			continue
		}
		if compareSemver(parsed, gateParsed) > 0 {
			drift.GateBehind = true
			drift.GateOutdatedBy = append(drift.GateOutdatedBy, env.Name)
		}
	}
	return drift, nil
}

func erunVersionString(env ListEnvironmentResult) string {
	if env.ErunVersion == nil {
		return ""
	}
	return strings.TrimSpace(env.ErunVersion.Version)
}

// maxEnvironmentErunVersion returns the newest parseable erun version among
// envs. ok is false when none parses -- an empty/snapshot-only tenant has no
// max to compare against, not a max of zero.
func maxEnvironmentErunVersion(envs []ListEnvironmentResult) (version semver, ok bool) {
	for _, env := range envs {
		parsed, parsedOK := parseRegistryStableVersion(erunVersionString(env))
		if !parsedOK {
			continue
		}
		if !ok || compareSemver(parsed, version) > 0 {
			version = parsed
			ok = true
		}
	}
	return version, ok
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
