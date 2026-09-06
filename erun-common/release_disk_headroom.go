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

// releaseMinDiskHeadroomEnv overrides releaseMinDiskHeadroomBytes. Not a
// production knob to reach for casually — it exists so a constrained test or
// a genuinely small node can tune the floor without recompiling.
const releaseMinDiskHeadroomEnv = "ERUN_RELEASE_MIN_DISK_HEADROOM_BYTES"

// releaseMinDiskHeadroomBytes is the default floor: a released multi-arch,
// many-image build has consumed tens of gigabytes on the reported incident
// node, so headroom well under that is already too little to safely absorb
// one more release.
const releaseMinDiskHeadroomBytes uint64 = 20 << 30 // 20 GiB

// diskHeadroomFreeSpaceFunc reads the docker root's current free space,
// returning ok=false when the read is inconclusive. Injectable so the
// decision logic in ensureReleaseDiskHeadroomWith can be unit-tested without
// a real docker daemon.
type diskHeadroomFreeSpaceFunc func() (free uint64, ok bool)

// diskHeadroomPruneFunc bounds a build-cache prune to leave at least floor
// bytes free. Injectable for the same reason as diskHeadroomFreeSpaceFunc.
type diskHeadroomPruneFunc func(floor uint64) error

// ensureReleaseDiskHeadroom reads the docker root's free space before a
// release's build starts and, only when it is actually below the floor,
// prunes reclaimable build cache down to that floor and refuses if the disk
// is still too full afterward — rather than letting the build itself trigger
// the eviction it cannot recover from.
func ensureReleaseDiskHeadroom(ctx Context) error {
	return ensureReleaseDiskHeadroomWith(ctx, dockerRootFreeDiskBytes, runDiskHeadroomPrune)
}

// ensureReleaseDiskHeadroomWith holds the decision logic: read first, prune
// only when below the floor, then re-check before refusing. The docker
// daemon a release builds against often lives in a separate container (the
// erun-dind sidecar) with its own filesystem, so readFree makes its own
// attempt to reach that daemon's filesystem before giving up; when it still
// cannot, that inconclusive read is not an answer — the same "known failure
// over invented behavior" posture as ensureReleaseBaseBranchUnmoved — so it
// lets the release proceed exactly as it does today, with no prune at all.
func ensureReleaseDiskHeadroomWith(ctx Context, readFree diskHeadroomFreeSpaceFunc, prune diskHeadroomPruneFunc) error {
	floor := resolveReleaseMinDiskHeadroomBytes()

	ctx.TraceCommand("", "docker", "info", "-f", "{{.DockerRootDir}}")
	if ctx.DryRun {
		return nil
	}

	free, ok := readFree()
	if !ok {
		ctx.Trace("release: docker root free disk space is not observable from this process; skipping the headroom check")
		return nil
	}
	ctx.Trace(fmt.Sprintf("release: docker root has %s free (floor %s)", formatGiB(free), formatGiB(floor)))
	if free >= floor {
		return nil
	}

	ctx.Trace(fmt.Sprintf("release: docker root free disk is below the %s floor; pruning reclaimable build cache down to it", formatGiB(floor)))
	ctx.TraceCommand("", "docker", "builder", "prune", "-f", "--min-free-space", strconv.FormatUint(floor, 10))
	if err := prune(floor); err != nil {
		ctx.Trace("release: docker builder prune failed, continuing: " + err.Error())
	}

	free, ok = readFree()
	if ok && free < floor {
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

func resolveReleaseMinDiskHeadroomBytes() uint64 {
	raw := strings.TrimSpace(os.Getenv(releaseMinDiskHeadroomEnv))
	if raw == "" {
		return releaseMinDiskHeadroomBytes
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return releaseMinDiskHeadroomBytes
	}
	return value
}

// diskHeadroomProbeImage is a tiny, pinned image with a `df` binary, used
// only to read the docker daemon's own filesystem from the inside when this
// process cannot see the daemon's root directory itself (see
// dockerRootFreeDiskBytesViaProbe).
const diskHeadroomProbeImage = "busybox:1.36.1"

// dockerRootFreeDiskBytes asks the docker daemon where its root directory is,
// then reads that path's free space — a real filesystem read, not a guess
// from image/cache sizes docker itself reports, since none of those add up to
// "how much room is actually left on this node". Windows has no `df`.
//
// The docker daemon a release builds against often lives in a separate
// container (the erun-dind sidecar) with its own filesystem, so the root this
// process just resolved is frequently not a path it can stat directly. That
// case falls back to asking the daemon itself: it can always reach its own
// filesystem, so running a throwaway container with that root bind-mounted
// turns "not visible from here" into a real read instead of a reason to give
// up. Only when both routes fail is ok false.
func dockerRootFreeDiskBytes() (free uint64, ok bool) {
	if runtime.GOOS == "windows" {
		return 0, false
	}
	rootOut, err := Command("docker", "info", "-f", "{{.DockerRootDir}}").Output()
	if err != nil {
		return 0, false
	}
	root := strings.TrimSpace(string(rootOut))
	if root == "" {
		return 0, false
	}
	if _, statErr := os.Stat(root); statErr == nil {
		dfOut, err := Command("df", "-Pk", root).Output()
		if err != nil {
			return 0, false
		}
		return parseDFAvailableBytes(string(dfOut))
	}
	return dockerRootFreeDiskBytesViaProbe(root)
}

// dockerRootFreeDiskBytesViaProbe reads free space at root as the docker
// daemon itself sees it, by asking the daemon to run a throwaway container
// with root bind-mounted read-only and df'd from inside. This is what makes
// the read conclusive when the daemon lives in a different filesystem
// namespace than this process (the erun-dind sidecar case): the daemon can
// always reach its own root, even when this process cannot.
func dockerRootFreeDiskBytesViaProbe(root string) (uint64, bool) {
	out, err := Command("docker", "run", "--rm", "-v", root+":/host:ro", diskHeadroomProbeImage, "df", "-Pk", "/host").Output()
	if err != nil {
		return 0, false
	}
	return parseDFAvailableBytes(string(out))
}

// parseDFAvailableBytes reads the "Available" column (in 1024-byte blocks,
// guaranteed by -Pk) from the last line of `df`'s POSIX-format output — the
// data row, whether or not the filesystem name pushed it onto its own line.
// A long filesystem identifier wrapping the name onto its own line shifts
// every later column left by one, so the column is found by its neighbor
// (the "Capacity" percentage) rather than by a fixed index.
func parseDFAvailableBytes(output string) (uint64, bool) {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) < 2 {
		return 0, false
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
			return 0, false
		}
		return availableKB * 1024, true
	}
	return 0, false
}

func formatGiB(bytes uint64) string {
	return fmt.Sprintf("%.1f GiB", float64(bytes)/(1<<30))
}
