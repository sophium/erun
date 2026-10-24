package diskheadroom

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

// The disk floor keeps a node from filling; this file adds a ceiling on how
// much BuildKit cache one environment may keep. It is a share of the
// environment's own docker volume, not of `df`, which on a quota-less
// node-local claim reports the whole node.

// DockerVolumeBytesEnv carries the volume size from the chart, because a
// quota-less mount reports the node's capacity instead.
const DockerVolumeBytesEnv = "ERUN_DOCKER_VOLUME_BYTES"

// The ceiling leaves room for images and containers a cache prune cannot
// reach; the warning fires a full step before anything is reclaimed.
const (
	buildCacheCeilingPercent = 80
	buildCacheWarningPercent = 70
)

// BuildCacheBounds is the reclaim ceiling and the earlier warning mark.
type BuildCacheBounds struct {
	Ceiling uint64
	Warning uint64
}

// ResolveBuildCacheBounds divides before multiplying to avoid overflow.
func ResolveBuildCacheBounds(volumeBytes uint64) BuildCacheBounds {
	return BuildCacheBounds{
		Ceiling: volumeBytes / 100 * buildCacheCeilingPercent,
		Warning: volumeBytes / 100 * buildCacheWarningPercent,
	}
}

// Per-volume shares do not bound the node's sum, so the ceiling is also
// bounded by the node's disk divided among co-tenants. Both are declared by
// the chart: the pod's namespace-scoped RBAC cannot count its co-tenants.
const (
	BuildCacheNodeBytesEnv = "ERUN_BUILD_CACHE_NODE_BYTES"
	BuildCacheCoTenantsEnv = "ERUN_BUILD_CACHE_CO_TENANTS"
)

// resolveBuildCacheNodeAllowance divides the node's disk above its floor among
// co-tenants. A share too small to hold a working set is refused, never
// clamped up: the ceiling feeds `--max-used-space`, so a tiny one would drop
// the cache every build, and inventing room would take it from other tenants.
func resolveBuildCacheNodeAllowance(nodeBytes uint64, coTenants int) (uint64, error) {
	if coTenants < 1 {
		return 0, fmt.Errorf("%w: %d is not a count of the caches that share the node", errBuildCacheNodeBudgetUnusable, coTenants)
	}
	floor := ResolveMinDiskHeadroomBytes(nodeBytes)
	if nodeBytes <= floor {
		return 0, fmt.Errorf("%w: a %s node reserves %s of its own disk for everything a cache prune cannot reach, which is all of it", errBuildCacheNodeBudgetUnusable, FormatGiB(nodeBytes), FormatGiB(floor))
	}
	allowance := (nodeBytes - floor) / uint64(coTenants)
	ceiling := ResolveBuildCacheBounds(allowance).Ceiling
	// Separate from the check below: a share under a hundred bytes rounds to a
	// zero ceiling even when the reserve check would pass.
	if ceiling == 0 {
		return 0, fmt.Errorf("%w: the %s a %s node holds above its %s floor, divided among %d caches, leaves this one a ceiling of no bytes", errBuildCacheNodeBudgetUnusable, FormatGiB(nodeBytes-floor), FormatGiB(nodeBytes), FormatGiB(floor), coTenants)
	}
	// A ceiling under this environment's share of the reserve is smaller than
	// the non-cache state beside it; the line scales with the node.
	if usable := floor / uint64(coTenants); usable > 0 && ceiling < usable {
		return 0, fmt.Errorf("%w: the %s a %s node holds above its %s floor, divided among %d caches, leaves this one a ceiling of %s — under the %s it would share of the node's reserve, so it is a cache bounded smaller than the state beside it and holds no working set",
			errBuildCacheNodeBudgetUnusable, FormatGiB(nodeBytes-floor), FormatGiB(nodeBytes), FormatGiB(floor), coTenants, FormatGiB(ceiling), FormatGiB(usable))
	}
	return allowance, nil
}

// errNoBuildCacheNodeBudget is the quiet default; the volume share still applies.
var errNoBuildCacheNodeBudget = errors.New("this environment declares no node build-cache budget")

