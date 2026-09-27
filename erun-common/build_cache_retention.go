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

// A share of one environment's own volume is not a bound on the node. Four
// environments each honouring their own 80% of a declared 50 GiB volume hold
// 160 GiB between them, and every one of them is inside its own ceiling while
// the node fills: the sum is what crosses the eviction threshold, and nothing
// here ever looked at it. The volume share says how much of *what this
// environment was sized for* it may keep; it cannot say how much of the node
// that is.
//
// So the ceiling is additionally bounded by the node's disk, divided among the
// environments whose caches share it. Both inputs travel as environment
// variables for the same reason the volume's does, and neither is discoverable
// from inside the pod: the node's tenants are invisible to it — its RBAC is
// namespace-scoped, so it cannot list the other environments, let alone count
// them — and a node-local, quota-less claim reports the node's capacity rather
// than its own size. The count has to be stated by whoever knows the node, not
// inferred by each tenant guessing at the others.
const (
	buildCacheNodeBytesEnv = "ERUN_BUILD_CACHE_NODE_BYTES"
	buildCacheCoTenantsEnv = "ERUN_BUILD_CACHE_CO_TENANTS"
)

// resolveBuildCacheNodeAllowance is the most one environment's build cache may
// hold once the node's own disk floor is set aside and what is left is divided
// among the environments whose caches share it.
//
// The reserve it withholds is the disk-headroom floor's own answer, taken from
// the same function that decides when a build refuses and when a prune runs, so
// a cache cannot be bounded to a size that walks the node onto the threshold
// that floor exists to hold it off. Dividing what is left is the whole point: a
// per-environment bound that does not account for how many environments share
// the node bounds nothing, because the sum of the parts is the only figure
// kubelet ever evicts on.
//
// A node at or below its own floor has nothing left to divide, and a co-tenant
// count below one is not a count. Neither yields an allowance, and the caller
// falls back to the volume share alone rather than reclaiming against an
// invented number.
//
// Every way this can fail to produce a share is an error and none of them is a
// zero allowance, because the two are not the same thing to the caller that
// acts on the result: an allowance of zero is passed to `resolveBuildCacheBounds`,
// comes back as a ceiling of no bytes, and is reclaimed to as
// `--max-used-space 0`. That is not a small bound. It drops every earned layer
// on every build and the ceiling never rises afterwards, because a cache
// holding nothing is never over a ceiling of nothing — so a node whose figures
// leave no room to divide leaves the volume share in force and says so, rather
// than reaching that state through arithmetic that returned a real-looking
// number.
//
// The floor is not a value this can be clamped up to. A byte floor here would
// not be the honest analogue of RuntimeDindCPULimit's MinimumRuntimeDindCPU: a
// CPU limit is a ceiling the kernel shares fairly, so raising a small one costs
// nothing when the node is idle, while a disk ceiling is space the environment
// then holds against every other tenant of that node. Inventing room a node has
// told us it does not have is the aggregate defect this bound exists to
// prevent, so the unusable case falls back to the bound that was already in
// force instead of to a number nobody measured.
func resolveBuildCacheNodeAllowance(nodeBytes uint64, coTenants int) (uint64, error) {
	if coTenants < 1 {
		return 0, fmt.Errorf("%w: %d is not a count of the caches that share the node", errBuildCacheNodeBudgetUnusable, coTenants)
	}
	floor := resolveMinDiskHeadroomBytes(nodeBytes)
	if nodeBytes <= floor {
		return 0, fmt.Errorf("%w: a %s node reserves %s of its own disk for everything a cache prune cannot reach, which is all of it", errBuildCacheNodeBudgetUnusable, formatGiB(nodeBytes), formatGiB(floor))
	}
	allowance := (nodeBytes - floor) / uint64(coTenants)
	// The share survives the division but not the ceiling it is a share of:
	// resolveBuildCacheBounds divides before it multiplies, so a share under a
	// hundred bytes has a ceiling of zero however much room the node had. That
	// is the same no-bound state by the same argument, and it is reached
	// without the node ever being at or below its floor.
	if resolveBuildCacheBounds(allowance).ceiling == 0 {
		return 0, fmt.Errorf("%w: the %s a %s node holds above its %s floor, divided among %d caches, leaves this one a ceiling of no bytes", errBuildCacheNodeBudgetUnusable, formatGiB(nodeBytes-floor), formatGiB(nodeBytes), formatGiB(floor), coTenants)
	}
	return allowance, nil
}

