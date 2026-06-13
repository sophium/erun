package eruncommon

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// UpgradeVersionsOverrideEnv is a test seam: when set, the shared upgrade
// versions resolver uses these channel targets for every tenant instead of
// querying the registry, so tests and integration scenarios can drive the
// channel-target resolution deterministically without network. Format:
// "stable=<v>,snapshot=<v>" (either key optional), or "error=<msg>" to stage
// a tenant whose resolution fails (the target-unresolved path, issue #497).
// Mirrors the ERUN_HOST_OS_OVERRIDE pattern — a deliberate test seam, not a
// production knob.
const UpgradeVersionsOverrideEnv = "ERUN_UPGRADE_VERSIONS_OVERRIDE"

// RuntimeVersionsOverrideFromEnv parses the UpgradeVersionsOverrideEnv seam.
// ok is false when the env var is unset.
func RuntimeVersionsOverrideFromEnv() (RuntimeRegistryVersions, bool) {
	versions, _, ok := runtimeVersionsOverrideFromEnvWithError()
	return versions, ok
}

// runtimeVersionsOverrideFromEnvWithError additionally surfaces the seam's
// forced-failure form ("error=<msg>") so the shared resolver can stage the
// target-unresolved path deterministically.
func runtimeVersionsOverrideFromEnvWithError() (RuntimeRegistryVersions, string, bool) {
	raw := strings.TrimSpace(os.Getenv(UpgradeVersionsOverrideEnv))
	if raw == "" {
		return RuntimeRegistryVersions{}, "", false
	}
	var versions RuntimeRegistryVersions
	var forcedError string
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
		case "error":
			forcedError = strings.TrimSpace(value)
		}
	}
	return versions, forcedError, true
}

// RegistryVersionsLookup queries one image repository for its latest channel
// versions. ResolveRuntimeImageRegistryVersions is the production lookup; the
// desktop injects its own (same signature) and tests inject fakes.
type RegistryVersionsLookup func(ctx context.Context, namespace, repository string) (RuntimeRegistryVersions, error)

// UpgradeVersionsResolverForStore returns the one RuntimeVersionsResolver
// every transport (CLI, MCP, desktop preview) uses for Upgrade all. One
// resolver is the contract behind issue #497: the desktop preview used to
// substitute the default ERun image's versions when a tenant lookup failed,
// promising upgrades the run then refused as "target unresolved". Policy:
//
//   - the UpgradeVersionsOverrideEnv seam wins (stable=/snapshot=/error=);
//   - the tenant's registry namespace comes from deploy provenance — the
//     first env of the tenant with a persisted RuntimeRegistry — falling
//     back to the default registry, the same source the version picker uses
//     (issue #475);
//   - the tenant repo refines the targets when it is listable: a published
//     tenant stream wins per channel;
//   - a tenant lookup that fails or comes back empty falls back per channel
//     to the canonical ERun image (issue #501): the tenant image is a thin
//     wrapper the deploy rebuilds FROM the canonical image at the requested
//     version (default_devops_module.go), so the canonical channel-latest is
//     the tenant's real target universe — and registries like ghcr report
//     403 for private and nonexistent repos alike, so a listing failure says
//     nothing about what the env can deploy;
//   - only a canonical-image lookup failure leaves the target unresolved,
//     with that failure as the reason.
func UpgradeVersionsResolverForStore(store DeployStore, lookup RegistryVersionsLookup) RuntimeVersionsResolver {
	return func(_ Context, tenant string) (RuntimeRegistryVersions, error) {
		if versions, forcedError, ok := runtimeVersionsOverrideFromEnvWithError(); ok {
			if forcedError != "" {
				return RuntimeRegistryVersions{}, fmt.Errorf("%s", forcedError)
			}
			return versions, nil
		}
		namespace := upgradeRegistryNamespaceForTenant(store, tenant)
		if namespace == "" {
			namespace = DefaultContainerRegistry
		}
		repository := RuntimeReleaseName(tenant)
		if repository == DefaultRuntimeImageName {
			return lookup(context.Background(), namespace, repository)
		}
		versions, tenantErr := lookup(context.Background(), namespace, repository)
		if tenantErr == nil &&
			strings.TrimSpace(versions.LatestStable) != "" && strings.TrimSpace(versions.LatestSnapshot) != "" {
			return versions, nil
		}
		fallback, fallbackErr := lookup(context.Background(), DefaultContainerRegistry, DefaultRuntimeImageName)
		if fallbackErr != nil {
			if tenantErr != nil {
				// Neither source is resolvable — genuinely unknown. The
				// canonical failure is the actionable reason: it is the
				// image the deploy would build from.
				return RuntimeRegistryVersions{}, fallbackErr
			}
			// The tenant's own lookup succeeded (just partial); a failed
			// canonical lookup must not fail the tenant — the empty
			// channels simply stay unresolved.
			return versions, nil
		}
		if tenantErr != nil {
			return fallback, nil
		}
		if strings.TrimSpace(versions.LatestStable) == "" {
			versions.LatestStable = fallback.LatestStable
		}
		if strings.TrimSpace(versions.LatestSnapshot) == "" {
			versions.LatestSnapshot = fallback.LatestSnapshot
		}
		return versions, nil
	}
}

