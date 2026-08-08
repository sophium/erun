package eruncommon

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// UpgradeVersionsOverrideEnv is a test-only seam that forces the upgrade
// versions resolver's channel targets so tests resolve deterministically
// without a registry. Not a production knob.
const UpgradeVersionsOverrideEnv = "ERUN_UPGRADE_VERSIONS_OVERRIDE"

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
// versions.
type RegistryVersionsLookup func(ctx context.Context, namespace, repository string) (RuntimeRegistryVersions, error)

// SourcedRuntimeVersions pairs a registry with the runtime versions it offers
// for an environment's runtime image.
type SourcedRuntimeVersions struct {
	Registry string
	Versions RuntimeRegistryVersions
}

// EnvVersionsResolver resolves the candidate runtime versions for one
// environment — one entry per registry queried, each tagged with its source
// registry.
type EnvVersionsResolver func(ctx Context, tenant string, env EnvConfig) ([]SourcedRuntimeVersions, error)

// UpgradeVersionsResolverForStore builds the shared Upgrade-all resolver. It
// always queries the canonical ERun image alongside the env's own registries
// because tenant images are thin wrappers rebuilt FROM the canonical image, so
// its channel-latest is part of the env's real target universe and a
// private/nonexistent tenant repo (ghcr 403s both alike) never blocks the
// upgrade. Each result is tagged with its source registry so the caller can
// offer a pick when registries disagree on the newest version.
func UpgradeVersionsResolverForStore(_ DeployStore, lookup RegistryVersionsLookup) EnvVersionsResolver {
	return func(_ Context, tenant string, env EnvConfig) ([]SourcedRuntimeVersions, error) {
		if versions, forcedError, ok := runtimeVersionsOverrideFromEnvWithError(); ok {
			if forcedError != "" {
				return nil, fmt.Errorf("%s", forcedError)
			}
			return []SourcedRuntimeVersions{{Versions: versions}}, nil
		}
		repository := RuntimeReleaseName(tenant)
		sourced := make([]SourcedRuntimeVersions, 0, 4)
		queried := make(map[string]struct{}, 4)
		var firstErr error
		query := func(registry, repo string) {
			registry = strings.TrimSpace(registry)
			if registry == "" {
				return
			}
			key := registry + "|" + repo
			if _, ok := queried[key]; ok {
				return
			}
			queried[key] = struct{}{}
			versions, err := lookup(context.Background(), registry, repo)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			sourced = append(sourced, SourcedRuntimeVersions{Registry: registry, Versions: versions})
		}
		for _, registry := range upgradeDiscoveryRegistries(env) {
			query(registry, repository)
		}
		query(DefaultContainerRegistry, DefaultRuntimeImageName)
		if len(sourced) == 0 {
			return nil, firstErr
		}
		return sourced, nil
	}
}

func upgradeDiscoveryRegistries(env EnvConfig) []string {
	registries := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	add := func(registry string) {
		registry = strings.TrimSpace(registry)
		if registry == "" {
			return
		}
		if _, ok := seen[registry]; ok {
			return
		}
		seen[registry] = struct{}{}
		registries = append(registries, registry)
	}
	add(env.RuntimeRegistry)
	for _, registry := range ResolveEnvironmentContainerRegistries(env).DistinctRegistries() {
		add(registry)
	}
	if len(registries) == 0 {
		add(DefaultContainerRegistry)
	}
	return registries
}

// UpgradeTarget scopes and parameterizes an upgrade run. An empty Tenant
// covers every tenant; Environment narrows to one env (requires Tenant).
// VersionOverride, when set, is the target for every opted-in env regardless
// of channel, so `erun upgrade --version X` needs no registry lookup.
type UpgradeTarget struct {
	Tenant          string
	Environment     string
	VersionOverride string
	Force           bool
}

// ResolveUpgradePlanForStore resolves the "Upgrade all" plan and traces every
// decision through ctx so `--dry-run` is a complete audit of which envs are
// opted in, the channel each tracks, its current version, and the resolved
// target (or why it is unresolved).
func ResolveUpgradePlanForStore(ctx Context, store DeployStore, target UpgradeTarget, resolveVersions EnvVersionsResolver) (UpgradePlan, error) {
	return buildUpgradePlan(store, target, resolveVersions, ctx.Trace)
}

