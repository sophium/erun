package lifecycle

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
	registrypkg "github.com/sophium/erun/erun-common/infra/registry"
	deploypkg "github.com/sophium/erun/erun-common/primitive/deploy"
)

// UpgradeVersionsOverrideEnv is a test-only seam forcing the resolver's channel targets.
const UpgradeVersionsOverrideEnv = "ERUN_UPGRADE_VERSIONS_OVERRIDE"

func runtimeVersionsOverrideFromEnvWithError() (model.RuntimeRegistryVersions, string, bool) {
	raw := strings.TrimSpace(os.Getenv(UpgradeVersionsOverrideEnv))
	if raw == "" {
		return model.RuntimeRegistryVersions{}, "", false
	}
	var versions model.RuntimeRegistryVersions
	var forcedError string
	for _, part := range strings.Split(raw, ",") {
		key, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case config.UpgradeChannelStable:
			versions.LatestStable = strings.TrimSpace(value)
		case config.UpgradeChannelSnapshot:
			versions.LatestSnapshot = strings.TrimSpace(value)
		case "error":
			forcedError = strings.TrimSpace(value)
		}
	}
	return versions, forcedError, true
}

// RegistryVersionsLookup queries one image repository for its latest channel versions.
type RegistryVersionsLookup func(ctx context.Context, namespace, repository string) (model.RuntimeRegistryVersions, error)

// SourcedRuntimeVersions pairs a registry with the runtime versions it offers.
type SourcedRuntimeVersions struct {
	Registry string
	Versions model.RuntimeRegistryVersions
}

// EnvVersionsResolver resolves one environment's candidate versions, one entry per registry.
type EnvVersionsResolver func(ctx core.Context, tenant string, env config.EnvConfig) ([]SourcedRuntimeVersions, error)

// UpgradeVersionsResolverForStore builds the Upgrade-all resolver. It always queries the canonical
// image too: tenant images are rebuilt FROM it, and ghcr 403s private and missing repos alike.
func UpgradeVersionsResolverForStore(_ deploypkg.DeployStore, lookup RegistryVersionsLookup) EnvVersionsResolver {
	return func(_ core.Context, tenant string, env config.EnvConfig) ([]SourcedRuntimeVersions, error) {
		if versions, forcedError, ok := runtimeVersionsOverrideFromEnvWithError(); ok {
			if forcedError != "" {
				return nil, fmt.Errorf("%s", forcedError)
			}
			return []SourcedRuntimeVersions{{Versions: versions}}, nil
		}
		repository := config.RuntimeReleaseName(tenant)
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
		query(config.DefaultContainerRegistry, config.DefaultRuntimeImageName)
		if len(sourced) == 0 {
			return nil, firstErr
		}
		return sourced, nil
	}
}

func upgradeDiscoveryRegistries(env config.EnvConfig) []string {
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
	for _, registry := range config.ResolveEnvironmentContainerRegistries(env).DistinctRegistries() {
		add(registry)
	}
	if len(registries) == 0 {
		add(config.DefaultContainerRegistry)
	}
	return registries
}

// ResolveUpgradePlanForStore resolves the Upgrade-all plan, tracing every decision for --dry-run.
func ResolveUpgradePlanForStore(ctx core.Context, store deploypkg.DeployStore, target model.UpgradeTarget, resolveVersions EnvVersionsResolver) (UpgradePlan, error) {
	return buildUpgradePlan(store, target, resolveVersions, ctx.Trace)
}

// BuildUpgradePlan is the untraced plan resolver for in-process callers.
func BuildUpgradePlan(store deploypkg.DeployStore, target model.UpgradeTarget, resolveVersions EnvVersionsResolver) (UpgradePlan, error) {
	return buildUpgradePlan(store, target, resolveVersions, nil)
}

func buildUpgradePlan(store deploypkg.DeployStore, target model.UpgradeTarget, resolveVersions EnvVersionsResolver, trace func(string)) (UpgradePlan, error) {
	traceln := func(msg string) {
		if trace != nil {
			trace(msg)
		}
	}
	scope, err := normalizeUpgradeScope(target)
	if err != nil {
		return UpgradePlan{}, err
	}
	tenants, err := store.ListTenantConfigs()
	if err != nil {
		return UpgradePlan{}, err
	}
	items, gateFound, err := collectUpgradePlanItems(store, tenants, scope, resolveVersions, traceln)
	if err != nil {
		return UpgradePlan{}, err
	}
	if scope.gateEnvironment != "" {
		if !gateFound {
			return UpgradePlan{}, fmt.Errorf("gate environment %q not found in tenant %q", scope.gateEnvironment, scope.tenant)
		}
		items = prioritizeGateUpgradeItem(items, scope.tenant, scope.gateEnvironment)
	}
	return UpgradePlan{Items: items}, nil
}