// errNoBuildCacheNodeBudget is the state of an environment whose deployment has
// said nothing about the node it lives on. It is the one failure here that is
// not news: it is how every environment ran before this bound existed, and the
// volume share above still applies to it unchanged. Only a declaration that is
// present and unusable is a deployment mistake worth tracing.
var errNoBuildCacheNodeBudget = errors.New("this environment declares no node build-cache budget")

// errBuildCacheNodeBudgetUnusable is the state of a deployment that has
// described its node but whose description leaves this environment no usable
// share of it — a node at or below the disk floor the build refuses against, or
// a share that divides down to no ceiling at all. It is separate from
// errNoBuildCacheNodeBudget because it is the opposite silence: a bound that
// looks like it is in force and is not, so it is traced rather than passed
// over. It is never resolved by inventing a share.
var errBuildCacheNodeBudgetUnusable = errors.New("the declared node yields no usable share")

// declaredBuildCacheNodeAllowance reads the node's disk and the number of
// environments sharing it.
//
// Both are plain environment reads, so unlike the docker-daemon readings they
// are taken directly rather than injected — the same reason the disk floor
// reads its own override where it resolves it. There is no daemon behind them
// and nothing non-deterministic to substitute.
//
// A node size with no co-tenant count is an incomplete declaration, not a count
// of one. Assuming one would hand a single environment the node's whole cache
// budget, which is precisely the unbounded shape this bound exists to remove.
//
// The count is returned alongside the allowance so the trace can name it. It is
// a declared value nothing cross-checks — the pod's RBAC is namespace-scoped,
// so it cannot count its own co-tenants to compare against — and a declaration
// that has drifted from the node is exactly the state that puts the aggregate
// back where it started. Naming the number in the line that reports the ceiling
// is what makes that drift visible to whoever reads the log, since nothing on
// this path can detect it.
func declaredBuildCacheNodeAllowance() (uint64, int, error) {
	nodeRaw := strings.TrimSpace(os.Getenv(buildCacheNodeBytesEnv))
	coTenantsRaw := strings.TrimSpace(os.Getenv(buildCacheCoTenantsEnv))
	if nodeRaw == "" && coTenantsRaw == "" {
		return 0, 0, fmt.Errorf("%w (%s is unset)", errNoBuildCacheNodeBudget, buildCacheNodeBytesEnv)
	}
	nodeBytes, err := strconv.ParseUint(nodeRaw, 10, 64)
	if err != nil || nodeBytes == 0 {
		return 0, 0, fmt.Errorf("%s=%q is not a positive byte count", buildCacheNodeBytesEnv, nodeRaw)
	}
	coTenants, err := strconv.Atoi(coTenantsRaw)
	if err != nil || coTenants < 1 {
		return 0, 0, fmt.Errorf("%s=%q is not a positive co-tenant count", buildCacheCoTenantsEnv, coTenantsRaw)
	}
	allowance, err := resolveBuildCacheNodeAllowance(nodeBytes, coTenants)
	if err != nil {
		return 0, 0, err
	}
	return allowance, coTenants, nil
}

// buildCacheBound is the ceiling this environment's build cache is held to,
// together with the wording that names what it was derived from. The two
// wordings are carried rather than re-derived at each message because a ceiling
// that came from the node and one that came from the volume's declared size are
// different bounds with different remedies, and an operator reading either line
// has to be able to tell which one is in force.
type buildCacheBound struct {
	bounds buildCacheBounds
	// allowedOn names the source of the ceiling, as the subject of a sentence.
	allowedOn string
	// reclaimedFrom is the possessive that follows it.
	reclaimedFrom string
}

