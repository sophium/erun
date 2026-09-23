package eruncommon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// A multi-arch, many-image release is the single build most likely to fill a
// node's disk — exactly what evicted the pod mid-release once before (a 12-image
// multi-arch build filled the node, kubelet raised DiskPressure, pods were
// evicted, and the release that filled the disk was the one killed by it).
// This file is the preflight that exists to keep a release from doing that to
// itself, in the same spirit as the registry-permission preflight in
// build_run.go: a knowable, avoidable failure caught before the build spends
// anything, not discovered by the build itself running out of room.
//
// Ordinary builds run the same check. They are individually far smaller than a
// release, but they are also far more frequent, and the BuildKit cache they
// leave behind grows monotonically — so between two releases a run of plain
// builds can walk the node all the way to the eviction threshold with nothing
// on that path ever looking at free space. Guarding only the release leaves the
// common case unguarded.
//
// The floor has to sit *above* kubelet's eviction threshold to be worth
// anything. kubelet's evictionHard for nodefs defaults to 5% available, and it
// evicts every pod on the node when it is crossed; a floor below that line can
// only ever prune after the eviction it exists to prevent has already started.
// So the floor is proportional to the filesystem, at kubelet's own
// evictionMinimumReclaim level, with the absolute byte floor as a lower bound
// for small disks where a percentage is too little to fit a release.

// releaseMinDiskHeadroomEnv overrides the resolved floor outright. Not a
// production knob to reach for casually — it exists so a constrained test or
// a genuinely small node can tune the floor without recompiling.
const releaseMinDiskHeadroomEnv = "ERUN_RELEASE_MIN_DISK_HEADROOM_BYTES"

// releaseMinDiskHeadroomBytes is the absolute lower bound on the floor: a
// released multi-arch, many-image build has consumed tens of gigabytes on the
// reported incident node, so headroom well under that is already too little to
// safely absorb one more release.
const releaseMinDiskHeadroomBytes uint64 = 20 << 30 // 20 GiB

// minDiskHeadroomPercent is the floor as a share of the whole filesystem.
// It matches kubelet's default evictionMinimumReclaim for nodefs — the level
// kubelet itself reclaims *to* once it has started evicting — so holding that
// line proactively keeps the node off the 5% evictionHard cliff entirely
// rather than reacting after pods are already being killed.
const minDiskHeadroomPercent uint64 = 10

// Every subprocess this preflight runs against the docker daemon is bounded.
// The daemon it talks to can stop answering — observed on a real node as a
// readiness probe's two-second `docker info` timing out while buildkit held a
// live Solve — and an unbounded call there is worse than a failed one: the
// preflight never returns, so the build it fronts neither proceeds nor fails
// and writes no terminal record at all. A bound turns that into a state the
// caller can report and act on.
//
// The three bounds differ because what they wait on differs. The plain CLI
// reads return in milliseconds on a healthy daemon; the probe is a container
// run that may first pull a small image; the prune is a real reclaim of a
// build cache measured in tens of gigabytes, which has taken tens of seconds
// on the node this floor exists for.
const (
	diskHeadroomReadTimeout  = 15 * time.Second
	diskHeadroomProbeTimeout = 60 * time.Second
	diskHeadroomPruneTimeout = 5 * time.Minute
)

// diskHeadroomTimeouts are those bounds as one value, carried on the policy so
// each caller binds them explicitly and a test can shorten them without
// mutating anything shared.
type diskHeadroomTimeouts struct {
	read  time.Duration
	probe time.Duration
	prune time.Duration
}

// diskHeadroomDefaultTimeouts is what both production callers bind.
var diskHeadroomDefaultTimeouts = diskHeadroomTimeouts{
	read:  diskHeadroomReadTimeout,
	probe: diskHeadroomProbeTimeout,
	prune: diskHeadroomPruneTimeout,
}

// diskHeadroomMeasurement is one capacity's free/total read together with what
// the read established about the filesystem it came from. The path travels
// with the numbers — and with the environment-owned paths measured to share
// that filesystem — because the remedy a verdict names has to match the space
// that was actually measured, and the space a release runs out of is not
// always docker's to free.
type diskHeadroomMeasurement struct {
	free  uint64
	total uint64
	// path is where the read was taken.
	path string
	// sharedPaths are the environment's own space holders (work checkouts,
	// caches) that were measured to sit on the same filesystem as path. Empty
	// when that could not be established, which is not a claim that they do
	// not: an unmeasured answer is left out rather than guessed.
	sharedPaths []string
}

