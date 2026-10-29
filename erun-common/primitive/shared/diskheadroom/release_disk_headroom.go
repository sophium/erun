package diskheadroom

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

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared/gitref"
)

// Disk preflight for releases and builds: a multi-arch release once filled a node and was
// evicted by its own DiskPressure, and plain builds grow BuildKit cache between releases.
// The floor sits above kubelet's 5% evictionHard, at its evictionMinimumReclaim level,
// with an absolute byte floor for small disks.

// ReleaseMinDiskHeadroomEnv overrides the resolved floor outright.
const ReleaseMinDiskHeadroomEnv = "ERUN_RELEASE_MIN_DISK_HEADROOM_BYTES"

// ReleaseMinDiskHeadroomBytes is the absolute lower bound on the floor.
const ReleaseMinDiskHeadroomBytes uint64 = 20 << 30 // 20 GiB

// MinDiskHeadroomPercent matches kubelet's default nodefs evictionMinimumReclaim.
const MinDiskHeadroomPercent uint64 = 10

// Every docker subprocess is bounded: a wedged daemon must not leave the build with no
// terminal record. Probes may pull an image; prunes can take tens of seconds.
const (
	diskHeadroomReadTimeout  = 15 * time.Second
	diskHeadroomProbeTimeout = 60 * time.Second
	diskHeadroomPruneTimeout = 5 * time.Minute
)

// DiskHeadroomTimeouts are carried on the policy so a test can shorten them.
type DiskHeadroomTimeouts struct {
	Read  time.Duration
	Probe time.Duration
	Prune time.Duration
}

// DiskHeadroomDefaultTimeouts are the production bounds.
var DiskHeadroomDefaultTimeouts = DiskHeadroomTimeouts{
	Read:  diskHeadroomReadTimeout,
	Probe: diskHeadroomProbeTimeout,
	Prune: diskHeadroomPruneTimeout,
}

// DiskHeadroomMeasurement is one free/total read and the filesystem it came from, so the
// remedy matches the space actually measured.
type DiskHeadroomMeasurement struct {
	Free  uint64
	Total uint64
	Path  string
	// SharedPaths are environment space holders measured on the same filesystem; empty when unknown.
	SharedPaths []string
}

// diskHeadroomFreeSpaceFunc reads the docker root's free and total space. Total is 0 when
// unknown, falling back to the absolute floor. The error says why the read failed.
type diskHeadroomFreeSpaceFunc func(limits DiskHeadroomTimeouts) (DiskHeadroomMeasurement, error)

// diskHeadroomReclaimableFunc is a lower bound, trusted only to say "nothing at all".
// An error means docker may still hold something.
type diskHeadroomReclaimableFunc func(limit time.Duration) (uint64, error)

// diskHeadroomCacheReclaimableFunc reads BuildKit's own cache: `docker system df` attributes
// built layers to Images and reads 0B Build Cache on exactly this store.
type diskHeadroomCacheReclaimableFunc func(limit time.Duration) (uint64, error)

// diskHeadroomPruneFunc prunes build cache to leave at least floor bytes free.
type diskHeadroomPruneFunc func(floor uint64, limit time.Duration) error

// DiskHeadroomPolicy differs per caller: a release refuses when still below the floor,
// a build only warns.
type DiskHeadroomPolicy struct {
	Label  string
	Refuse bool
	Limits DiskHeadroomTimeouts
}

var ReleaseDiskHeadroomPolicy = DiskHeadroomPolicy{Label: "release", Refuse: true, Limits: DiskHeadroomDefaultTimeouts}

// EnsureReleaseDiskHeadroom prunes when below the floor and refuses if still short. It runs
// before the release stage so a refusal leaves no stamp commit or tag; the base-branch check
// cannot move earlier and still can.
func EnsureReleaseDiskHeadroom(ctx core.Context) error {
	// The cache ceiling holds regardless of free space, and frees room before the floor is read.
	EnsureBuildCacheRetention(ctx, ReleaseDiskHeadroomPolicy)
	return EnsureDiskHeadroomWith(ctx, ReleaseDiskHeadroomPolicy, DockerRootDiskBytes, DockerReclaimableBytes, DockerBuildCacheReclaimable, RunDiskHeadroomPrune)
}