type upgradeScope struct {
	tenant          string
	environment     string
	override        string
	gateEnvironment string
	fleet           bool
}

func normalizeUpgradeScope(target model.UpgradeTarget) (upgradeScope, error) {
	scope := upgradeScope{
		tenant:          strings.TrimSpace(target.Tenant),
		environment:     strings.TrimSpace(target.Environment),
		override:        strings.TrimSpace(target.VersionOverride),
		gateEnvironment: strings.TrimSpace(target.GateEnvironment),
		fleet:           target.Fleet,
	}
	if scope.gateEnvironment != "" && scope.tenant == "" {
		return upgradeScope{}, fmt.Errorf("--gate-environment requires --tenant")
	}
	if scope.fleet && scope.tenant == "" {
		return upgradeScope{}, fmt.Errorf("--fleet requires --tenant")
	}
	return scope, nil
}

// A gate environment that is not found must fail the plan.
func collectUpgradePlanItems(store deploypkg.DeployStore, tenants []model.TenantConfig, scope upgradeScope, resolveVersions EnvVersionsResolver, traceln func(string)) ([]model.UpgradePlanItem, bool, error) {
	items := make([]model.UpgradePlanItem, 0)
	gateFound := scope.gateEnvironment == ""
	for _, tenant := range tenants {
		if scope.tenant != "" && tenant.Name != scope.tenant {
			continue
		}
		envs, err := store.ListEnvConfigs(tenant.Name)
		if err != nil {
			return nil, false, err
		}
		if scope.gateEnvironment != "" && envConfigsContain(envs, scope.gateEnvironment) {
			gateFound = true
		}
		items = appendTenantUpgradeItems(items, tenant.Name, envs, scope.environment, scope.override, scope.gateEnvironment, scope.fleet, resolveVersions, traceln)
	}
	return items, gateFound, nil
}

func envConfigsContain(envs []config.EnvConfig, name string) bool {
	for _, env := range envs {
		if env.Name == name {
			return true
		}
	}
	return false
}

func appendTenantUpgradeItems(items []model.UpgradePlanItem, tenant string, envs []config.EnvConfig, scopeEnv, override, gateEnvironment string, fleet bool, resolveVersions EnvVersionsResolver, traceln func(string)) []model.UpgradePlanItem {
	for _, env := range envs {
		if scopeEnv != "" && env.Name != scopeEnv {
			continue
		}
		isGate := gateEnvironment != "" && env.Name == gateEnvironment
		if !env.AutoUpgrade && !fleet && !isGate {
			traceln(fmt.Sprintf("upgrade: %s/%s not opted in (autoupgrade=false), skipping", tenant, env.Name))
			continue
		}
		// ResolvedType, not !HasPod(), so a legacy env with an empty type keeps upgrading.
		if env.ResolvedType() == config.EnvironmentTypeHost {
			traceln(fmt.Sprintf("upgrade: %s/%s is a host environment (no runtime pod to upgrade), skipping", tenant, env.Name))
			continue
		}
		traceln(upgradeInclusionTrace(tenant, env, fleet, isGate))
		item := resolveEnvUpgradeItem(tenant, env, override, resolveVersions, traceln)
		item.IsGate = isGate
		items = append(items, item)
	}
	return items
}

// Fleet and gate-forced inclusions get their own wording so they never read as an opt-in.
func upgradeInclusionTrace(tenant string, env config.EnvConfig, fleet, isGate bool) string {
	channel, current := env.ResolvedUpgradeChannel(), strings.TrimSpace(env.RuntimeVersion)
	switch {
	case env.AutoUpgrade:
		return fmt.Sprintf("upgrade: %s/%s opted in, channel=%s current=%s", tenant, env.Name, channel, current)
	case isGate:
		return fmt.Sprintf("upgrade: %s/%s included as the gate environment (autoupgrade=false), channel=%s current=%s", tenant, env.Name, channel, current)
	default: // fleet
		return fmt.Sprintf("upgrade: %s/%s included via --fleet (autoupgrade=false), channel=%s current=%s", tenant, env.Name, channel, current)
	}
}