// errBuildCacheNodeBudgetUnusable is traced, because it looks like a bound in
// force and is not.
var errBuildCacheNodeBudgetUnusable = errors.New("the declared node yields no usable share")

// declaredBuildCacheNodeAllowance treats a node size without a co-tenant count
// as incomplete, not as one. The count is returned so the trace can expose
// drift nothing here can detect.
func declaredBuildCacheNodeAllowance() (uint64, int, error) {
	nodeRaw := strings.TrimSpace(os.Getenv(BuildCacheNodeBytesEnv))
	coTenantsRaw := strings.TrimSpace(os.Getenv(BuildCacheCoTenantsEnv))
	if nodeRaw == "" && coTenantsRaw == "" {
		return 0, 0, fmt.Errorf("%w (%s is unset)", errNoBuildCacheNodeBudget, BuildCacheNodeBytesEnv)
	}
	nodeBytes, err := strconv.ParseUint(nodeRaw, 10, 64)
	if err != nil || nodeBytes == 0 {
		return 0, 0, fmt.Errorf("%s=%q is not a positive byte count", BuildCacheNodeBytesEnv, nodeRaw)
	}
	coTenants, err := strconv.Atoi(coTenantsRaw)
	if err != nil || coTenants < 1 {
		return 0, 0, fmt.Errorf("%s=%q is not a positive co-tenant count", BuildCacheCoTenantsEnv, coTenantsRaw)
	}
	allowance, err := resolveBuildCacheNodeAllowance(nodeBytes, coTenants)
	if err != nil {
		return 0, 0, err
	}
	return allowance, coTenants, nil
}

// buildCacheBound carries wording naming its source, since node and volume
// ceilings have different remedies.
type buildCacheBound struct {
	bounds BuildCacheBounds
	// allowedOn names the source of the ceiling, as the subject of a sentence.
	allowedOn string
	// reclaimedFrom is the possessive that follows it.
	reclaimedFrom string
}

// resolveBuildCacheBound lets the node's bound only tighten the volume's.
func resolveBuildCacheBound(ctx core.Context, policy DiskHeadroomPolicy, volumeBytes uint64) buildCacheBound {
	volume := ResolveBuildCacheBounds(volumeBytes)
	bound := buildCacheBound{
		bounds:        volume,
		allowedOn:     fmt.Sprintf("this environment's %s docker volume", FormatGiB(volumeBytes)),
		reclaimedFrom: "that volume's",
	}
	allowance, coTenants, err := declaredBuildCacheNodeAllowance()
	if err != nil {
		if !errors.Is(err, errNoBuildCacheNodeBudget) {
			ctx.Trace(fmt.Sprintf("%s: this environment's share of the node's disk is unusable (%s); its build cache is bounded by its docker volume alone, and nothing on this run bounds the sum across the node's environments", policy.Label, err))
		}
		return bound
	}
	fromNode := ResolveBuildCacheBounds(allowance)
	// Compare ceilings so the wording names the bound actually in force.
	if fromNode.Ceiling < volume.Ceiling {
		bound.bounds = fromNode
		// The unverifiable co-tenant count is named so drift shows in the log.
		bound.allowedOn = fmt.Sprintf("the %s this environment is allowed of the node, divided among the %d build caches the deployment declares share it", FormatGiB(allowance), coTenants)
		bound.reclaimedFrom = "that share's"
	}
	return bound
}

// ErrNoDockerVolume is the expected, untraced state of runtime and host envs.
var ErrNoDockerVolume = errors.New("this environment declares no docker volume")

// DockerVolumeBytes refuses absent or malformed sizes rather than reclaim
// against an invented number.
func DockerVolumeBytes() (uint64, error) {
	raw := strings.TrimSpace(os.Getenv(DockerVolumeBytesEnv))
	if raw == "" {
		return 0, fmt.Errorf("%w (%s is unset)", ErrNoDockerVolume, DockerVolumeBytesEnv)
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, fmt.Errorf("%s=%q is not a positive byte count", DockerVolumeBytesEnv, raw)
	}
	return value, nil
}