// EnsureDiskHeadroomWith reads, prunes when below the floor, and re-checks before refusing.
// An inconclusive read proceeds with no prune.
func EnsureDiskHeadroomWith(ctx core.Context, policy DiskHeadroomPolicy, readFree diskHeadroomFreeSpaceFunc, readReclaimable diskHeadroomReclaimableFunc, readCacheReclaimable diskHeadroomCacheReclaimableFunc, prune diskHeadroomPruneFunc) error {
	ctx.TraceCommand("", "docker", "info", "-f", "{{.DockerRootDir}}")
	if ctx.DryRun {
		return nil
	}

	measured, readErr := readFree(policy.Limits)
	if readErr != nil {
		ctx.Trace(fmt.Sprintf("%s: docker root free disk space is not observable from this process (%s); skipping the headroom check", policy.Label, readErr))
		return nil
	}
	floor := ResolveMinDiskHeadroomBytes(measured.Total)
	ctx.Trace(fmt.Sprintf("%s: docker root has %s free of %s (floor %s)", policy.Label, FormatGiB(measured.Free), FormatGiB(measured.Total), FormatGiB(floor)))
	if measured.Free >= floor {
		return nil
	}

	// These figures only detect "nothing to reclaim"; docker understates what a prune frees.
	reclaimable, reclaimErr := readReclaimable(policy.Limits.Read)
	known := reclaimErr == nil

	// Always prune below the floor: `docker system df` reads 0B Build Cache while built images
	// hold it. --min-free-space makes this a no-op when there is nothing to free.
	ctx.Trace(fmt.Sprintf("%s: docker root free disk is below the %s floor; pruning reclaimable build cache down to it", policy.Label, FormatGiB(floor)))
	ctx.TraceCommand("", "docker", diskHeadroomPruneArgs(floor)...)
	if err := prune(floor, policy.Limits.Prune); err != nil {
		// Not fatal, but must not read as a prune that happened.
		ctx.Trace(fmt.Sprintf("%s: the build-cache prune did not complete (%s), so the cache it would have reclaimed is still on disk; measuring the shortfall against what is actually free now", policy.Label, err))
	}

	measured, readErr = readFree(policy.Limits)
	if readErr != nil {
		ctx.Trace(fmt.Sprintf("%s: the docker root's free space could not be re-read after the prune (%s); the shortfall is unmeasured and the run continues unrefused", policy.Label, readErr))
		return nil
	}
	if measured.Free >= floor {
		return nil
	}

	// Asked only once the prune is spent, since it costs a cache walk and decides the remedy.
	cacheReclaimable, cacheErr := readCacheReclaimable(policy.Limits.Prune)
	if cacheErr != nil {
		ctx.Trace(fmt.Sprintf("%s: what the build cache could still reclaim is not observable from this process (%s); the refusal below reports the shortfall without it", policy.Label, cacheErr))
	}
	return diskHeadroomVerdict(ctx, policy, newDiskHeadroomShortfall(measured, floor, reclaimable, known, cacheReclaimable))
}

// DiskHeadroomShortfall is one failing check's measured state; the remedy derives from it.
type DiskHeadroomShortfall struct {
	Free        uint64
	Floor       uint64
	Path        string
	SharedPaths []string
	// DockerReclaimExhausted is false when the reading failed: unknown is not inert.
	DockerReclaimExhausted bool
	// BuildCacheStillReclaimable is BuildKit's post-prune reclaimable figure, or 0; non-zero decides the remedy.
	BuildCacheStillReclaimable uint64
}

func newDiskHeadroomShortfall(measured DiskHeadroomMeasurement, floor uint64, reclaimable uint64, known bool, buildCacheStillReclaimable uint64) DiskHeadroomShortfall {
	return DiskHeadroomShortfall{
		Free:                       measured.Free,
		Floor:                      floor,
		Path:                       measured.Path,
		SharedPaths:                measured.SharedPaths,
		DockerReclaimExhausted:     known && reclaimable == 0,
		BuildCacheStillReclaimable: buildCacheStillReclaimable,
	}
}

// Remedy names the remediation matching the measured space: remaining BuildKit cache first
// (removing images does not free it), then docker's own stores, else space held outside docker.
func (s DiskHeadroomShortfall) Remedy() string {
	if s.BuildCacheStillReclaimable > 0 {
		return fmt.Sprintf("docker's build cache still holds %s a prune can reclaim, so what is short is the "+
			"cache rather than docker's own images: reclaim it with docker buildx prune -a, or grow the volume",
			FormatGiB(s.BuildCacheStillReclaimable))
	}
	if !s.DockerReclaimExhausted {
		return "free up space (docker system prune, remove unused images) or grow the volume"
	}
	holders := ""
	if len(s.SharedPaths) > 0 {
		holders = fmt.Sprintf(" (the environment's own space on it is %s)", strings.Join(s.SharedPaths, ", "))
	}
	return "docker's own stores have nothing left to reclaim there, so pruning docker cannot free any of it; " +
		"the space this gate measured is held outside docker" + holders +
		", so free it on that filesystem or grow the volume"
}