// diskHeadroomFreeSpaceFunc reads the docker root's current free and total
// space, returning an error when the read is inconclusive. Total is what makes
// the floor proportional; it is 0 when the read could not determine it, which
// falls back to the absolute floor. Injectable so the decision logic in
// ensureDiskHeadroomWith can be unit-tested without a real docker daemon. The
// error names why the read failed — a daemon that did not answer within its
// bound is a different state from one that is simply absent, and only the read
// that knows which can say so.
type diskHeadroomFreeSpaceFunc func(limits diskHeadroomTimeouts) (diskHeadroomMeasurement, error)

// diskHeadroomReclaimableFunc reports what a prune of docker's own stores has
// to free, so the remedy a message names can match what was measured. The
// reported figure is a lower bound, not a size estimate — see
// dockerReclaimableBytes — so it is only trusted to say "nothing at all",
// never to say "not enough". An error is treated as "docker's stores may
// still hold something" rather than "they hold nothing": an unknown is not
// evidence that a remedy is inert.
type diskHeadroomReclaimableFunc func(limit time.Duration) (uint64, error)

// diskHeadroomCacheReclaimableFunc reports what BuildKit's own build cache
// could still free. It is a separate reading from the one above because the
// two cannot see the same bytes: docker attributes a build's layers to Images
// for as long as the image holding them exists, so `docker system df`'s Build
// Cache column reads 0B on exactly the store this check exists for — one full
// of previously-built images — while BuildKit itself reports the tens of
// gigabytes it can still free. See dockerBuildCacheReclaimable.
type diskHeadroomCacheReclaimableFunc func(limit time.Duration) (uint64, error)

// diskHeadroomPruneFunc bounds a build-cache prune to leave at least floor
// bytes free. Injectable for the same reason as diskHeadroomFreeSpaceFunc.
type diskHeadroomPruneFunc func(floor uint64, limit time.Duration) error

// diskHeadroomPolicy is what differs between the two callers: the word used in
// traces, and whether a disk still below the floor after the prune stops the
// run. A release refuses, because it is about to spend tens of minutes and tens
// of gigabytes and would take the node down with it. A build only warns: it is
// small enough that proceeding is usually fine, the prune it just ran is the
// protective act, and refusing every build on a full node would block the
// operator from the very work that clears it.
type diskHeadroomPolicy struct {
	label  string
	refuse bool
	// limits bound every subprocess this policy's check runs. Carried here
	// rather than fixed in the implementations so the decision logic and the
	// real commands can be exercised together against a daemon that stops
	// answering, without waiting out a production bound to do it.
	limits diskHeadroomTimeouts
}

var (
	releaseDiskHeadroomPolicy = diskHeadroomPolicy{label: "release", refuse: true, limits: diskHeadroomDefaultTimeouts}
	buildDiskHeadroomPolicy   = diskHeadroomPolicy{label: "build", refuse: false, limits: diskHeadroomDefaultTimeouts}
)

// ensureReleaseDiskHeadroom reads the docker root's free space before a
// release's build starts and, only when it is actually below the floor,
// prunes reclaimable build cache down to that floor and refuses if the disk
// is still too full afterward — rather than letting the build itself trigger
// the eviction it cannot recover from.
func ensureReleaseDiskHeadroom(ctx Context) error {
	// The ceiling is checked first and unconditionally: it is a bound on how
	// much cache this environment may hold, so it has to hold whether or not
	// free space happens to be short this time. Reaching it also frees room,
	// which is why it runs before the floor is measured rather than after.
	ensureBuildCacheRetention(ctx, releaseDiskHeadroomPolicy)
	return ensureDiskHeadroomWith(ctx, releaseDiskHeadroomPolicy, dockerRootDiskBytes, dockerReclaimableBytes, dockerBuildCacheReclaimable, runDiskHeadroomPrune)
}

