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
// a tenant whose resolution fails (the target-unresolved path).
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

// SourcedRuntimeVersions pairs a registry with the runtime versions it offers
// for an environment's runtime image.
type SourcedRuntimeVersions struct {
	Registry string
	Versions RuntimeRegistryVersions
}

// EnvVersionsResolver resolves the candidate runtime versions for one
// environment — one entry per registry queried, each tagged with its source
// registry. Every transport (CLI, MCP, desktop preview) shares the resolver
// UpgradeVersionsResolverForStore builds.
type EnvVersionsResolver func(ctx Context, tenant string, env EnvConfig) ([]SourcedRuntimeVersions, error)

// UpgradeVersionsResolverForStore builds the shared Upgrade-all resolver. It
// queries every registry in the env's marked list (plus the registry the env
// was last deployed from) for the tenant runtime image, and always queries the
// canonical ERun image too: tenant images are thin wrappers the deploy rebuilds
// FROM the canonical image, so its channel-latest is part of the env's real
// target universe and a private/nonexistent tenant repo (ghcr 403s both alike)
// never blocks the upgrade. Each result is tagged with its source registry so
// the caller can offer a pick when registries disagree on the newest version.
// The UpgradeVersionsOverrideEnv seam wins for deterministic tests. Only when
// no registry resolves does the env go unresolved, with the first failure as
// the reason.
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

// upgradeDiscoveryRegistries returns the registries the upgrade resolver
// queries for an env's tenant runtime image: the registry it was last deployed
// from (provenance) plus every registry in its marked list,
// defaulting to the canonical registry.
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
// VersionOverride, when set, is used as the target for every opted-in env
// regardless of channel (so `erun upgrade --version X` is deterministic and
// needs no registry lookup). Force is threaded into each deploy.
type UpgradeTarget struct {
	Tenant          string
	Environment     string
	VersionOverride string
	Force           bool
}

// ResolveUpgradePlanForStore enumerates the opted-in environments (scoped by
// target), resolves each env's candidate versions across its listed registries,
// and returns the plan. Every decision is traced through ctx so `--dry-run` is
// a complete audit: which envs are opted in, the channel each tracks, its
// current version, and the resolved target (or why it is unresolved). The
// desktop read-model uses BuildUpgradePlan instead, which needs no Context.
func ResolveUpgradePlanForStore(ctx Context, store DeployStore, target UpgradeTarget, resolveVersions EnvVersionsResolver) (UpgradePlan, error) {
	return buildUpgradePlan(store, target, resolveVersions, ctx.Trace)
}

// BuildUpgradePlan is the Context-free plan resolver for in-process callers
// (the desktop) that don't have a CLI/MCP trace channel. It does the same
// listing + candidate resolution as ResolveUpgradePlanForStore without tracing.
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

// appendTenantUpgradeItems resolves the upgrade decision for each opted-in env
// of one tenant (scoped by scopeEnv when non-empty) and appends one plan item
// per opted-in env, tracing the skip/opt-in decision for each.
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

// resolveEnvUpgradeItem resolves one env's upgrade decision. With an explicit
// override it short-circuits the registry (the override is the single target).
// Otherwise it collects the distinct newer versions across the env's listed
// registries: zero → up to date (or unresolved when nothing resolved), one →
// the target, more than one → ambiguous (the caller picks; CLI/MCP skip with
// the reason). Each outcome is traced.
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
			// The resolved latest equals current: up to date. Record the
			// current version as the (already-met) target so the run reports
			// it up to date rather than unresolved.
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

// collectUpgradeCandidates reduces the per-registry sourced versions to the
// distinct newer candidates for the channel: it skips registries with no target
// for the channel, skips the current version, and dedupes by version (first
// registry wins). anyResolved reports whether at least one registry produced a
// channel target (even when that target equals current), so the caller can tell
// "up to date" from "nothing resolved".
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

// candidateRegistrySuffix renders " (from <registry>)" when the candidate
// carries a source registry, empty otherwise (e.g. the test seam).
func candidateRegistrySuffix(registry string) string {
	if registry = strings.TrimSpace(registry); registry != "" {
		return " (from " + registry + ")"
	}
	return ""
}

// candidateSummary renders the ambiguous candidate set for the trace.
func candidateSummary(candidates []UpgradeVersionCandidate) string {
	parts := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		parts = append(parts, candidate.Version+candidateRegistrySuffix(candidate.Registry))
	}
	return strings.Join(parts, ", ")
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
// (skipped — never "up to date"), and members whose deploy
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
// simply couldn't be determined (or, for an ambiguous env, needs a pick). It
// continues past per-env failures and reports them in the result. Each
// decision is traced.
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

// UpgradeVersionCandidate is one newer runtime version discovered for an env,
// tagged with the registry that offered it. When an env has more than one
// distinct newer candidate, the caller picks one.
type UpgradeVersionCandidate struct {
	Version  string `json:"version"`
	Registry string `json:"registry,omitempty"`
}

// UpgradePlanItem is one opted-in environment's upgrade decision: the channel
// it tracks, its current runtime version, the discovered newer candidates (one
// per distinct version across the listed registries), the chosen target, and
// whether it lags. An empty Target means the env is up to date, its latest
// could not be resolved, or it has more than one newer candidate awaiting a
// pick — UnresolvedReason distinguishes the last two.
type UpgradePlanItem struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Channel     string `json:"channel"`
	Current     string `json:"current"`
	Target      string `json:"target"`
	Lagging     bool   `json:"lagging"`
	// Candidates lists the distinct newer versions discovered across the env's
	// listed registries, each with its source registry. Empty when up to date
	// or unresolved; one entry when a single target resolved; more than one
	// when the user must pick (also surfaced via UnresolvedReason).
	Candidates []UpgradeVersionCandidate `json:"candidates,omitempty"`
	// UnresolvedReason says why Target is empty (registry lookup failed, no
	// published version for the channel, or multiple newer candidates need a
	// pick) so the dialog and the run report the cause instead of a bare
	// "(unset)".
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
// the latest snapshot unless a stable release supersedes it.
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
