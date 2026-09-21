package eruncommon

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// release_disk_headroom.go's preflight keeps a node from being *filled*: it
// reacts once the docker root's free space is already below a floor. Nothing on
// that path — nor anywhere else a build runs — ever says how large this
// environment's build cache may become. So between two floor crossings the
// cache grows without bound, and on a node shared by several environments the
// first one to cross the floor triggers a prune whose freed bytes belong to the
// node, leaving every other environment's next build to repay its layers from
// cold.
//
// A floor and a ceiling answer different questions. The floor asks "is there
// room for this build?"; the ceiling asks "how much of what this environment
// has already cached may it keep?". Only the first was wired, so this file adds
// the second: the environment's own BuildKit cache is bounded to a share of the
// docker volume it lives on, judged on every build rather than only once free
// space has run out, and reported before the bound is reached so the growth is
// visible while it is still a cost rather than a failure.
//
// The bound is per environment on purpose. Each environment's dind sidecar owns
// its own docker volume (the chart's `-docker` claim, mounted at
// /var/lib/docker), so a share of that volume is a bound no single environment
// can exceed on behalf of the others. It is deliberately not a share of the
// filesystem the docker root reports: where that claim is backed by a
// node-local volume carrying no quota, `df` at /var/lib/docker reports the
// whole node, every environment on it reads the same total, and a share of that
// would bound nothing any one environment controls.

// dockerVolumeBytesEnv carries the size of this environment's docker volume, in
// bytes, from the chart that declares the claim to the process that builds
// inside it. The number has to travel with the deployment rather than be
// discovered at runtime, because the pod cannot read the claim's requested size
// from the mounted filesystem: a node-local volume has no quota, so the mount
// reports the node's capacity instead.
const dockerVolumeBytesEnv = "ERUN_DOCKER_VOLUME_BYTES"

// The two marks, as shares of that volume.
//
// The ceiling leaves a fifth of the volume to everything on it a build-cache
// prune cannot reach — images, containers, local volumes. The node that
// produced this bound held tens of gigabytes of images beside its cache in
// every environment measured, so a ceiling that let the cache claim the whole
// volume would bound nothing that matters.
//
// The warning sits a tenth of the volume below the ceiling, so growth is
// reported a full step before anything is reclaimed. A warning that fires at
// the same threshold as the remedy is not a warning; it is a status line
// printed after the decision has already been taken.
const (
	buildCacheCeilingPercent = 80
	buildCacheWarningPercent = 70
)

// buildCacheBounds is the two byte marks one environment's cache is judged
// against: the ceiling it is reclaimed down to, and the lower mark that reports
// growth before anything is reclaimed.
type buildCacheBounds struct {
	ceiling uint64
	warning uint64
}

// resolveBuildCacheBounds turns the volume's declared size into those marks.
// Divide before multiplying, as the disk floor does: a volume size is a byte
// count of an arbitrary volume, and the product would overflow on a large
// enough one.
func resolveBuildCacheBounds(volumeBytes uint64) buildCacheBounds {
	return buildCacheBounds{
		ceiling: volumeBytes / 100 * buildCacheCeilingPercent,
		warning: volumeBytes / 100 * buildCacheWarningPercent,
	}
}

// dockerVolumeBytes reads the size the deployment declared for this
// environment's docker volume. An absent variable is not a volume of size zero,
// and a malformed one is not a size to build a bound out of: both are reported
// as "no ceiling applies, and here is why", so the run proceeds under the disk
// floor alone rather than reclaiming against an invented number. An environment
// with no docker volume at all — a runtime env runs no dind sidecar — is the
// common case for the absent branch, and it has no cache to bound.
func dockerVolumeBytes() (uint64, error) {
	raw := strings.TrimSpace(os.Getenv(dockerVolumeBytesEnv))
	if raw == "" {
		return 0, fmt.Errorf("%s is unset, so this environment's docker volume size is unknown", dockerVolumeBytesEnv)
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, fmt.Errorf("%s=%q is not a positive byte count", dockerVolumeBytesEnv, raw)
	}
	return value, nil
}

// dockerBuildCacheBytes reads how much this environment's build cache holds,
// from the same `docker system df` the headroom path already reads for what it
// could free. The two questions need different columns — a size to compare
// against a ceiling, a reclaimable figure to size a prune — so this is its own
// reading rather than a second field on dockerReclaimable, whose name says what
// it holds.
//
// Treat the figure as a lower bound, for the same measured reason
// dockerReclaimableBytes does: docker's own accounting for build cache has
// understated what a prune actually frees by several times over on a real node.
// A lower bound is what makes the ceiling safe to act on — it can only report
// the cache as smaller than it is, so a reclaim fires late rather than
// destroying a cache that was inside its bound.
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
		size, ok := parseDockerSize(value)
		if !ok {
			return 0, errors.New("docker system df reported an unreadable build-cache size")
		}
		return size, nil
	}
	return 0, errors.New("docker system df reported no build cache row")
}