// ensureBuildDiskHeadroom is the same preflight for an ordinary build, which
// warns instead of refusing. It is the one that actually runs between releases,
// where the cache growth that fills a node happens.
func ensureBuildDiskHeadroom(ctx Context) error {
	ensureBuildCacheRetention(ctx, buildDiskHeadroomPolicy)
	return ensureDiskHeadroomWith(ctx, buildDiskHeadroomPolicy, dockerRootDiskBytes, dockerReclaimableBytes, dockerBuildCacheReclaimable, runDiskHeadroomPrune)
}

// ensureDiskHeadroomWith holds the decision logic: read first, prune whenever
// the disk is below the floor, then re-check before refusing. The docker daemon
// a build runs against often lives in a separate container (the erun-dind
// sidecar) with its own filesystem, so readFree makes its own attempt to reach
// that daemon's filesystem before giving up; when it still cannot, that
// inconclusive read is not an answer — the same "known failure over invented
// behavior" posture as ensureReleaseBaseBranchUnmoved — so it lets the run
// proceed exactly as it does today, with no prune at all.
func ensureDiskHeadroomWith(ctx Context, policy diskHeadroomPolicy, readFree diskHeadroomFreeSpaceFunc, readReclaimable diskHeadroomReclaimableFunc, readCacheReclaimable diskHeadroomCacheReclaimableFunc, prune diskHeadroomPruneFunc) error {
	ctx.TraceCommand("", "docker", "info", "-f", "{{.DockerRootDir}}")
	if ctx.DryRun {
		return nil
	}

	measured, readErr := readFree(policy.limits)
	if readErr != nil {
		ctx.Trace(fmt.Sprintf("%s: docker root free disk space is not observable from this process (%s); skipping the headroom check", policy.label, readErr))
		return nil
	}
	floor := resolveMinDiskHeadroomBytes(measured.total)
	ctx.Trace(fmt.Sprintf("%s: docker root has %s free of %s (floor %s)", policy.label, formatGiB(measured.free), formatGiB(measured.total), formatGiB(floor)))
	if measured.free >= floor {
		return nil
	}

	// The reported figures are only ever trusted to detect "nothing to
	// reclaim", never to size whether a prune can close the gap: docker's own
	// accounting for build cache has understated what a real prune frees by
	// several times over on a real node, and declining on a figure that
	// understates in that direction refuses releases a prune would have
	// rescued. What they are not trusted for at all is deciding whether to
	// prune, because that question this reading is structurally unable to
	// answer — see the prune below.
	reclaimable, reclaimErr := readReclaimable(policy.limits.read)
	known := reclaimErr == nil

	// The prune is attempted on every disk below the floor, not only when a
	// figure says there is something to reclaim. `docker system df` cannot
	// answer that question: a layer a build produced is attributed to Images
	// for as long as the image holding it exists, and becomes Build Cache only
	// once that image is removed — so on the store this check exists for, one
	// full of previously-built images, the Build Cache column reads 0B while
	// the cache holds tens of gigabytes a prune would free. Gating the prune on
	// that column skipped the one act that would have closed the gap, and
	// refused a release over space the guard was itself holding.
	//
	// An unbounded prune is not what this risks: --min-free-space is exactly
	// the bound that makes "prune, in case there is something" safe, leaving it
	// a genuine no-op when the cache is already empty. The daemon knows what it
	// holds; a size column that cannot see the bytes does not.
	ctx.Trace(fmt.Sprintf("%s: docker root free disk is below the %s floor; pruning reclaimable build cache down to it", policy.label, formatGiB(floor)))
	ctx.TraceCommand("", "docker", diskHeadroomPruneArgs(floor)...)
	if err := prune(floor, policy.limits.prune); err != nil {
		// The prune is not what the run depends on, so a failure here is not
		// fatal — but it must not read as an act that happened. A prune that
		// never ran (an unrecognized flag, a daemon that did not answer in
		// time) leaves the shortfall exactly where it was, and the verdict
		// below is measured against a disk this line did not move.
		ctx.Trace(fmt.Sprintf("%s: the build-cache prune did not complete (%s), so the cache it would have reclaimed is still on disk; measuring the shortfall against what is actually free now", policy.label, err))
	}

	measured, readErr = readFree(policy.limits)
	if readErr != nil {
		ctx.Trace(fmt.Sprintf("%s: the docker root's free space could not be re-read after the prune (%s); the shortfall is unmeasured and the run continues unrefused", policy.label, readErr))
		return nil
	}
	if measured.free >= floor {
		return nil
	}

	// Still short with the prune already spent: only now is the cache asked
	// what it is still holding, because that answer decides the remedy and
	// nothing else can supply it. It costs a walk of the cache, so it is taken
	// only on the path that is about to tell an operator what to do.
	cacheReclaimable, cacheErr := readCacheReclaimable(policy.limits.prune)
	if cacheErr != nil {
		ctx.Trace(fmt.Sprintf("%s: what the build cache could still reclaim is not observable from this process (%s); the refusal below reports the shortfall without it", policy.label, cacheErr))
	}
	return diskHeadroomVerdict(ctx, policy, newDiskHeadroomShortfall(measured, floor, reclaimable, known, cacheReclaimable))
}

