package eruncommon

import (
	"fmt"
	"strings"
)

// EnvironmentVersionStatus is one environment's erun version, compared
// against the newest version any environment in the same tenant is running.
type EnvironmentVersionStatus struct {
	Environment string `json:"environment"`
	// Version is the environment's resolved erun version: read from its own
	// cached config (ResolveErunVersion) when that names one, or from a live
	// read of the environment's own deployed helm release when it does not
	// (a config that never learned about a deploy run elsewhere must not be
	// reported as a confirmed absence). Empty means either a
	// confirmed absence (VersionUnresolved false) or that neither source
	// could tell (VersionUnresolved true) -- never conflate the two.
	Version string `json:"version,omitempty"`
	// BehindMax is set only when both this environment's version and the
	// tenant's MaxVersion parse as plain three-part semver -- an unparseable
	// or snapshot version is reported bare rather than guessed at.
	BehindMax bool `json:"behindMax,omitempty"`
	// VersionUnresolved is set when Version is empty because nothing --
	// neither the environment's own cached config nor a live read of its
	// cluster -- could answer whether (and what) it runs. Distinct from an
	// empty Version that is a confirmed absence: that is a fact, reported as
	// "none" with VersionUnresolved false, not a guess. Excluded from
	// MaxVersion/BehindMax, since neither can be computed from an unknown.
	VersionUnresolved bool `json:"versionUnresolved,omitempty"`
	// VersionUnresolvedReason explains why, e.g. an unreachable cluster or a
	// failed helm read -- absent evidence is reported explicitly, never
	// silently folded into "not deployed".
	VersionUnresolvedReason string `json:"versionUnresolvedReason,omitempty"`
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
// no stored concept of (the backend API guide's Release cadence policy
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
	// GateVersionUnresolvedReason explains why, mirroring
	// EnvironmentVersionStatus.VersionUnresolvedReason -- empty only when
	// GateVersionUnresolved is also false, or when the gate's version parsed
	// but not as plain semver (a snapshot build, reported bare).
	GateVersionUnresolvedReason string `json:"gateVersionUnresolvedReason,omitempty"`
	GateBehind                  bool   `json:"gateBehind,omitempty"`
	// GateOutdatedBy names every environment running a newer erun version
	// than GateEnvironment -- the concrete environments a stale gate could
	// wrongly pass a change against.
	GateOutdatedBy []string `json:"gateOutdatedBy,omitempty"`
}

// ResolveTenantVersionDrift compares the erun version of every environment in
// tenant, using an already-resolved ListResult (see ResolveListResult).
// gateEnvironment is optional; when set, it must name one of tenant's own
// environments, or resolution fails outright -- a typo silently producing no
// gate verdict is worse than an error. ctx supports --dry-run: an
// environment whose cached config carries no version is never guessed at
// live in a dry run, only traced (see resolveLiveEnvironmentVersion).
func ResolveTenantVersionDrift(ctx Context, result ListResult, tenant, gateEnvironment string) (TenantVersionDrift, error) {
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
		drift.MaxVersion = formatSemver(maxVersion)
	}
	for _, env := range tenantResult.Environments {
		drift.Environments = append(drift.Environments, environmentVersionStatus(env.Name, resolutions[env.Name], maxVersion, hasMax))
	}

	if gateEnvironment == "" {
		return drift, nil
	}
	return addGateVerdict(drift, tenantResult, tenant, gateEnvironment, resolutions)
}

// environmentVersionStatus renders one environment's resolution into the
// report shape: BehindMax only applies to a resolution that actually named a
// version, never to a confirmed absence or an unresolved unknown -- neither
// of those can be "behind" anything.
func environmentVersionStatus(name string, resolution environmentVersionResolution, maxVersion semver, hasMax bool) EnvironmentVersionStatus {
	status := EnvironmentVersionStatus{Environment: name, Version: resolution.Version}
	if resolution.Version == "" {
		status.VersionUnresolved = !resolution.ConfirmedAbsent
		status.VersionUnresolvedReason = resolution.UnresolvedReason
		return status
	}
	if hasMax {
		if parsed, parsedOK := parseRegistryStableVersion(resolution.Version); parsedOK {
			status.BehindMax = compareSemver(parsed, maxVersion) < 0
		}
	}
	return status
}