// resolveBuildCacheBound is the ceiling for this environment: a share of the
// docker volume it was given, tightened by its share of the node its co-tenants
// also build on.
//
// The node's bound is applied on top of the volume's and never instead of it,
// so it can only tighten the ceiling. An environment that declares no node
// budget, or whose share of the node is still larger than the volume it was
// given, keeps exactly the bound it had before — which is why the volume's own
// wording is returned unchanged for that case.
func resolveBuildCacheBound(ctx Context, policy diskHeadroomPolicy, volumeBytes uint64) buildCacheBound {
	volume := resolveBuildCacheBounds(volumeBytes)
	bound := buildCacheBound{
		bounds:        volume,
		allowedOn:     fmt.Sprintf("this environment's %s docker volume", formatGiB(volumeBytes)),
		reclaimedFrom: "that volume's",
	}
	allowance, coTenants, err := declaredBuildCacheNodeAllowance()
	if err != nil {
		// A node budget declared but unusable is a deployment mistake: it leaves
		// the aggregate unbounded by the node while looking like it is bounded,
		// so it is named rather than passed over. No declaration at all is the
		// state every environment was in before this bound existed, and stays
		// quiet.
		if !errors.Is(err, errNoBuildCacheNodeBudget) {
			ctx.Trace(fmt.Sprintf("%s: this environment's share of the node's disk is unusable (%s); its build cache is bounded by its docker volume alone, and nothing on this run bounds the sum across the node's environments", policy.label, err))
		}
		return bound
	}
	fromNode := resolveBuildCacheBounds(allowance)
	// A share that resolved to the volume's own number is not a node bound, and
	// saying the cache is allowed it "of the node" would attribute a bound the
	// node did not set. The comparison is on the ceiling rather than on the
	// share so the wording follows the bound that is actually in force.
	if fromNode.ceiling < volume.ceiling {
		bound.bounds = fromNode
		// The co-tenant count is named because nothing here can check it: it is
		// a chart value, the pod cannot count its own co-tenants, and a count
		// that has drifted from the node is the one way this bound silently
		// stops bounding the sum. Naming it puts the drift in the log that
		// reports the ceiling it produced.
		bound.allowedOn = fmt.Sprintf("the %s this environment is allowed of the node, divided among the %d build caches the deployment declares share it", formatGiB(allowance), coTenants)
		bound.reclaimedFrom = "that share's"
	}
	return bound
}

// errNoDockerVolume is the expected state of an environment that has no docker
// volume to bound: a runtime env runs no dind sidecar, and a host build runs
// against a daemon nobody declared a size for. It is separated from the other
// ways the size can fail to resolve because it is the one of them that is not
// news — an environment with no volume has no cache to bound, and saying so on
// every build of every such environment would be noise around the case that
// does matter.
var errNoDockerVolume = errors.New("this environment declares no docker volume")

// dockerVolumeBytes reads the size the deployment declared for this
// environment's docker volume. An absent variable is not a volume of size zero,
// and a malformed one is not a size to build a bound out of: neither is
// reclaimed against, so the run proceeds under the disk floor alone rather than
// destroying cache against an invented number.
func dockerVolumeBytes() (uint64, error) {
	raw := strings.TrimSpace(os.Getenv(dockerVolumeBytesEnv))
	if raw == "" {
		return 0, fmt.Errorf("%w (%s is unset)", errNoDockerVolume, dockerVolumeBytesEnv)
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
// reading rather than a second figure on the headroom path's reclaimable one.
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
	// A dry run reports nothing here, and that is a deliberate exception to how
	// the disk-headroom check beside it behaves: that one previews its own read
	// because the read is its whole deliverable, while a preview of this one
	// cannot say anything a reader could act on — whether a reclaim is due is a
	// function of how much cache exists right now, which no preview knows. What
	// a dry run must not do is imply a bound is in force where none is; it stays
	// silent rather than printing a check it did not run.
	if ctx.DryRun {
		return
	}

	volume, err := readVolume()
	if err != nil {
		if errors.Is(err, errNoDockerVolume) {
			return
		}
		// A size the deployment declared but this process cannot use is not the
		// same quiet state: it is a deployment mistake, and one that would
		// otherwise leave the cache unbounded with nothing to say why.
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

	bound := resolveBuildCacheBound(ctx, policy, volume)
	bounds := bound.bounds
	ctx.Trace(fmt.Sprintf("%s: the build cache holds %s of the %s allowed on %s",
		policy.label, formatGiB(cacheBytes), formatGiB(bounds.ceiling), bound.allowedOn))

	if cacheBytes < bounds.warning {
		return
	}
	if cacheBytes < bounds.ceiling {
		ctx.Info(fmt.Sprintf("warning: the build cache holds %s, past the %s mark on %s; "+
			"it is reclaimed down to %s %s ceiling automatically, and until then it is space this environment holds against every other tenant of the node",
			formatGiB(cacheBytes), formatGiB(bounds.warning), bound.allowedOn, bound.reclaimedFrom, formatGiB(bounds.ceiling)))
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