// diskHeadroomShortfall is one failing headroom check's measured state: the
// numbers the gate compared, the filesystem they were read on, and what docker
// was measured to still be able to free there. The remedy is derived from
// these, never applied by default; see remedy.
type diskHeadroomShortfall struct {
	free        uint64
	floor       uint64
	path        string
	sharedPaths []string
	// dockerReclaimExhausted is true only when docker's own stores were
	// measured to have nothing left to free on this filesystem. It is false
	// when that reading failed, because an unreadable figure is not evidence
	// that the remedy is inert.
	dockerReclaimExhausted bool
	// buildCacheStillReclaimable is what BuildKit's own build cache reported it
	// could still free once the prune had already been spent, or 0 when that
	// reading reported nothing or could not be taken. A non-zero figure is the
	// one piece of evidence that says where the shortfall actually sits, so it
	// decides the remedy; see remedy.
	buildCacheStillReclaimable uint64
}

func newDiskHeadroomShortfall(measured diskHeadroomMeasurement, floor uint64, reclaimable uint64, known bool, buildCacheStillReclaimable uint64) diskHeadroomShortfall {
	return diskHeadroomShortfall{
		free:                       measured.free,
		floor:                      floor,
		path:                       measured.path,
		sharedPaths:                measured.sharedPaths,
		dockerReclaimExhausted:     known && reclaimable == 0,
		buildCacheStillReclaimable: buildCacheStillReclaimable,
	}
}

// remedy names the remediation that matches the space that was measured.
//
// A shortfall the build cache is itself still holding is named as that and
// nothing else. `docker system df` attributes a build's layers to Images for as
// long as the images holding them exist, so on a store full of previously-built
// images it advertises tens of gigabytes of reclaimable images that removing
// those images does not free — measured as 74 unreferenced erun images deleted
// with not one byte off /var/lib/docker/overlay2, the whole of it reappearing
// under Build Cache and none of it reachable by `docker rmi`. Sending an
// operator down that route costs them a real attempt and teaches them the
// diagnosis is unreliable, so where BuildKit's own cache reports it can still
// free space, that is what the message names.
//
// A shortfall docker's own stores can still reach is docker's to close, and
// the message says so. A shortfall on a filesystem whose docker stores were
// measured empty is not: naming a prune there sends the operator through a
// remedy that cannot move the number by a byte — costing them a real attempt
// and teaching them the diagnosis is unreliable — when what the gate actually
// measured is space held outside docker on that same filesystem. That case
// says the docker remedy is spent and names the space it measured, rather than
// leaving the operator to rediscover the real cause.
func (s diskHeadroomShortfall) remedy() string {
	if s.buildCacheStillReclaimable > 0 {
		return fmt.Sprintf("docker's build cache still holds %s a prune can reclaim, so what is short is the "+
			"cache rather than docker's own images: reclaim it with docker buildx prune -a, or grow the volume",
			formatGiB(s.buildCacheStillReclaimable))
	}
	if !s.dockerReclaimExhausted {
		return "free up space (docker system prune, remove unused images) or grow the volume"
	}
	holders := ""
	if len(s.sharedPaths) > 0 {
		holders = fmt.Sprintf(" (the environment's own space on it is %s)", strings.Join(s.sharedPaths, ", "))
	}
	return "docker's own stores have nothing left to reclaim there, so pruning docker cannot free any of it; " +
		"the space this gate measured is held outside docker" + holders +
		", so free it on that filesystem or grow the volume"
}