// prioritizeGateUpgradeItem moves the gate first so it rolls before what it gates.
func prioritizeGateUpgradeItem(items []model.UpgradePlanItem, tenant, gateEnvironment string) []model.UpgradePlanItem {
	for i, item := range items {
		if item.Tenant != tenant || item.Environment != gateEnvironment {
			continue
		}
		if i == 0 {
			return items
		}
		reordered := make([]model.UpgradePlanItem, 0, len(items))
		reordered = append(reordered, item)
		reordered = append(reordered, items[:i]...)
		reordered = append(reordered, items[i+1:]...)
		return reordered
	}
	return items
}

func resolveEnvUpgradeItem(tenant string, env config.EnvConfig, override string, resolveVersions EnvVersionsResolver, traceln func(string)) model.UpgradePlanItem {
	channel := env.ResolvedUpgradeChannel()
	current := strings.TrimSpace(env.RuntimeVersion)
	item := model.UpgradePlanItem{Tenant: tenant, Environment: env.Name, Channel: channel, Current: current}

	if override != "" {
		traceln(fmt.Sprintf("upgrade: %s/%s using version override %s for all channels", tenant, env.Name, override))
		item.Candidates = []model.UpgradeVersionCandidate{{Version: override}}
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
	sourced, err := resolveVersions(core.Context{}, tenant, env)
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
			// Record current as the met target so the run reports up to date, not unresolved.
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

// anyResolved separates "up to date" from "nothing resolved".
func collectUpgradeCandidates(sourced []SourcedRuntimeVersions, channel, current string) ([]model.UpgradeVersionCandidate, bool) {
	candidates := make([]model.UpgradeVersionCandidate, 0, len(sourced))
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
		candidates = append(candidates, model.UpgradeVersionCandidate{Version: target, Registry: sv.Registry})
	}
	return candidates, anyResolved
}

func candidateRegistrySuffix(registry string) string {
	if registry = strings.TrimSpace(registry); registry != "" {
		return " (from " + registry + ")"
	}
	return ""
}

func candidateSummary(candidates []model.UpgradeVersionCandidate) string {
	parts := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		parts = append(parts, candidate.Version+candidateRegistrySuffix(candidate.Registry))
	}
	return strings.Join(parts, ", ")
}

// UpgradeItemDeployer redeploys one lagging env to its target version.
type UpgradeItemDeployer func(ctx core.Context, item model.UpgradePlanItem) error

// UpgradeOccupancyError refuses an upgrade of a held environment, since the roll recreates its pod.
type UpgradeOccupancyError struct {
	Tenant      string
	Environment string
	Holders     []model.EnvironmentActivityLease
}

func (e *UpgradeOccupancyError) Error() string {
	return fmt.Sprintf("%s/%s is held by %s -- an upgrade restarts the runtime pod and would interrupt that work; pass --override-lease to roll it anyway, or wait until it finishes",
		e.Tenant, e.Environment, activity.FormatLeaseHolders(e.Holders))
}

// LeaseGuardedUpgradeDeployer refuses held environments unless override (traced), and holds an
// exclusive lease during the deploy so concurrent upgrades cannot race.
func LeaseGuardedUpgradeDeployer(deploy UpgradeItemDeployer, override bool, holder model.EnvironmentActivityLeaseHolder, now func() time.Time) UpgradeItemDeployer {
	if now == nil {
		now = time.Now
	}
	return func(ctx core.Context, item model.UpgradePlanItem) error {
		nowValue := now()
		leases, err := activity.LoadEnvironmentActivityLeases(item.Tenant, item.Environment, nowValue)
		if err != nil {
			return fmt.Errorf("upgrade: %s/%s: reading activity leases: %w", item.Tenant, item.Environment, err)
		}
		if len(leases) > 0 {
			if !override {
				return &UpgradeOccupancyError{Tenant: item.Tenant, Environment: item.Environment, Holders: leases}
			}
			ctx.Trace(fmt.Sprintf("upgrade: %s/%s overriding %d held lease(s): %s", item.Tenant, item.Environment, len(leases), activity.FormatLeaseHolders(leases)))
		}
		// Dry-run shows the refusal but must not take the lease.
		if ctx.DryRun {
			return deploy(ctx, item)
		}

		lease, err := activity.TakeEnvironmentActivityLease(activity.TakeEnvironmentActivityLeaseParams{
			Tenant: item.Tenant, Environment: item.Environment, Name: "upgrade", Exclusive: true, Holder: holder, Now: nowValue,
		})
		if err != nil {
			return fmt.Errorf("upgrade: %s/%s: %w", item.Tenant, item.Environment, err)
		}
		defer func() {
			_, _ = activity.ReleaseExclusiveEnvironmentActivityLease(item.Tenant, item.Environment, lease.Scope, lease.ID)
		}()

		return deploy(ctx, item)
	}
}