// diskHeadroomVerdict is shared so the declined-prune and pruned paths cannot drift.
func diskHeadroomVerdict(ctx core.Context, policy DiskHeadroomPolicy, shortfall DiskHeadroomShortfall) error {
	if shortfall.Free >= shortfall.Floor {
		return nil
	}
	if !policy.Refuse {
		ctx.Info(fmt.Sprintf("warning: only %s free at %s, below the %s floor; "+
			"this node is close to the disk pressure that evicts pods — %s",
			FormatGiB(shortfall.Free), shortfall.Path, FormatGiB(shortfall.Floor), shortfall.Remedy()))
		return nil
	}
	return fmt.Errorf("only %s free at %s, below the %s a multi-arch release build needs: "+
		"%s before retrying — filling this disk is what evicts the pod running the release",
		FormatGiB(shortfall.Free), shortfall.Path, FormatGiB(shortfall.Floor), shortfall.Remedy())
}

// DockerReclaimableBytes reads what pruning docker's stores could free, excluding local volumes.
// A lower bound only; never use it to decide on a cache prune.
func DockerReclaimableBytes(limit time.Duration) (uint64, error) {
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
		bytes, ok := ParseDockerSize(value)
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

// DockerBuildCacheReclaimable reads BuildKit's reclaimable cache from `docker buildx du`.
// It uses the prune's bound since it walks the same cache.
func DockerBuildCacheReclaimable(limit time.Duration) (uint64, error) {
	out, err := diskHeadroomOutput(limit, "docker", "buildx", "du")
	if err != nil {
		return 0, diskHeadroomReadFailure(limit, "docker buildx du", err)
	}
	bytes, ok := ParseBuildxDuReclaimable(string(out))
	if !ok {
		return 0, errors.New("docker buildx du reported no reclaimable total")
	}
	return bytes, nil
}

// ParseBuildxDuReclaimable reads Reclaimable from `docker buildx du`'s summary block, by name,
// since record rows may contain colon timestamps.
func ParseBuildxDuReclaimable(output string) (uint64, bool) {
	for _, line := range strings.Split(output, "\n") {
		name, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found || !strings.EqualFold(strings.TrimSpace(name), "reclaimable") {
			continue
		}
		return ParseDockerSize(value)
	}
	return 0, false
}

// dockerSizeUnits are longest first so "kB" is never matched as "B". Decimal, like docker's HumanSize.
var dockerSizeUnits = []struct {
	suffix string
	scale  float64
}{
	{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"KB", 1e3}, {"B", 1},
}

// ParseDockerSize reads one docker size ("8.914GB"), ignoring a trailing "(100%)".
func ParseDockerSize(value string) (uint64, bool) {
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

// diskHeadroomPruneArgs is shared by the trace and the invocation. It must be `docker buildx
// prune`: the pinned classic `builder prune` rejects --min-free-space. It only touches build cache.
func diskHeadroomPruneArgs(floor uint64) []string {
	return []string{"buildx", "prune", "-f", "--min-free-space", strconv.FormatUint(floor, 10)}
}

// RunDiskHeadroomPrune is bounded because a full disk is when the daemon is most likely wedged.
func RunDiskHeadroomPrune(floor uint64, limit time.Duration) error {
	err := diskHeadroomRun(limit, "docker", diskHeadroomPruneArgs(floor)...)
	return diskHeadroomReadFailure(limit, "docker buildx prune", err)
}

// CommandContext, not Command: WaitDelay only bounds the post-exit pipe drain.
func diskHeadroomOutput(limit time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	out, err := core.CommandContext(ctx, name, args...).Output()
	return out, diskHeadroomDeadlineErr(ctx, err)
}

func diskHeadroomRun(limit time.Duration, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	return diskHeadroomDeadlineErr(ctx, core.CommandContext(ctx, name, args...).Run())
}

// diskHeadroomDeadlineErr reports a deadline kill as the deadline, not "signal: killed".
func diskHeadroomDeadlineErr(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

// diskHeadroomReadFailure distinguishes a daemon that stopped answering from one that errored.
func diskHeadroomReadFailure(limit time.Duration, what string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s did not answer within %s", what, limit)
	}
	// A missing executable is reported plainly, not with the runtime's raw string.
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%s could not run: the executable is not on PATH", what)
	}
	return fmt.Errorf("%s failed: %w", what, err)
}

// ResolveMinDiskHeadroomBytes takes the larger of the absolute and proportional floors;
// the env override replaces both.
func ResolveMinDiskHeadroomBytes(total uint64) uint64 {
	if raw := strings.TrimSpace(os.Getenv(ReleaseMinDiskHeadroomEnv)); raw != "" {
		if value, err := strconv.ParseUint(raw, 10, 64); err == nil {
			return value
		}
	}
	floor := ReleaseMinDiskHeadroomBytes
	// Divide first: total*percent can overflow.
	if proportional := total / 100 * MinDiskHeadroomPercent; proportional > floor {
		floor = proportional
	}
	return floor
}

// diskHeadroomProbeImage is a pinned image with df, for reading the daemon's root from inside.
const diskHeadroomProbeImage = "busybox:1.36.1"

// DockerRootDiskBytes reads free/total space at the daemon's root dir. When this process
// cannot see it (erun-dind sidecar), it asks the daemon via a probe container. Windows has no df.
func DockerRootDiskBytes(limits DiskHeadroomTimeouts) (DiskHeadroomMeasurement, error) {
	if runtime.GOOS == "windows" {
		return DiskHeadroomMeasurement{}, errors.New("windows has no df")
	}
	rootOut, err := diskHeadroomOutput(limits.Read, "docker", "info", "-f", "{{.DockerRootDir}}")
	if err != nil {
		return DiskHeadroomMeasurement{}, diskHeadroomReadFailure(limits.Read, "docker info", err)
	}
	root := strings.TrimSpace(string(rootOut))
	if root == "" {
		return DiskHeadroomMeasurement{}, errors.New("docker reported no root directory")
	}
	if _, statErr := os.Stat(root); statErr == nil {
		dfOut, err := diskHeadroomOutput(limits.Read, "df", "-Pk", root)
		if err != nil {
			return DiskHeadroomMeasurement{}, diskHeadroomReadFailure(limits.Read, "df", err)
		}
		free, total, ok := ParseDFDiskBytes(string(dfOut))
		if !ok {
			return DiskHeadroomMeasurement{}, errors.New("df output at the docker root was not parseable")
		}
		return DiskHeadroomMeasurement{
			Free:        free,
			Total:       total,
			Path:        root,
			SharedPaths: environmentPathsSharingFilesystem(limits, root),
		}, nil
	}
	return dockerRootDiskBytesViaProbe(limits, root)
}

// dockerRootDiskBytesViaProbe df's root inside a throwaway container. It reports no
// co-located space, since root is not readable here.
func dockerRootDiskBytesViaProbe(limits DiskHeadroomTimeouts, root string) (DiskHeadroomMeasurement, error) {
	// Longer bound: the probe may pull its image first.
	out, err := diskHeadroomOutput(limits.Probe, "docker", "run", "--rm", "-v", root+":/host:ro", diskHeadroomProbeImage, "df", "-Pk", "/host")
	if err != nil {
		return DiskHeadroomMeasurement{}, diskHeadroomReadFailure(limits.Probe, "the disk headroom probe container", err)
	}
	free, total, ok := ParseDFDiskBytes(string(out))
	if !ok {
		return DiskHeadroomMeasurement{}, errors.New("the disk headroom probe container returned unparseable df output")
	}
	return DiskHeadroomMeasurement{Free: free, Total: total, Path: root}, nil
}

// environmentPathsSharingFilesystem reports which environment space holders share measured's
// filesystem, read per path; unknowns are left out.
func environmentPathsSharingFilesystem(limits DiskHeadroomTimeouts, measured string) []string {
	mount, ok := dfMountPoint(limits.Read, measured)
	if !ok {
		return nil
	}
	var candidates []string
	if root, err := gitref.WorkCloneRoot(""); err == nil {
		candidates = append(candidates, root)
	}
	if home, err := gitref.WorkCloneUserHomeDir(); err == nil {
		if resolved := strings.TrimSpace(home); resolved != "" {
			candidates = append(candidates, filepath.Join(resolved, ".cache"))
		}
	}
	var shared []string
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		if candidateMount, ok := dfMountPoint(limits.Read, candidate); ok && candidateMount == mount {
			shared = append(shared, candidate)
		}
	}
	return shared
}

// dfMountPoint compares mount points (df's last column), which survive a wrapped row.
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

// ParseDFDiskBytes reads Available and 1024-blocks from `df -Pk`'s last row, locating columns
// relative to Capacity% since a long filesystem name can wrap.
func ParseDFDiskBytes(output string) (free, total uint64, ok bool) {
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
		// Total is three fields left of Capacity%; too narrow a row leaves it 0 (absolute floor).
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

func FormatGiB(bytes uint64) string {
	return fmt.Sprintf("%.1f GiB", float64(bytes)/(1<<30))
}