// addGateVerdict fills in drift's gate fields: which environment gates
// tenant's merges, its own erun version, and whether any other environment
// in tenantResult outranks it. Reuses the resolutions ResolveTenantVersionDrift
// already computed rather than re-resolving the gate environment live a
// second time.
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
	gateParsed, gateParsedOK := parseRegistryStableVersion(drift.GateVersion)
	if !gateParsedOK {
		drift.GateVersionUnresolved = true
		return drift, nil
	}
	for _, env := range tenantResult.Environments {
		if env.Name == gateEnvironment {
			continue
		}
		parsed, parsedOK := parseRegistryStableVersion(resolutions[env.Name].Version)
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

// gateVersionUnresolvedReason explains why the gate's own version is empty --
// a confirmed absence reads distinctly from a check that could not tell.
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

// maxResolvedVersion returns the newest parseable erun version among envs'
// resolutions. ok is false when none parses -- an empty/snapshot-only tenant
// has no max to compare against, not a max of zero.
func maxResolvedVersion(envs []ListEnvironmentResult, resolutions map[string]environmentVersionResolution) (version semver, ok bool) {
	for _, env := range envs {
		parsed, parsedOK := parseRegistryStableVersion(resolutions[env.Name].Version)
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

// environmentVersionResolution is the outcome of resolving one environment's
// erun version for the tenant drift report. An empty Version means either a
// confirmed absence (ConfirmedAbsent true -- a fact, safe to report as
// "none") or that nothing could tell (ConfirmedAbsent false; UnresolvedReason
// names why when the check ran and failed, empty when it was never run at
// all, e.g. skipped in a dry run).
type environmentVersionResolution struct {
	Version          string
	ConfirmedAbsent  bool
	UnresolvedReason string
}

// resolveEnvironmentVersionForDrift resolves one environment's erun version:
// its own cached config first (ResolveErunVersion, already applied by
// ResolveListResult), falling back to a live read of its deployed helm
// release only when that cache has nothing to say. The cache alone cannot
// distinguish "never deployed" from "deployed by a process whose config
// never learned about it" -- both look identical to the config reader, and
// only asking the cluster directly can tell them apart.
func resolveEnvironmentVersionForDrift(ctx Context, tenant string, env ListEnvironmentResult) environmentVersionResolution {
	if version := erunVersionString(env); version != "" {
		return environmentVersionResolution{Version: version}
	}
	return resolveLiveEnvironmentVersion(ctx, tenant, env)
}

// resolveLiveEnvironmentVersion asks env's own cluster whether a runtime
// release is deployed at all, and what version it runs, reusing the same
// helm reads `erun observe` already trusts (fetchObservedHelmRelease's
// Found/Error contract -- see observe_helm_release.go). Never called in a
// dry run: the two helm invocations are traced as what *would* run, and the
// resolution comes back unresolved with a reason that says so, matching the
// no-live-network-call dry-run contract every other list-mode live check in
// this file already follows.
func resolveLiveEnvironmentVersion(ctx Context, tenant string, env ListEnvironmentResult) environmentVersionResolution {
	req := ShellLaunchParams{
		Tenant:            tenant,
		Environment:       env.Name,
		Namespace:         KubernetesNamespaceName(tenant, env.Name),
		KubernetesContext: strings.TrimSpace(env.KubernetesContext),
	}
	releaseName := RuntimeReleaseName(tenant)
	statusArgs := observeHelmStatusArgs(req)
	listArgs := observeHelmListArgs(req, releaseName)
	ctx.Trace("list: " + tenant + "/" + env.Name + "'s runtime version is not recorded locally -- checking its deployed release live")
	ctx.TraceCommand("", "helm", statusArgs...)
	ctx.TraceCommand("", "helm", listArgs...)
	if ctx.DryRun {
		return environmentVersionResolution{UnresolvedReason: "not checked in a dry run"}
	}
	release := fetchObservedHelmRelease(statusArgs, listArgs, releaseName, req.Namespace)
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