// diskHeadroomVerdict is what the caller does once no further reclaim is going
// to happen: a release refuses, a build warns and proceeds. Shared so the
// declined-prune path and the pruned-anyway path cannot drift apart.
func diskHeadroomVerdict(ctx Context, policy diskHeadroomPolicy, shortfall diskHeadroomShortfall) error {
	if shortfall.free >= shortfall.floor {
		return nil
	}
	if !policy.refuse {
		ctx.Info(fmt.Sprintf("warning: only %s free at %s, below the %s floor; "+
			"this node is close to the disk pressure that evicts pods — %s",
			formatGiB(shortfall.free), shortfall.path, formatGiB(shortfall.floor), shortfall.remedy()))
		return nil
	}
	return fmt.Errorf("only %s free at %s, below the %s a multi-arch release build needs: "+
		"%s before retrying — filling this disk is what evicts the pod running the release",
		formatGiB(shortfall.free), shortfall.path, formatGiB(shortfall.floor), shortfall.remedy())
}

// dockerReclaimableBytes reads what a prune of docker's own stores could still
// free. Local volumes are excluded: `docker system prune` and unused-image
// removal do not reclaim them, so counting them would let the message name a
// docker remedy for space docker cannot actually reach — the same defect, in
// the other direction.
//
// Treat the returned figure as a lower bound only, never an estimate of the
// true yield: measured on a real node, `docker system df`'s reclaimable figure
// for build cache undercounted what `docker builder prune -a` actually freed
// by 4.4x (22.57GB reported, 98.27GB freed). The exact accounting gap behind
// that understatement is not confirmed, so callers must not assume a
// particular cause — only that the number can be short.
//
// It is not a reading of what a build-cache prune can reach, and must not drive
// that decision: see dockerBuildCacheReclaimable for why the Build Cache column
// of this same output cannot answer it.
func dockerReclaimableBytes(limit time.Duration) (uint64, error) {
	out, err := diskHeadroomOutput(limit, "docker", "system", "df", "--format", "{{.Type}}|{{.Reclaimable}}")
	if err != nil {
		return 0, diskHeadroomReadFailure(limit, "docker system df", err)
	}
	var total uint64
	recognized := false
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name, value, found := strings.Cut(strings.TrimSpace(line), "|")
		if !found || strings.EqualFold(strings.TrimSpace(name), "local volumes") {
			continue
		}
		bytes, ok := parseDockerSize(value)
		if !ok {
			continue
		}
		recognized = true
		total += bytes
	}
	if !recognized {
		return 0, errors.New("docker system df reported no recognisable size")
	}
	return total, nil
}

// dockerBuildCacheReclaimable reads how much BuildKit's own build cache could
// still free, from `docker buildx du`.
//
// This, not `docker system df`, is the reading that can answer "is there build
// cache to reclaim", because docker's own accounting cannot: a layer a build
// produced is attributed to Images for as long as the image holding it exists,
// and becomes Build Cache only once that image is removed. A store full of
// previously-built images therefore reports Build Cache 0B while the cache
// holds tens of gigabytes a prune would free — measured as 0B reported against
// 43.37GB of reclaimable cache, the whole of it surfacing under Build Cache
// only after 74 unreferenced images were removed, freeing none of it.
//
// Bounded like every other subprocess here, but on the prune's bound rather
// than the short one the plain CLI reads get: it walks the same BuildKit cache
// the prune walks, which is not a milliseconds-long read on a node this check
// exists for.
func dockerBuildCacheReclaimable(limit time.Duration) (uint64, error) {
	out, err := diskHeadroomOutput(limit, "docker", "buildx", "du")
	if err != nil {
		return 0, diskHeadroomReadFailure(limit, "docker buildx du", err)
	}
	bytes, ok := parseBuildxDuReclaimable(string(out))
	if !ok {
		return 0, errors.New("docker buildx du reported no reclaimable total")
	}
	return bytes, nil
}

// parseBuildxDuReclaimable reads the Reclaimable figure out of `docker buildx
// du`'s summary block, which closes the per-record listing:
//
//	Shared:		24.17GB
//	Private:	19.2GB
//	Reclaimable:	43.37GB
//	Total:		43.37GB
//
// The record rows above it carry no colon-separated key, and the timestamps
// some of them end with do, so the summary is located by name rather than by
// position or by format.
func parseBuildxDuReclaimable(output string) (uint64, bool) {
	for _, line := range strings.Split(output, "\n") {
		name, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found || !strings.EqualFold(strings.TrimSpace(name), "reclaimable") {
			continue
		}
		return parseDockerSize(value)
	}
	return 0, false
}