// BuildUpgradePlan is the Context-free plan resolver for in-process callers
// (the desktop) that have no CLI/MCP trace channel.
func BuildUpgradePlan(store DeployStore, target UpgradeTarget, resolveVersions EnvVersionsResolver) (UpgradePlan, error) {
	return buildUpgradePlan(store, target, resolveVersions, nil)
}

func buildUpgradePlan(store DeployStore, target UpgradeTarget, resolveVersions EnvVersionsResolver, trace func(string)) (UpgradePlan, error) {
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

	plan := UpgradePlan{Items: make([]UpgradePlanItem, 0)}
	for _, tenant := range tenants {
		if scopeTenant != "" && tenant.Name != scopeTenant {
			continue
		}
		envs, err := store.ListEnvConfigs(tenant.Name)
		if err != nil {
			return UpgradePlan{}, err
		}
		plan.Items = appendTenantUpgradeItems(plan.Items, tenant.Name, envs, scopeEnv, override, resolveVersions, traceln)
	}

	return plan, nil
}

func appendTenantUpgradeItems(items []UpgradePlanItem, tenant string, envs []EnvConfig, scopeEnv, override string, resolveVersions EnvVersionsResolver, traceln func(string)) []UpgradePlanItem {
	for _, env := range envs {
		if scopeEnv != "" && env.Name != scopeEnv {
			continue
		}
		if !env.AutoUpgrade {
			traceln(fmt.Sprintf("upgrade: %s/%s not opted in (autoupgrade=false), skipping", tenant, env.Name))
			continue
		}
		traceln(fmt.Sprintf("upgrade: %s/%s opted in, channel=%s current=%s", tenant, env.Name, env.ResolvedUpgradeChannel(), strings.TrimSpace(env.RuntimeVersion)))
		items = append(items, resolveEnvUpgradeItem(tenant, env, override, resolveVersions, traceln))
	}
	return items
}

func resolveEnvUpgradeItem(tenant string, env EnvConfig, override string, resolveVersions EnvVersionsResolver, traceln func(string)) UpgradePlanItem {
	channel := env.ResolvedUpgradeChannel()
	current := strings.TrimSpace(env.RuntimeVersion)
	item := UpgradePlanItem{Tenant: tenant, Environment: env.Name, Channel: channel, Current: current}

	if override != "" {
		traceln(fmt.Sprintf("upgrade: %s/%s using version override %s for all channels", tenant, env.Name, override))
		item.Candidates = []UpgradeVersionCandidate{{Version: override}}
		item.Target = override
		item.Lagging = override != current
		return item
	}
	if resolveVersions == nil {
		traceln(fmt.Sprintf("upgrade: %s/%s has no version resolver; target unresolved", tenant, env.Name))
		item.UnresolvedReason = "no version resolver"
		return item
	}

	traceln(fmt.Sprintf("upgrade: resolving versions for %s/%s from the listed registries", tenant, env.Name))
	sourced, err := resolveVersions(Context{}, tenant, env)
	if err != nil {
		traceln(fmt.Sprintf("upgrade: %s/%s version resolution failed: %s", tenant, env.Name, err.Error()))
		item.UnresolvedReason = err.Error()
		return item
	}

	candidates, anyResolved := collectUpgradeCandidates(sourced, channel, current)
	item.Candidates = candidates

	switch len(candidates) {
	case 0:
		if !anyResolved {
			item.UnresolvedReason = fmt.Sprintf("no %s version found in the listed registries", channel)
			traceln(fmt.Sprintf("upgrade: %s/%s target unresolved: %s", tenant, env.Name, item.UnresolvedReason))
		} else {
			// Record current as the (already-met) target so the run reports
			// up to date rather than unresolved.
			item.Target = current
			traceln(fmt.Sprintf("upgrade: %s/%s up to date at %s", tenant, env.Name, displayVersion(current)))
		}
	case 1:
		item.Target = candidates[0].Version
		item.Lagging = true
		traceln(fmt.Sprintf("upgrade: %s/%s %s -> %s%s", tenant, env.Name, displayVersion(current), candidates[0].Version, candidateRegistrySuffix(candidates[0].Registry)))
	default:
		item.UnresolvedReason = "multiple newer versions across registries; pick one or pass --version"
		traceln(fmt.Sprintf("upgrade: %s/%s has %d newer candidates; needs a pick (%s)", tenant, env.Name, len(candidates), candidateSummary(candidates)))
	}
	return item
}