// upgradeRegistryNamespaceForTenant mirrors the deploy provenance the desktop
// version picker uses (issue #475): the first env of the tenant that recorded
// the registry its runtime image was last pushed to. Empty when nothing is
// recorded (never-deployed tenant, or configs predating the provenance).
func upgradeRegistryNamespaceForTenant(store DeployStore, tenant string) string {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" || store == nil {
		return ""
	}
	envs, err := store.ListEnvConfigs(tenant)
	if err != nil {
		return ""
	}
	for _, env := range envs {
		if registry := strings.TrimSpace(env.RuntimeRegistry); registry != "" {
			return registry
		}
	}
	return ""
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
	reasonsByTenant := make(map[string]string)
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
		versions, reason := resolveTenantUpgradeVersions(tenant.Name, override, resolveVersions, traceln)
		versionsByTenant[tenant.Name] = versions
		if reason != "" {
			reasonsByTenant[tenant.Name] = reason
		}
	}

	return ResolveUpgradePlan(candidates, versionsByTenant, reasonsByTenant), nil
}

// resolveTenantUpgradeVersions resolves the channel targets for a tenant. With
// an explicit override it short-circuits the registry (both channels resolve
// to the override). Otherwise it queries the registry via resolveVersions,
// tracing the outcome; a failure yields empty targets plus the human-readable
// reason, so no env is treated as lagging against an unknown version and the
// plan can say why (issue #497).
func resolveTenantUpgradeVersions(tenant, override string, resolveVersions RuntimeVersionsResolver, traceln func(string)) (RuntimeRegistryVersions, string) {
	if override != "" {
		traceln(fmt.Sprintf("upgrade: %s using version override %s for all channels", tenant, override))
		return RuntimeRegistryVersions{LatestStable: override, LatestSnapshot: override}, ""
	}
	if resolveVersions == nil {
		traceln(fmt.Sprintf("upgrade: %s has no version resolver; targets unresolved", tenant))
		return RuntimeRegistryVersions{}, "no version resolver"
	}
	traceln(fmt.Sprintf("upgrade: resolving latest registry versions for %s", tenant))
	versions, err := resolveVersions(Context{}, tenant)
	if err != nil {
		traceln(fmt.Sprintf("upgrade: %s version resolution failed: %s", tenant, err.Error()))
		return RuntimeRegistryVersions{}, err.Error()
	}
	traceln(fmt.Sprintf("upgrade: %s latest stable=%s snapshot=%s", tenant, strings.TrimSpace(versions.LatestStable), strings.TrimSpace(versions.LatestSnapshot)))
	if stable, snapshot, superseded := stableSupersedesSnapshot(versions); superseded {
		traceln(fmt.Sprintf("upgrade: %s stable %s supersedes snapshot %s; snapshot channel targets the stable release", tenant, stable, snapshot))
	}
	return versions, ""
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
// up to date (skipped), members whose channel target could not be resolved
// (skipped — never "up to date", issue #497), and members whose deploy
// failed. The run continues past a failure so one bad env doesn't strand the
// rest.
type UpgradeResult struct {
	Plan       UpgradePlan          `json:"plan"`
	Upgraded   []UpgradePlanItem    `json:"upgraded,omitempty"`
	UpToDate   []UpgradePlanItem    `json:"upToDate,omitempty"`
	Unresolved []UpgradePlanItem    `json:"unresolved,omitempty"`
	Failed     []UpgradeItemFailure `json:"failed,omitempty"`
}

// RunUpgradePlan deploys every lagging member of the plan via deploy, leaving
// up-to-date members untouched. A member whose target is unresolved is
// reported as exactly that — it is not known to be up to date, its latest
// simply couldn't be determined. It continues past per-env failures and
// reports them in the result. Each decision is traced.
func RunUpgradePlan(ctx Context, plan UpgradePlan, deploy UpgradeItemDeployer) UpgradeResult {
	result := UpgradeResult{Plan: plan}
	for _, item := range plan.Items {
		if strings.TrimSpace(item.Target) == "" {
			ctx.Trace(fmt.Sprintf("upgrade: %s/%s target unresolved, skipping%s", item.Tenant, item.Environment, unresolvedReasonSuffix(item)))
			result.Unresolved = append(result.Unresolved, item)
			continue
		}
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
	ctx.Info(fmt.Sprintf("==> Upgrade complete: %d upgraded, %d up to date, %d unresolved, %d failed", len(result.Upgraded), len(result.UpToDate), len(result.Unresolved), len(result.Failed)))
	return result
}

// unresolvedReasonSuffix renders the why behind an unresolved target when the
// plan carries one (": <reason>"), empty otherwise.
func unresolvedReasonSuffix(item UpgradePlanItem) string {
	if reason := strings.TrimSpace(item.UnresolvedReason); reason != "" {
		return ": " + reason
	}
	return ""
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
	// UnresolvedReason says why Target is empty (registry lookup failed, no
	// published version for the channel) so the dialog and the run report
	// the cause instead of a bare "(unset)" (issue #497).
	UnresolvedReason string `json:"unresolvedReason,omitempty"`
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
// versions. Unknown channels fall back to stable. The snapshot channel targets
// the latest snapshot unless a stable release supersedes it (issue #524).
func channelTarget(versions RuntimeRegistryVersions, channel string) string {
	if strings.TrimSpace(channel) == UpgradeChannelSnapshot {
		if stable, _, superseded := stableSupersedesSnapshot(versions); superseded {
			return stable
		}
		return strings.TrimSpace(versions.LatestSnapshot)
	}
	return strings.TrimSpace(versions.LatestStable)
}

// stableSupersedesSnapshot reports whether the latest stable release is the
// newer artifact for the snapshot channel (issue #524). A snapshot tag is a
// pre-release of its base version — builds stamp <version>-snapshot-<utc-ts>
// and the release flow bumps the version right after each stable release — so
// a stable at or above the snapshot's base version was published on top of
// that snapshot stream, while a snapshot whose base outranks the stable
// belongs to the next, newer stream. Unparseable versions keep the snapshot
// target.
func stableSupersedesSnapshot(versions RuntimeRegistryVersions) (stable, snapshot string, superseded bool) {
	stable = strings.TrimSpace(versions.LatestStable)
	snapshot = strings.TrimSpace(versions.LatestSnapshot)
	if stable == "" || snapshot == "" {
		return stable, snapshot, false
	}
	stableVersion, ok := parseRegistryStableVersion(stable)
	if !ok {
		return stable, snapshot, false
	}
	base, _, found := strings.Cut(snapshot, "-snapshot-")
	if !found {
		return stable, snapshot, false
	}
	baseVersion, ok := parseRegistryStableVersion(base)
	if !ok {
		return stable, snapshot, false
	}
	return stable, snapshot, compareSemver(stableVersion, baseVersion) >= 0
}

// ResolveUpgradePlan computes the upgrade plan over the candidate envs given
// the latest registry versions per tenant (and, for tenants whose resolution
// failed, the reason). It includes every env whose AutoUpgrade is set (so
// callers can show up-to-date members too), resolving each env's channel
// target and marking it lagging when the current version differs from a
// non-empty target; an empty target carries the why in UnresolvedReason.
// Pure: no I/O, deterministic in candidate order.
func ResolveUpgradePlan(candidates []UpgradeCandidate, versionsByTenant map[string]RuntimeRegistryVersions, reasonsByTenant map[string]string) UpgradePlan {
	plan := UpgradePlan{Items: make([]UpgradePlanItem, 0, len(candidates))}
	for _, candidate := range candidates {
		if !candidate.Config.AutoUpgrade {
			continue
		}
		channel := candidate.Config.ResolvedUpgradeChannel()
		current := strings.TrimSpace(candidate.Config.RuntimeVersion)
		target := channelTarget(versionsByTenant[candidate.Tenant], channel)
		reason := ""
		if target == "" {
			reason = strings.TrimSpace(reasonsByTenant[candidate.Tenant])
			if reason == "" {
				reason = fmt.Sprintf("no %s version found in the registry", channel)
			}
		}
		plan.Items = append(plan.Items, UpgradePlanItem{
			Tenant:           candidate.Tenant,
			Environment:      candidate.Environment,
			Channel:          channel,
			Current:          current,
			Target:           target,
			Lagging:          target != "" && target != current,
			UnresolvedReason: reason,
		})
	}
	return plan
}