// dockerSizeUnits are the suffixes docker renders sizes with, longest first so
// "kB" is never matched as "B". Decimal, matching docker's own HumanSize.
var dockerSizeUnits = []struct {
	suffix string
	scale  float64
}{
	{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"KB", 1e3}, {"B", 1},
}

// parseDockerSize reads one docker-rendered size ("8.914GB", "0B"), ignoring
// any trailing percentage docker appends to some rows ("77.29GB (100%)").
func parseDockerSize(value string) (uint64, bool) {
	value = strings.TrimSpace(value)
	if idx := strings.Index(value, " "); idx >= 0 {
		value = value[:idx]
	}
	for _, unit := range dockerSizeUnits {
		if !strings.HasSuffix(value, unit.suffix) {
			continue
		}
		number, err := strconv.ParseFloat(strings.TrimSuffix(value, unit.suffix), 64)
		if err != nil || number < 0 {
			return 0, false
		}
		return uint64(number * unit.scale), true
	}
	return 0, false
}

// diskHeadroomPruneArgs is the prune both the trace and the real invocation
// issue, built once so the command a run records cannot drift from the one it
// actually runs.
//
// It is `docker buildx prune`, not `docker builder prune`, and that choice is
// load-bearing rather than incidental: the classic command — still the one the
// docker CLI this repository pins implements — accepts only --all, --filter,
// --force and --keep-storage, so a --min-free-space bound aimed at it is
// rejected as an unknown flag. The prune then reclaims nothing while the trace
// above it advertises a reclaim, which is worse than not pruning: the shortfall
// is discovered afterward against a disk nothing moved, and the headroom check
// silently stops being the protective act it exists to be. --min-free-space is
// also what expresses this check's actual invariant — prune until free space
// reaches the floor — rather than capping how much cache is retained and
// leaving the free space it was meant to recover unaddressed.
func diskHeadroomPruneArgs(floor uint64) []string {
	return []string{"buildx", "prune", "-f", "--min-free-space", strconv.FormatUint(floor, 10)}
}

// runDiskHeadroomPrune is diskHeadroomPruneFunc's real implementation. See
// diskHeadroomPruneArgs for the command and flag it is built from.
//
// It is bounded because the daemon it drives is the thing this check exists
// for: the disk-floor case is precisely the one where a daemon can be too busy
// — or too wedged — to answer at all, and an unbounded prune there is a build
// that never returns instead of a build that proceeds on a full disk.
func runDiskHeadroomPrune(floor uint64, limit time.Duration) error {
	err := diskHeadroomRun(limit, "docker", diskHeadroomPruneArgs(floor)...)
	return diskHeadroomReadFailure(limit, "docker buildx prune", err)
}

// diskHeadroomOutput runs one headroom read under limit and returns its
// stdout, and diskHeadroomRun does the same for a command whose output is not
// read. Both go through CommandContext with a deadline rather than Command,
// whose WaitDelay bounds only the post-exit pipe drain and would leave a
// daemon that never answers holding the process open forever.
func diskHeadroomOutput(limit time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	out, err := CommandContext(ctx, name, args...).Output()
	return out, diskHeadroomDeadlineErr(ctx, err)
}

func diskHeadroomRun(limit time.Duration, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	return diskHeadroomDeadlineErr(ctx, CommandContext(ctx, name, args...).Run())
}

// diskHeadroomDeadlineErr reports a command killed by its own deadline as that
// deadline, rather than as the bare "signal: killed" the kill produces. The
// distinction is the whole diagnosis: a daemon that stopped answering within
// its bound reads very differently from one that exited with an error, and a
// caller cannot recover it from the signal alone once the context is gone.
func diskHeadroomDeadlineErr(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

// diskHeadroomReadFailure turns a subprocess failure into the state it
// actually represents. A deadline that elapsed is not the same failure as a
// command that ran and exited nonzero — the first says the daemon stopped
// answering, the second says it answered with an error — and only a message
// that tells them apart lets an operator diagnose a node whose daemon is
// wedged from one where docker is simply missing.
func diskHeadroomReadFailure(limit time.Duration, what string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s did not answer within %s", what, limit)
	}
	// An executable that is not on PATH is reported as that, not by splicing
	// the runtime's own "exec: ...: executable file not found" string. The
	// callers' traces are read as evidence of what a run reached for, and that
	// string is indistinguishable there from a run silently depending on a
	// binary it never declared — a different fault from a daemon that is
	// present but unhealthy.
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%s could not run: the executable is not on PATH", what)
	}
	return fmt.Errorf("%s failed: %w", what, err)
}