// collectUpgradeCandidates returns the distinct newer candidates for the
// channel. anyResolved is true when at least one registry produced a channel
// target (even one equal to current), letting the caller tell "up to date"
// from "nothing resolved".
func collectUpgradeCandidates(sourced []SourcedRuntimeVersions, channel, current string) ([]UpgradeVersionCandidate, bool) {
	candidates := make([]UpgradeVersionCandidate, 0, len(sourced))
	seen := make(map[string]struct{}, len(sourced))
	anyResolved := false
	for _, sv := range sourced {
		target := channelTarget(sv.Versions, channel)
		if target == "" {
			continue
		}
		anyResolved = true
		if target == current {
			continue
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		candidates = append(candidates, UpgradeVersionCandidate{Version: target, Registry: sv.Registry})
	}
	return candidates, anyResolved
}

func candidateRegistrySuffix(registry string) string {
	if registry = strings.TrimSpace(registry); registry != "" {
		return " (from " + registry + ")"
	}
	return ""
}

func candidateSummary(candidates []UpgradeVersionCandidate) string {
	parts := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		parts = append(parts, candidate.Version+candidateRegistrySuffix(candidate.Registry))
	}
	return strings.Join(parts, ", ")
}

// UpgradeItemDeployer redeploys one lagging env to its target version.
type UpgradeItemDeployer func(ctx Context, item UpgradePlanItem) error

// UpgradeItemFailure records a member whose deploy returned an error.
type UpgradeItemFailure struct {
	Item  UpgradePlanItem `json:"item"`
	Error string          `json:"error"`
}

// UpgradeResult summarizes an upgrade run. Unresolved members are skipped but
// never counted as up to date — their latest simply couldn't be determined.
// The run continues past a per-env failure so one bad env doesn't strand the
// rest.
type UpgradeResult struct {
	Plan       UpgradePlan          `json:"plan"`
	Upgraded   []UpgradePlanItem    `json:"upgraded,omitempty"`
	UpToDate   []UpgradePlanItem    `json:"upToDate,omitempty"`
	Unresolved []UpgradePlanItem    `json:"unresolved,omitempty"`
	Failed     []UpgradeItemFailure `json:"failed,omitempty"`
}

// RunUpgradePlan deploys every lagging member and leaves up-to-date members
// untouched. An unresolved target is reported as unresolved, not up to date:
// its latest couldn't be determined (or an ambiguous env needs a pick). The
// run continues past per-env failures and reports them.
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

// UpgradeVersionCandidate is one newer runtime version discovered for an env,
// tagged with the registry that offered it. When an env has more than one
// distinct newer candidate, the caller picks one.
type UpgradeVersionCandidate struct {
	Version  string `json:"version"`
	Registry string `json:"registry,omitempty"`
}

// UpgradePlanItem is one opted-in environment's upgrade decision. An empty
// Target means the env is up to date, its latest could not be resolved, or it
// has more than one newer candidate awaiting a pick — UnresolvedReason
// distinguishes the last two.
type UpgradePlanItem struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Channel     string `json:"channel"`
	Current     string `json:"current"`
	Target      string `json:"target"`
	Lagging     bool   `json:"lagging"`
	// Candidates holds the distinct newer versions found across the env's
	// registries; more than one means the user must pick.
	Candidates []UpgradeVersionCandidate `json:"candidates,omitempty"`
	// UnresolvedReason says why Target is empty so the run reports the cause
	// rather than a bare "(unset)".
	UnresolvedReason string `json:"unresolvedReason,omitempty"`
}

// UpgradePlan is the resolved "Upgrade all" plan: every opted-in environment,
// each marked lagging or up to date.
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

func channelTarget(versions RuntimeRegistryVersions, channel string) string {
	if strings.TrimSpace(channel) == UpgradeChannelSnapshot {
		// An absent snapshot side is not "nothing newer exists" — it means the
		// registry publishes no snapshots at all, and the stable release is then
		// the only, and newest, candidate. Falling through to LatestSnapshot in
		// that case resolves to "", which leaves a snapshot-channel env
		// permanently unresolvable against a stable-only registry — the canonical
		// one included, so every env pointed at it silently never upgraded.
		if stable, snapshot, superseded := stableSupersedesSnapshot(versions); superseded || snapshot == "" {
			return stable
		}
		return strings.TrimSpace(versions.LatestSnapshot)
	}
	return strings.TrimSpace(versions.LatestStable)
}

// stableSupersedesSnapshot reports whether the latest stable release is the
// newer artifact for the snapshot channel. A snapshot tag is a
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