// dockerBuildCacheBytes is a lower bound (docker understates build cache), so
// a reclaim can only fire late, never early.
func dockerBuildCacheBytes(limit time.Duration) (uint64, error) {
	out, err := diskHeadroomOutput(limit, "docker", "system", "df", "--format", "{{.Type}}|{{.Size}}")
	if err != nil {
		return 0, diskHeadroomReadFailure(limit, "docker system df", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name, value, found := strings.Cut(strings.TrimSpace(line), "|")
		if !found || !strings.EqualFold(strings.TrimSpace(name), "build cache") {
			continue
		}
		size, ok := ParseDockerSize(value)
		if !ok {
			return 0, errors.New("docker system df reported an unreadable build-cache size")
		}
		return size, nil
	}
	return 0, errors.New("docker system df reported no build cache row")
}

// EnsureBuildCacheRetention never refuses a run; the disk floor decides that.
func EnsureBuildCacheRetention(ctx core.Context, policy DiskHeadroomPolicy) {
	EnsureBuildCacheRetentionWith(ctx, policy, DockerVolumeBytes, dockerBuildCacheBytes, runBuildCacheRetentionPrune)
}

// EnsureBuildCacheRetentionWith takes injected readings for tests.
func EnsureBuildCacheRetentionWith(
	ctx core.Context,
	policy DiskHeadroomPolicy,
	readVolume func() (uint64, error),
	readCacheBytes func(time.Duration) (uint64, error),
	prune func(ceiling uint64, limit time.Duration) error,
) {
	// A dry run stays silent: whether a reclaim is due depends on the cache's
	// current size, which a preview cannot know.
	if ctx.DryRun {
		return
	}

	volume, err := readVolume()
	if err != nil {
		if errors.Is(err, ErrNoDockerVolume) {
			return
		}
		ctx.Trace(fmt.Sprintf("%s: no build-cache ceiling applies this run (%s)", policy.Label, err))
		return
	}
	cacheBytes, err := readCacheBytes(policy.Limits.Read)
	if err != nil {
		// Never reclaim an unmeasured cache.
		ctx.Trace(fmt.Sprintf("%s: the build cache's size could not be read (%s); leaving it bounded by the disk floor alone", policy.Label, err))
		return
	}

	bound := resolveBuildCacheBound(ctx, policy, volume)
	bounds := bound.bounds
	ctx.Trace(fmt.Sprintf("%s: the build cache holds %s of the %s allowed on %s",
		policy.Label, FormatGiB(cacheBytes), FormatGiB(bounds.Ceiling), bound.allowedOn))

	if cacheBytes < bounds.Warning {
		return
	}
	if cacheBytes < bounds.Ceiling {
		ctx.Info(fmt.Sprintf("warning: the build cache holds %s, past the %s mark on %s; "+
			"it is reclaimed down to %s %s ceiling automatically, and until then it is space this environment holds against every other tenant of the node",
			FormatGiB(cacheBytes), FormatGiB(bounds.Warning), bound.allowedOn, bound.reclaimedFrom, FormatGiB(bounds.Ceiling)))
		return
	}

	ctx.Trace(fmt.Sprintf("%s: the build cache is at or over this environment's %s ceiling; reclaiming it down to it",
		policy.Label, FormatGiB(bounds.Ceiling)))
	ctx.TraceCommand("", "docker", "buildx", "prune", "-f", "--max-used-space", strconv.FormatUint(bounds.Ceiling, 10))
	if err := prune(bounds.Ceiling, policy.Limits.Prune); err != nil {
		ctx.Trace(fmt.Sprintf("%s: the build-cache reclaim did not complete (%s), so the cache is still holding what it held", policy.Label, err))
	}
}

// runBuildCacheRetentionPrune must use `docker buildx prune`: the classic
// `builder prune` rejects --max-used-space as unknown.
func runBuildCacheRetentionPrune(ceiling uint64, limit time.Duration) error {
	err := diskHeadroomRun(limit, "docker", "buildx", "prune", "-f", "--max-used-space", strconv.FormatUint(ceiling, 10))
	return diskHeadroomReadFailure(limit, "docker buildx prune", err)
}
