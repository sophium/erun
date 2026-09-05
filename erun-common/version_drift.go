package eruncommon

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// EnvironmentVersionStatus is one environment's erun version, compared
// against the newest version any environment in the same tenant is running.
type EnvironmentVersionStatus struct {
	Environment string `json:"environment"`
	// Version is the environment's resolved erun version: read from config
	// alone (ResolveErunVersion) when that resolves, otherwise a live
	// fallback probe of the environment's own MCP edge (see
	// EnvironmentVersionProbeFunc) -- empty when neither source can name it.
	Version string `json:"version,omitempty"`
	// BehindMax is set only when both this environment's version and the
	// tenant's MaxVersion parse as plain three-part semver -- an unparseable
	// or snapshot version is reported bare rather than guessed at.
	BehindMax bool `json:"behindMax,omitempty"`
	// VersionUnresolved is set when this environment's MCP edge answered the
	// live probe (it is up and reachable) but still reported no version --
	// distinct from an environment nobody probed reaching, or one that was
	// never open at all, because an environment that is up and unreadable is
	// itself drift-shaped: erun has lost track of what is deployed there.
	// --fail-on-drift treats this as a finding; a genuinely unreachable
	// environment (never opened, or stopped) leaves Version empty with this
	// left false, and is not a finding (erun-cli/AGENTS.md's fail-on-drift
	// exit-code contract).
	VersionUnresolved bool `json:"versionUnresolved,omitempty"`
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

// EnvironmentVersionProbeFunc probes one environment's own MCP edge for the
// erun version it is actually running, the fallback ResolveTenantVersionDrift
// reaches for whenever config-based resolution (ResolveErunVersion) comes
// back empty -- config has no way to parse a tenant's own runtime image tag
// (e.g. frs-devops:1.0.98), but the running edge's own initialize handshake
// always reports its own binary's version, regardless of which image shipped
// it (erun#2093). reachable reports whether the edge answered the handshake
// at all, independent of err: false means there is nothing to ask (never
// opened, or genuinely stopped) and is not itself a finding; true with a
// non-nil err or an empty version means the edge is up but its version still
// could not be determined, which --fail-on-drift must treat as a finding
// (see EnvironmentVersionStatus.VersionUnresolved).
type EnvironmentVersionProbeFunc func(ctx context.Context, tenant string, env ListEnvironmentResult) (version string, reachable bool, err error)

// DefaultEnvironmentVersionProbe live-probes an environment's local MCP
// edge -- the same loopback port `erun open`'s port-forward occupies -- using
// the desktop identity to mint the bearer. clientVersion identifies the
// caller (CLI or MCP) in the edge's own request logs; it never affects the
// probed environment's reported version.
func DefaultEnvironmentVersionProbe(clientVersion string) EnvironmentVersionProbeFunc {
	return func(ctx context.Context, tenant string, env ListEnvironmentResult) (string, bool, error) {
		port := env.LocalPorts.MCP
		if port <= 0 || !CanReachLocalMCPEndpoint(port) {
			return "", false, nil
		}
		mintToken := func() (string, error) {
			return MintDesktopMCPToken(DefaultDesktopIdentityDir(), tenant, env.Name, time.Now())
		}
		version, err := ProbeMCPServerVersion(ctx, LocalMCPEndpoint(port), mintToken, clientVersion)
		return version, true, err
	}
}

func normalizeEnvironmentVersionProbe(probe EnvironmentVersionProbeFunc) EnvironmentVersionProbeFunc {
	if probe != nil {
		return probe
	}
	return DefaultEnvironmentVersionProbe("")
}

// ResolveTenantVersionDrift compares the erun version of every environment in
// tenant, using an already-resolved ListResult (see ResolveListResult).
// gateEnvironment is optional; when set, it must name one of tenant's own
// environments, or resolution fails outright -- a typo silently producing no
// gate verdict is worse than an error. probe is normalized to
// DefaultEnvironmentVersionProbe when nil; under ctx.DryRun no probe call is
// made at all -- every environment that would be probed is traced instead,
// matching the no-network-call dry-run contract the control-plane drift
// check (erun-common/control_plane_version_drift.go) already established.
func ResolveTenantVersionDrift(ctx Context, result ListResult, tenant, gateEnvironment string, probe EnvironmentVersionProbeFunc) (TenantVersionDrift, error) {
	tenant = strings.TrimSpace(tenant)
	gateEnvironment = strings.TrimSpace(gateEnvironment)
	probe = normalizeEnvironmentVersionProbe(probe)

	tenantResult, ok := findListTenant(result, tenant)
	if !ok {
		return TenantVersionDrift{}, fmt.Errorf("tenant %q not found", tenant)
	}

	drift := TenantVersionDrift{Tenant: tenant}
	for _, env := range tenantResult.Environments {
		drift.Environments = append(drift.Environments, resolveEnvironmentVersionStatus(ctx, tenant, env, probe))
	}

	maxVersion, hasMax := maxResolvedEnvironmentVersion(drift.Environments)
	if hasMax {
		drift.MaxVersion = formatSemver(maxVersion)
		for i := range drift.Environments {
			drift.Environments[i].BehindMax, _ = versionVerdict(drift.Environments[i].Version, maxVersion, hasMax)
		}
	}

	if gateEnvironment == "" {
		return drift, nil
	}
	return addGateVerdict(drift, tenantResult, gateEnvironment)
}

// resolveEnvironmentVersionStatus resolves one environment's version from
// config first, live-probing its MCP edge only when config resolution comes
// back empty -- the live probe is the exception path, never the routine one,
// so a tenant whose environments all resolve from config never makes a
// network call.
func resolveEnvironmentVersionStatus(ctx Context, tenant string, env ListEnvironmentResult, probe EnvironmentVersionProbeFunc) EnvironmentVersionStatus {
	status := EnvironmentVersionStatus{Environment: env.Name, Version: erunVersionString(env)}
	if status.Version != "" {
		return status
	}
	if env.LocalPorts.MCP <= 0 {
		return status
	}
	endpoint := LocalMCPEndpoint(env.LocalPorts.MCP)
	if ctx.DryRun {
		ctx.Trace(fmt.Sprintf("list: %s/%s has no erun version recorded in config; would probe its MCP edge at %s for the running version", tenant, env.Name, endpoint))
		return status
	}
	ctx.Trace(fmt.Sprintf("list: %s/%s has no erun version recorded in config; probing its MCP edge at %s for the running version", tenant, env.Name, endpoint))
	version, reachable, err := probe(context.Background(), tenant, env)
	if !reachable {
		ctx.Trace(fmt.Sprintf("list: %s/%s's MCP edge is not reachable; its version stays unresolved", tenant, env.Name))
		return status
	}
	if err != nil {
		ctx.Trace(fmt.Sprintf("list: %s/%s's MCP edge answered but its version could not be determined: %s", tenant, env.Name, err.Error()))
		status.VersionUnresolved = true
		return status
	}
	version = strings.TrimSpace(version)
	if version == "" {
		ctx.Trace(fmt.Sprintf("list: %s/%s's MCP edge answered but reported no version", tenant, env.Name))
		status.VersionUnresolved = true
		return status
	}
	ctx.Trace(fmt.Sprintf("list: %s/%s's MCP edge reports erun version %s", tenant, env.Name, version))
	status.Version = version
	return status
}

// addGateVerdict fills in drift's gate fields: which environment gates
// tenant's merges, its own erun version, and whether any other environment
// in drift.Environments outranks it. It reuses the statuses
// ResolveTenantVersionDrift already resolved (including any live-probed
// fallback) rather than re-deriving them, so the gate environment is never
// probed twice.
func addGateVerdict(drift TenantVersionDrift, tenantResult ListTenantResult, gateEnvironment string) (TenantVersionDrift, error) {
	drift.GateEnvironment = gateEnvironment
	if _, found := findListEnvironment(tenantResult, gateEnvironment); !found {
		return TenantVersionDrift{}, fmt.Errorf("gate environment %q not found in tenant %q", gateEnvironment, drift.Tenant)
	}
	gateStatus, _ := findEnvironmentVersionStatus(drift.Environments, gateEnvironment)
	drift.GateVersion = gateStatus.Version
	gateParsed, gateParsedOK := parseRegistryStableVersion(drift.GateVersion)
	if !gateParsedOK {
		drift.GateVersionUnresolved = true
		return drift, nil
	}
	for _, status := range drift.Environments {
		if status.Environment == gateEnvironment {
			continue
		}
		parsed, parsedOK := parseRegistryStableVersion(status.Version)
		if !parsedOK {
			continue
		}
		if compareSemver(parsed, gateParsed) > 0 {
			drift.GateBehind = true
			drift.GateOutdatedBy = append(drift.GateOutdatedBy, status.Environment)
		}
	}
	return drift, nil
}

func findEnvironmentVersionStatus(statuses []EnvironmentVersionStatus, environment string) (EnvironmentVersionStatus, bool) {
	for _, status := range statuses {
		if status.Environment == environment {
			return status, true
		}
	}
	return EnvironmentVersionStatus{}, false
}

func erunVersionString(env ListEnvironmentResult) string {
	if env.ErunVersion == nil {
		return ""
	}
	return strings.TrimSpace(env.ErunVersion.Version)
}

// maxResolvedEnvironmentVersion returns the newest parseable erun version
// among statuses' already-resolved versions (config-based or live-probed).
// ok is false when none parses -- an empty/snapshot-only tenant has no max to
// compare against, not a max of zero.
func maxResolvedEnvironmentVersion(statuses []EnvironmentVersionStatus) (version semver, ok bool) {
	for _, status := range statuses {
		parsed, parsedOK := parseRegistryStableVersion(status.Version)
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