// ensureBuildCacheRetention bounds this environment's build cache to its share
// of the environment's own docker volume. It never refuses a run: a cache over
// its ceiling is reclaimed, and one that cannot be reclaimed is reported and
// left to the disk floor, which is the guard that decides whether a build may
// proceed. Both the build and the release path call it, because both grow the
// same cache.
func ensureBuildCacheRetention(ctx Context, policy diskHeadroomPolicy) {
	ensureBuildCacheRetentionWith(ctx, policy, dockerVolumeBytes, dockerBuildCacheBytes, runBuildCacheRetentionPrune)
}

// ensureBuildCacheRetentionWith holds the decision logic, with its readings
// injected for the same reason ensureDiskHeadroomWith's are: the policy is the
// thing under test, and driving it against a real docker daemon is neither
// deterministic nor safe to do from inside this repository.
func ensureBuildCacheRetentionWith(
	ctx Context,
	policy diskHeadroomPolicy,
	readVolume func() (uint64, error),
	readCacheBytes func(time.Duration) (uint64, error),
	prune func(ceiling uint64, limit time.Duration) error,
) {
	ctx.TraceCommand("", "docker", "system", "df", "--format", "{{.Type}}|{{.Size}}")
	if ctx.DryRun {
		return
	}

	volume, err := readVolume()
	if err != nil {
		ctx.Trace(fmt.Sprintf("%s: no build-cache ceiling applies this run (%s)", policy.label, err))
		return
	}
	cacheBytes, err := readCacheBytes(policy.limits.read)
	if err != nil {
		// An unreadable size is not evidence that the cache is within its
		// ceiling, but neither is it evidence that it is over one: reclaiming an
		// unmeasured cache would destroy layers on a guess.
		ctx.Trace(fmt.Sprintf("%s: the build cache's size could not be read (%s); leaving it bounded by the disk floor alone", policy.label, err))
		return
	}

	bounds := resolveBuildCacheBounds(volume)
	ctx.Trace(fmt.Sprintf("%s: the build cache holds %s of the %s allowed on this environment's %s docker volume",
		policy.label, formatGiB(cacheBytes), formatGiB(bounds.ceiling), formatGiB(volume)))

	if cacheBytes < bounds.warning {
		return
	}
	if cacheBytes < bounds.ceiling {
		ctx.Info(fmt.Sprintf("warning: the build cache holds %s, past the %s mark on this environment's %s docker volume; "+
			"it is reclaimed down to that volume's %s ceiling automatically, and until then it is space this environment holds against every other tenant of the node",
			formatGiB(cacheBytes), formatGiB(bounds.warning), formatGiB(volume), formatGiB(bounds.ceiling)))
		return
	}

	ctx.Trace(fmt.Sprintf("%s: the build cache is at or over this environment's %s ceiling; reclaiming it down to it",
		policy.label, formatGiB(bounds.ceiling)))
	ctx.TraceCommand("", "docker", "buildx", "prune", "-f", "--max-used-space", strconv.FormatUint(bounds.ceiling, 10))
	if err := prune(bounds.ceiling, policy.limits.prune); err != nil {
		// The reclaim is not what the run depends on, so its failure is not
		// fatal — but it must not read as an act that happened. A prune that
		// never ran leaves the cache holding exactly what it held.
		ctx.Trace(fmt.Sprintf("%s: the build-cache reclaim did not complete (%s), so the cache is still holding what it held", policy.label, err))
	}
}

// runBuildCacheRetentionPrune reclaims the cache down to the ceiling, through
// the command that implements the bound.
//
// It is bounded by --max-used-space where the disk-floor prune is bounded by
// --min-free-space, because those are the two different questions the two
// guards ask. And it is `docker buildx prune` rather than `docker builder
// prune` for the same reason: the classic command implements only --all,
// --filter, --force and --keep-storage, so a bound handed to it is rejected as
// an unknown flag — a reclaim that never happened, behind a trace that says it
// did, leaving the shortfall to be measured afterwards against a cache nothing
// moved.
//
// It descends no further than the ceiling, so a cache that is over its bound by
// a little is reclaimed by a little. Reclaiming everything and still failing is
// strictly worse than refusing to start.
func runBuildCacheRetentionPrune(ceiling uint64, limit time.Duration) error {
	err := diskHeadroomRun(limit, "docker", "buildx", "prune", "-f", "--max-used-space", strconv.FormatUint(ceiling, 10))
	return diskHeadroomReadFailure(limit, "docker buildx prune", err)
}
