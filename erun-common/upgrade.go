package eruncommon

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// UpgradeVersionsOverrideEnv is a test seam: when set, DefaultRuntimeVersionsResolver
// uses these channel targets for every tenant instead of querying the
// registry, so tests and integration scenarios can drive the channel-target
// resolution deterministically without network. Format: "stable=<v>,snapshot=<v>"
// (either key optional). Mirrors the ERUN_HOST_OS_OVERRIDE pattern — a
// deliberate test seam, not a production knob.
const UpgradeVersionsOverrideEnv = "ERUN_UPGRADE_VERSIONS_OVERRIDE"

// RuntimeVersionsOverrideFromEnv parses the UpgradeVersionsOverrideEnv seam.
// ok is false when the env var is unset.
func RuntimeVersionsOverrideFromEnv() (RuntimeRegistryVersions, bool) {
	raw := strings.TrimSpace(os.Getenv(UpgradeVersionsOverrideEnv))
	if raw == "" {
		return RuntimeRegistryVersions{}, false
	}
	var versions RuntimeRegistryVersions
	for _, part := range strings.Split(raw, ",") {
		key, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case UpgradeChannelStable:
			versions.LatestStable = strings.TrimSpace(value)
		case UpgradeChannelSnapshot:
			versions.LatestSnapshot = strings.TrimSpace(value)
		}
	}
	return versions, true
}

// DefaultRuntimeVersionsResolver resolves a tenant's latest channel versions
// from the runtime image registry, honoring the UpgradeVersionsOverrideEnv
// test seam first. Shared by the CLI `upgrade` command and the MCP `upgrade`
// tool so both resolve identically.
func DefaultRuntimeVersionsResolver(_ Context, tenant string) (RuntimeRegistryVersions, error) {
	if versions, ok := RuntimeVersionsOverrideFromEnv(); ok {
		return versions, nil
	}
	return ResolveRuntimeImageRegistryVersions(context.Background(), DefaultContainerRegistry, RuntimeReleaseName(tenant))
}

// UpgradeTarget scopes and parameterizes an upgrade run. An empty Tenant
// covers every tenant; Environment narrows to one env (requires Tenant).
// VersionOverride, when set, is used as the target for every opted-in env
// regardless of channel (so `erun upgrade --version X` is deterministic and
// needs no registry lookup). Force is threaded into each deploy.
type UpgradeTarget struct {
	Tenant          string
	Environment     string
	VersionOverride string
	Force           bool
}

// RuntimeVersionsResolver resolves the latest registry versions for a tenant's
// runtime image. Injected so the CLI default hits the registry while tests and
// `--version` overrides supply versions without network.
type RuntimeVersionsResolver func(ctx Context, tenant string) (RuntimeRegistryVersions, error)

// ResolveUpgradePlanForStore enumerates the opted-in environments (scoped by
// target), resolves each tenant's channel targets once, and returns the plan.
// Every decision is traced through ctx so `--dry-run` is a complete audit:
// which envs are opted in, the channel each tracks, its current version, and
// the resolved target. The desktop read-model uses BuildUpgradePlan instead,
// which needs no Context.
func ResolveUpgradePlanForStore(ctx Context, store DeployStore, target UpgradeTarget, resolveVersions RuntimeVersionsResolver) (UpgradePlan, error) {
	return buildUpgradePlan(store, target, resolveVersions, ctx.Trace)
}

// BuildUpgradePlan is the Context-free plan resolver for in-process callers
// (the desktop) that don't have a CLI/MCP trace channel. It does the same
// listing + channel-target resolution as ResolveUpgradePlanForStore without
// tracing.
func BuildUpgradePlan(store DeployStore, target UpgradeTarget, resolveVersions RuntimeVersionsResolver) (UpgradePlan, error) {
	return buildUpgradePlan(store, target, resolveVersions, nil)
}