// UpgradeItemFailure records a member whose deploy returned an error.
type UpgradeItemFailure struct {
	Item  model.UpgradePlanItem `json:"item"`
	Error string                `json:"error"`
}

// UpgradeResult summarizes an upgrade run; unresolved members are never counted as up to date.
type UpgradeResult struct {
	Plan       UpgradePlan             `json:"plan"`
	Upgraded   []model.UpgradePlanItem `json:"upgraded,omitempty"`
	UpToDate   []model.UpgradePlanItem `json:"upToDate,omitempty"`
	Unresolved []model.UpgradePlanItem `json:"unresolved,omitempty"`
	Failed     []UpgradeItemFailure    `json:"failed,omitempty"`
}

// RunUpgradePlan deploys every lagging member, continuing past per-env failures.
func RunUpgradePlan(ctx core.Context, plan UpgradePlan, deploy UpgradeItemDeployer) UpgradeResult {
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
		ctx.Info(upgradeItemAnnouncement(ctx.DryRun, item))
		if err := deploy(ctx, item); err != nil {
			ctx.Trace(fmt.Sprintf("upgrade: %s/%s failed: %s", item.Tenant, item.Environment, err.Error()))
			result.Failed = append(result.Failed, UpgradeItemFailure{Item: item, Error: err.Error()})
			continue
		}
		result.Upgraded = append(result.Upgraded, item)
	}
	ctx.Info(upgradeCompletionSummary(ctx.DryRun, result))
	return result
}

func upgradeItemAnnouncement(dryRun bool, item model.UpgradePlanItem) string {
	verb := "Upgrading"
	if dryRun {
		verb = "Would upgrade"
	}
	return fmt.Sprintf("==> %s %s/%s %s -> %s (%s)", verb, item.Tenant, item.Environment, displayVersion(item.Current), item.Target, item.Channel)
}

func upgradeCompletionSummary(dryRun bool, result UpgradeResult) string {
	if dryRun {
		return fmt.Sprintf("==> Dry run: would upgrade %d, %d up to date, %d unresolved, %d failed", len(result.Upgraded), len(result.UpToDate), len(result.Unresolved), len(result.Failed))
	}
	return fmt.Sprintf("==> Upgrade complete: %d upgraded, %d up to date, %d unresolved, %d failed", len(result.Upgraded), len(result.UpToDate), len(result.Unresolved), len(result.Failed))
}

func unresolvedReasonSuffix(item model.UpgradePlanItem) string {
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

// UpgradePlan is the resolved Upgrade-all plan.
type UpgradePlan struct {
	Items []model.UpgradePlanItem `json:"items"`
}

// Lagging returns the items whose current version differs from a resolvable target.
func (p UpgradePlan) Lagging() []model.UpgradePlanItem {
	lagging := make([]model.UpgradePlanItem, 0, len(p.Items))
	for _, item := range p.Items {
		if item.Lagging {
			lagging = append(lagging, item)
		}
	}
	return lagging
}

func channelTarget(versions model.RuntimeRegistryVersions, channel string) string {
	if strings.TrimSpace(channel) == config.UpgradeChannelSnapshot {
		// No snapshots published means the stable release is the newest candidate, not none.
		if stable, snapshot, superseded := stableSupersedesSnapshot(versions); superseded || snapshot == "" {
			return stable
		}
		return strings.TrimSpace(versions.LatestSnapshot)
	}
	return strings.TrimSpace(versions.LatestStable)
}

// A snapshot is a pre-release of its base version, so a stable at or above that base supersedes it.
// Unparseable versions keep the snapshot.
func stableSupersedesSnapshot(versions model.RuntimeRegistryVersions) (stable, snapshot string, superseded bool) {
	stable = strings.TrimSpace(versions.LatestStable)
	snapshot = strings.TrimSpace(versions.LatestSnapshot)
	if stable == "" || snapshot == "" {
		return stable, snapshot, false
	}
	stableVersion, ok := registrypkg.ParseRegistryStableVersion(stable)
	if !ok {
		return stable, snapshot, false
	}
	base, _, found := strings.Cut(snapshot, "-snapshot-")
	if !found {
		return stable, snapshot, false
	}
	baseVersion, ok := registrypkg.ParseRegistryStableVersion(base)
	if !ok {
		return stable, snapshot, false
	}
	return stable, snapshot, registrypkg.CompareSemver(stableVersion, baseVersion) >= 0
}