// resolveMinDiskHeadroomBytes takes the larger of the absolute floor and the
// proportional one, so a big disk gets a floor that clears kubelet's
// percentage-based eviction threshold and a small one still reserves enough
// bytes for a release. An explicit env override replaces both outright.
func resolveMinDiskHeadroomBytes(total uint64) uint64 {
	if raw := strings.TrimSpace(os.Getenv(releaseMinDiskHeadroomEnv)); raw != "" {
		if value, err := strconv.ParseUint(raw, 10, 64); err == nil {
			return value
		}
	}
	floor := releaseMinDiskHeadroomBytes
	// Divide before multiplying: total is a byte count of a whole filesystem
	// and total*percent would overflow on a large enough disk.
	if proportional := total / 100 * minDiskHeadroomPercent; proportional > floor {
		floor = proportional
	}
	return floor
}

// diskHeadroomProbeImage is a tiny, pinned image with a `df` binary, used
// only to read the docker daemon's own filesystem from the inside when this
// process cannot see the daemon's root directory itself (see
// dockerRootDiskBytesViaProbe).
const diskHeadroomProbeImage = "busybox:1.36.1"

// dockerRootDiskBytes asks the docker daemon where its root directory is,
// then reads that path's free and total space — a real filesystem read, not a
// guess from image/cache sizes docker itself reports, since none of those add
// up to "how much room is actually left on this node". Windows has no `df`.
//
// The docker daemon a build runs against often lives in a separate container
// (the erun-dind sidecar) with its own filesystem, so the root this process
// just resolved is frequently not a path it can stat directly. That case falls
// back to asking the daemon itself: it can always reach its own filesystem, so
// running a throwaway container with that root bind-mounted turns "not visible
// from here" into a real read instead of a reason to give up. Only when both
// routes fail is ok false.
func dockerRootDiskBytes(limits diskHeadroomTimeouts) (diskHeadroomMeasurement, error) {
	if runtime.GOOS == "windows" {
		return diskHeadroomMeasurement{}, errors.New("windows has no df")
	}
	rootOut, err := diskHeadroomOutput(limits.read, "docker", "info", "-f", "{{.DockerRootDir}}")
	if err != nil {
		return diskHeadroomMeasurement{}, diskHeadroomReadFailure(limits.read, "docker info", err)
	}
	root := strings.TrimSpace(string(rootOut))
	if root == "" {
		return diskHeadroomMeasurement{}, errors.New("docker reported no root directory")
	}
	if _, statErr := os.Stat(root); statErr == nil {
		dfOut, err := diskHeadroomOutput(limits.read, "df", "-Pk", root)
		if err != nil {
			return diskHeadroomMeasurement{}, diskHeadroomReadFailure(limits.read, "df", err)
		}
		free, total, ok := parseDFDiskBytes(string(dfOut))
		if !ok {
			return diskHeadroomMeasurement{}, errors.New("df output at the docker root was not parseable")
		}
		return diskHeadroomMeasurement{
			free:        free,
			total:       total,
			path:        root,
			sharedPaths: environmentPathsSharingFilesystem(limits, root),
		}, nil
	}
	return dockerRootDiskBytesViaProbe(limits, root)
}