func buildUpgradePlan(store DeployStore, target UpgradeTarget, resolveVersions RuntimeVersionsResolver, trace func(string)) (UpgradePlan, error) {
	traceln := func(msg string) {
		if trace != nil {
			trace(msg)
		}
	}
	tenants, err := store.ListTenantConfigs()
	if err != nil {
		return UpgradePlan{}, err
	}
	scopeTenant := strings.TrimSpace(target.Tenant)
	scopeEnv := strings.TrimSpace(target.Environment)
	override := strings.TrimSpace(target.VersionOverride)

	candidates := make([]UpgradeCandidate, 0)
	versionsByTenant := make(map[string]RuntimeRegistryVersions)
	for _, tenant := range tenants {
		if scopeTenant != "" && tenant.Name != scopeTenant {
			continue
		}
		envs, err := store.ListEnvConfigs(tenant.Name)
		if err != nil {
			return UpgradePlan{}, err
		}
		tenantHasMember := false
		for _, env := range envs {
			if scopeEnv != "" && env.Name != scopeEnv {
				continue
			}
			if !env.AutoUpgrade {
				traceln(fmt.Sprintf("upgrade: %s/%s not opted in (autoupgrade=false), skipping", tenant.Name, env.Name))
				continue
			}
			traceln(fmt.Sprintf("upgrade: %s/%s opted in, channel=%s current=%s", tenant.Name, env.Name, env.ResolvedUpgradeChannel(), strings.TrimSpace(env.RuntimeVersion)))
			candidates = append(candidates, UpgradeCandidate{Tenant: tenant.Name, Environment: env.Name, Config: env})
			tenantHasMember = true
		}
		if !tenantHasMember {
			continue
		}
		versionsByTenant[tenant.Name] = resolveTenantUpgradeVersions(tenant.Name, override, resolveVersions, traceln)
	}

	return ResolveUpgradePlan(candidates, versionsByTenant), nil
}

// resolveTenantUpgradeVersions resolves the channel targets for a tenant. With
// an explicit override it short-circuits the registry (both channels resolve
// to the override). Otherwise it queries the registry via resolveVersions,
// tracing the outcome; a failure yields empty targets so no env is treated as
// lagging against an unknown version.
func resolveTenantUpgradeVersions(tenant, override string, resolveVersions RuntimeVersionsResolver, traceln func(string)) RuntimeRegistryVersions {
	if override != "" {
		traceln(fmt.Sprintf("upgrade: %s using version override %s for all channels", tenant, override))
		return RuntimeRegistryVersions{LatestStable: override, LatestSnapshot: override}
	}
	if resolveVersions == nil {
		traceln(fmt.Sprintf("upgrade: %s has no version resolver; targets unresolved", tenant))
		return RuntimeRegistryVersions{}
	}
	traceln(fmt.Sprintf("upgrade: resolving latest registry versions for %s", tenant))
	versions, err := resolveVersions(Context{}, tenant)
	if err != nil {
		traceln(fmt.Sprintf("upgrade: %s version resolution failed: %s", tenant, err.Error()))
		return RuntimeRegistryVersions{}
	}
	traceln(fmt.Sprintf("upgrade: %s latest stable=%s snapshot=%s", tenant, strings.TrimSpace(versions.LatestStable), strings.TrimSpace(versions.LatestSnapshot)))
	return versions
}

// UpgradeItemDeployer redeploys one lagging env to its target version. The
// CLI/MCP supply this by composing the existing deploy flow.
type UpgradeItemDeployer func(ctx Context, item UpgradePlanItem) error

// UpgradeItemFailure records a member whose deploy returned an error.
type UpgradeItemFailure struct {
	Item  UpgradePlanItem `json:"item"`
	Error string          `json:"error"`
}

// UpgradeResult summarizes an upgrade run: members redeployed, members already
// up to date (skipped), and members whose deploy failed. The run continues
// past a failure so one bad env doesn't strand the rest.
type UpgradeResult struct {
	Plan     UpgradePlan          `json:"plan"`
	Upgraded []UpgradePlanItem    `json:"upgraded,omitempty"`
	UpToDate []UpgradePlanItem    `json:"upToDate,omitempty"`
	Failed   []UpgradeItemFailure `json:"failed,omitempty"`
}

