package eruncommon

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
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

// diskHeadroomFreeSpaceFunc reads the docker root's current free and total
// space, returning ok=false when the read is inconclusive. Total is what makes
// the floor proportional; it is 0 when the read could not determine it, which
// falls back to the absolute floor. Injectable so the decision logic in
// ensureDiskHeadroomWith can be unit-tested without a real docker daemon.
type diskHeadroomFreeSpaceFunc func() (free, total uint64, ok bool)

// diskHeadroomPruneFunc bounds a build-cache prune to leave at least floor
// bytes free. Injectable for the same reason as diskHeadroomFreeSpaceFunc.
type diskHeadroomPruneFunc func(floor uint64) error

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
}

var (
	releaseDiskHeadroomPolicy = diskHeadroomPolicy{label: "release", refuse: true}
	buildDiskHeadroomPolicy   = diskHeadroomPolicy{label: "build", refuse: false}
)

// ensureReleaseDiskHeadroom reads the docker root's free space before a
// release's build starts and, only when it is actually below the floor,
// prunes reclaimable build cache down to that floor and refuses if the disk
// is still too full afterward — rather than letting the build itself trigger
// the eviction it cannot recover from.
func ensureReleaseDiskHeadroom(ctx Context) error {
	return ensureDiskHeadroomWith(ctx, releaseDiskHeadroomPolicy, dockerRootDiskBytes, runDiskHeadroomPrune)
}

// ensureBuildDiskHeadroom is the same preflight for an ordinary build, which
// warns instead of refusing. It is the one that actually runs between releases,
// where the cache growth that fills a node happens.
func ensureBuildDiskHeadroom(ctx Context) error {
	return ensureDiskHeadroomWith(ctx, buildDiskHeadroomPolicy, dockerRootDiskBytes, runDiskHeadroomPrune)
}

// ensureDiskHeadroomWith holds the decision logic: read first, prune only when
// below the floor, then re-check before refusing. The docker daemon a build
// runs against often lives in a separate container (the erun-dind sidecar)
// with its own filesystem, so readFree makes its own attempt to reach that
// daemon's filesystem before giving up; when it still cannot, that
// inconclusive read is not an answer — the same "known failure over invented
// behavior" posture as ensureReleaseBaseBranchUnmoved — so it lets the run
// proceed exactly as it does today, with no prune at all.
func ensureDiskHeadroomWith(ctx Context, policy diskHeadroomPolicy, readFree diskHeadroomFreeSpaceFunc, prune diskHeadroomPruneFunc) error {
	ctx.TraceCommand("", "docker", "info", "-f", "{{.DockerRootDir}}")
	if ctx.DryRun {
		return nil
	}

	free, total, ok := readFree()
	if !ok {
		ctx.Trace(policy.label + ": docker root free disk space is not observable from this process; skipping the headroom check")
		return nil
	}
	floor := resolveMinDiskHeadroomBytes(total)
	ctx.Trace(fmt.Sprintf("%s: docker root has %s free of %s (floor %s)", policy.label, formatGiB(free), formatGiB(total), formatGiB(floor)))
	if free >= floor {
		return nil
	}

	ctx.Trace(fmt.Sprintf("%s: docker root free disk is below the %s floor; pruning reclaimable build cache down to it", policy.label, formatGiB(floor)))
	ctx.TraceCommand("", "docker", "builder", "prune", "-f", "--min-free-space", strconv.FormatUint(floor, 10))
	if err := prune(floor); err != nil {
		ctx.Trace(policy.label + ": docker builder prune failed, continuing: " + err.Error())
	}

	free, _, ok = readFree()
	if ok && free < floor {
		if !policy.refuse {
			ctx.Info(fmt.Sprintf("warning: only %s free at the docker root after pruning, below the %s floor; "+
				"this node is close to the disk pressure that evicts pods — free up space "+
				"(docker system prune, remove unused images) or grow the volume", formatGiB(free), formatGiB(floor)))
			return nil
		}
		return fmt.Errorf("only %s free at the docker root, below the %s a multi-arch release build needs: "+
			"free up space (docker system prune, remove unused images) or grow the volume before retrying — "+
			"filling this disk is what evicts the pod running the release",
			formatGiB(free), formatGiB(floor))
	}
	return nil
}

// runDiskHeadroomPrune is diskHeadroomPruneFunc's real implementation:
// --min-free-space makes the prune a no-op once free space reaches floor,
// rather than reclaiming everything reclaimable the way an unqualified
// `docker builder prune -f` does.
func runDiskHeadroomPrune(floor uint64) error {
	return Command("docker", "builder", "prune", "-f", "--min-free-space", strconv.FormatUint(floor, 10)).Run()
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
func dockerRootDiskBytes() (free, total uint64, ok bool) {
	if runtime.GOOS == "windows" {
		return 0, 0, false
	}
	rootOut, err := Command("docker", "info", "-f", "{{.DockerRootDir}}").Output()
	if err != nil {
		return 0, 0, false
	}
	root := strings.TrimSpace(string(rootOut))
	if root == "" {
		return 0, 0, false
	}
	if _, statErr := os.Stat(root); statErr == nil {
		dfOut, err := Command("df", "-Pk", root).Output()
		if err != nil {
			return 0, 0, false
		}
		return parseDFDiskBytes(string(dfOut))
	}
	return dockerRootDiskBytesViaProbe(root)
}

// dockerRootDiskBytesViaProbe reads free space at root as the docker
// daemon itself sees it, by asking the daemon to run a throwaway container
// with root bind-mounted read-only and df'd from inside. This is what makes
// the read conclusive when the daemon lives in a different filesystem
// namespace than this process (the erun-dind sidecar case): the daemon can
// always reach its own root, even when this process cannot.
func dockerRootDiskBytesViaProbe(root string) (free, total uint64, ok bool) {
	out, err := Command("docker", "run", "--rm", "-v", root+":/host:ro", diskHeadroomProbeImage, "df", "-Pk", "/host").Output()
	if err != nil {
		return 0, 0, false
	}
	return parseDFDiskBytes(string(out))
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