// dockerRootDiskBytesViaProbe reads free space at root as the docker
// daemon itself sees it, by asking the daemon to run a throwaway container
// with root bind-mounted read-only and df'd from inside. This is what makes
// the read conclusive when the daemon lives in a different filesystem
// namespace than this process (the erun-dind sidecar case): the daemon can
// always reach its own root, even when this process cannot.
//
// No co-located space is reported on this route, and that is deliberate: it
// exists precisely because root is not a path this process can read, so it
// cannot measure what else shares that filesystem either.
func dockerRootDiskBytesViaProbe(limits diskHeadroomTimeouts, root string) (diskHeadroomMeasurement, error) {
	// The probe gets its own, longer bound: unlike the reads above it may have
	// to pull its pinned image first, and a slow pull is not a daemon that
	// stopped answering.
	out, err := diskHeadroomOutput(limits.probe, "docker", "run", "--rm", "-v", root+":/host:ro", diskHeadroomProbeImage, "df", "-Pk", "/host")
	if err != nil {
		return diskHeadroomMeasurement{}, diskHeadroomReadFailure(limits.probe, "the disk headroom probe container", err)
	}
	free, total, ok := parseDFDiskBytes(string(out))
	if !ok {
		return diskHeadroomMeasurement{}, errors.New("the disk headroom probe container returned unparseable df output")
	}
	return diskHeadroomMeasurement{free: free, total: total, path: root}, nil
}

// environmentPathsSharingFilesystem reports which of the environment's own
// space holders sit on the same filesystem as measured — the answer to "where
// did the space go?" that a refusal needs when the space is not docker's. It
// is read per path rather than assumed: on a hosted node the user's work
// checkouts and caches share the volume with docker, and on a laptop they
// routinely do not, so naming them there would be the reported defect in the
// other direction. A path that does not exist, or whose filesystem cannot be
// determined, is left out rather than guessed at.
func environmentPathsSharingFilesystem(limits diskHeadroomTimeouts, measured string) []string {
	mount, ok := dfMountPoint(limits.read, measured)
	if !ok {
		return nil
	}
	var candidates []string
	if root, err := workCloneRoot(""); err == nil {
		candidates = append(candidates, root)
	}
	if home, err := workCloneUserHomeDir(); err == nil {
		if resolved := strings.TrimSpace(home); resolved != "" {
			candidates = append(candidates, filepath.Join(resolved, ".cache"))
		}
	}
	var shared []string
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		if candidateMount, ok := dfMountPoint(limits.read, candidate); ok && candidateMount == mount {
			shared = append(shared, candidate)
		}
	}
	return shared
}

// dfMountPoint reports the filesystem mount point path resolves to, as df
// names it. The mount point is compared rather than the filesystem name: it is
// the last column and stays last even when a long identifier wraps the data
// row onto its own line, the shape parseDFDiskBytes already has to tolerate.
func dfMountPoint(limit time.Duration, path string) (string, bool) {
	out, err := diskHeadroomOutput(limit, "df", "-Pk", path)
	if err != nil {
		return "", false
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) < 2 {
		return "", false
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 2 {
		return "", false
	}
	return fields[len(fields)-1], true
}

// parseDFDiskBytes reads the "Available" and "1024-blocks" columns (in
// 1024-byte blocks, guaranteed by -Pk) from the last line of `df`'s
// POSIX-format output — the data row, whether or not the filesystem name
// pushed it onto its own line. A long filesystem identifier wrapping the name
// onto its own line shifts every column left, so the columns are located
// relative to the Capacity percentage rather than by absolute index.
func parseDFDiskBytes(output string) (free, total uint64, ok bool) {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) < 2 {
		return 0, 0, false
	}
	fields := strings.Fields(lines[len(lines)-1])
	for i, field := range fields {
		if i == 0 || !strings.HasSuffix(field, "%") {
			continue
		}
		if _, err := strconv.Atoi(strings.TrimSuffix(field, "%")); err != nil {
			continue
		}
		availableKB, err := strconv.ParseUint(fields[i-1], 10, 64)
		if err != nil {
			return 0, 0, false
		}
		// blocks, used, available, capacity% — so the total sits three fields
		// left of the percentage in both the wrapped and unwrapped shapes.
		// A row too narrow to hold all four still yields a usable available
		// figure, and total stays 0 so the floor falls back to the absolute one.
		var totalBytes uint64
		if i >= 3 {
			if totalKB, parseErr := strconv.ParseUint(fields[i-3], 10, 64); parseErr == nil {
				totalBytes = totalKB * 1024
			}
		}
		return availableKB * 1024, totalBytes, true
	}
	return 0, 0, false
}

func formatGiB(bytes uint64) string {
	return fmt.Sprintf("%.1f GiB", float64(bytes)/(1<<30))
}