// RunUpgradePlan deploys every lagging member of the plan via deploy, leaving
// up-to-date members untouched. It continues past per-env failures and reports
// them in the result. Each decision is traced.
func RunUpgradePlan(ctx Context, plan UpgradePlan, deploy UpgradeItemDeployer) UpgradeResult {
	result := UpgradeResult{Plan: plan}
	for _, item := range plan.Items {
		if !item.Lagging {
			ctx.Trace(fmt.Sprintf("upgrade: %s/%s up to date at %s, skipping", item.Tenant, item.Environment, item.Current))
			result.UpToDate = append(result.UpToDate, item)
			continue
		}
		ctx.Info(fmt.Sprintf("==> Upgrading %s/%s %s -> %s (%s)", item.Tenant, item.Environment, displayVersion(item.Current), item.Target, item.Channel))
		if err := deploy(ctx, item); err != nil {
			ctx.Trace(fmt.Sprintf("upgrade: %s/%s failed: %s", item.Tenant, item.Environment, err.Error()))
			result.Failed = append(result.Failed, UpgradeItemFailure{Item: item, Error: err.Error()})
			continue
		}
		result.Upgraded = append(result.Upgraded, item)
	}
	ctx.Info(fmt.Sprintf("==> Upgrade complete: %d upgraded, %d up to date, %d failed", len(result.Upgraded), len(result.UpToDate), len(result.Failed)))
	return result
}

func displayVersion(v string) string {
	if strings.TrimSpace(v) == "" {
		return "(unset)"
	}
	return v
}

// UpgradeCandidate is one environment considered for "Upgrade all", paired
// with the tenant it belongs to. Callers collect candidates from the resolved
// environment list; ResolveUpgradePlan filters to the opted-in set.
type UpgradeCandidate struct {
	Tenant      string
	Environment string
	Config      EnvConfig
}

// UpgradePlanItem is one opted-in environment's upgrade decision: the channel
// it tracks, its current runtime version, the latest version for that channel,
// and whether it lags (a non-empty target different from current). Items with
// an empty Target are environments whose channel latest could not be resolved
// (e.g. no matching tags / registry unreachable); they are reported as
// not-lagging so the upgrade never deploys an unknown target.
type UpgradePlanItem struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Channel     string `json:"channel"`
	Current     string `json:"current"`
	Target      string `json:"target"`
	Lagging     bool   `json:"lagging"`
}

// UpgradePlan is the resolved "Upgrade all" plan: every opted-in environment,
// each marked lagging or up to date. Lagging() is the subset an upgrade run
// actually redeploys.
type UpgradePlan struct {
	Items []UpgradePlanItem `json:"items"`
}

// Lagging returns the plan items that will be redeployed — opted-in envs whose
// current version differs from a resolvable channel target.
func (p UpgradePlan) Lagging() []UpgradePlanItem {
	lagging := make([]UpgradePlanItem, 0, len(p.Items))
	for _, item := range p.Items {
		if item.Lagging {
			lagging = append(lagging, item)
		}
	}
	return lagging
}

// channelTarget picks the latest version for a channel from resolved registry
// versions. Unknown channels fall back to stable.
func channelTarget(versions RuntimeRegistryVersions, channel string) string {
	if strings.TrimSpace(channel) == UpgradeChannelSnapshot {
		return strings.TrimSpace(versions.LatestSnapshot)
	}
	return strings.TrimSpace(versions.LatestStable)
}

// ResolveUpgradePlan computes the upgrade plan over the candidate envs given
// the latest registry versions per tenant. It includes every env whose
// AutoUpgrade is set (so callers can show up-to-date members too), resolving
// each env's channel target and marking it lagging when the current version
// differs from a non-empty target. Pure: no I/O, deterministic in candidate
// order.
func ResolveUpgradePlan(candidates []UpgradeCandidate, versionsByTenant map[string]RuntimeRegistryVersions) UpgradePlan {
	plan := UpgradePlan{Items: make([]UpgradePlanItem, 0, len(candidates))}
	for _, candidate := range candidates {
		if !candidate.Config.AutoUpgrade {
			continue
		}
		channel := candidate.Config.ResolvedUpgradeChannel()
		current := strings.TrimSpace(candidate.Config.RuntimeVersion)
		target := channelTarget(versionsByTenant[candidate.Tenant], channel)
		plan.Items = append(plan.Items, UpgradePlanItem{
			Tenant:      candidate.Tenant,
			Environment: candidate.Environment,
			Channel:     channel,
			Current:     current,
			Target:      target,
			Lagging:     target != "" && target != current,
		})
	}
	return plan
}
