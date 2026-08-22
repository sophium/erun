package eruncommon

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// A multi-arch, many-image release is the single build most likely to fill a
// node's disk — exactly what evicted the pod mid-release in #1051 (a 12-image
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

// ensureReleaseDiskHeadroom prunes reclaimable docker build cache before a
// release's build starts, and — where the docker root's free space is
// actually observable from this process — refuses up front when it is
// already too low, rather than letting the build itself trigger the eviction
// it cannot recover from.
//
// The docker daemon a release builds against often lives in a separate
// container (the erun-dind sidecar) with its own filesystem, so the docker
// root's free space is frequently not visible from this process at all; the
// prune still runs regardless, but the numeric refusal only fires when the
// read is conclusive. An inconclusive read is not an answer — the same
// "known failure over invented behavior" posture as
// ensureReleaseBaseBranchUnmoved — so it lets the release proceed exactly as
// it does today.
func ensureReleaseDiskHeadroom(ctx Context) error {
	ctx.TraceCommand("", "docker", "builder", "prune", "-f")
	if ctx.DryRun {
		return nil
	}
	if err := Command("docker", "builder", "prune", "-f").Run(); err != nil {
		ctx.Trace("release: docker builder prune failed, continuing: " + err.Error())
	}

	free, ok := dockerRootFreeDiskBytes()
	if !ok {
		ctx.Trace("release: docker root free disk space is not observable from this process; skipping the headroom check")
		return nil
	}
	floor := resolveReleaseMinDiskHeadroomBytes()
	ctx.Trace(fmt.Sprintf("release: docker root has %s free (floor %s)", formatGiB(free), formatGiB(floor)))
	if free < floor {
		return fmt.Errorf("only %s free at the docker root, below the %s a multi-arch release build needs: "+
			"free up space (docker system prune, remove unused images) or grow the volume before retrying — "+
			"filling this disk is what evicts the pod running the release",
			formatGiB(free), formatGiB(floor))
	}
	return nil
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

// dockerRootFreeDiskBytes asks the docker daemon where its root directory is,
// then reads that path's free space with `df` — a real filesystem read, not a
// guess from image/cache sizes docker itself reports, since none of those add
// up to "how much room is actually left on this node". Windows has no `df`;
// or/anywhere the read fails or the root is not a path this process can see,
// ok is false and the caller treats the check as inconclusive.
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
	if _, statErr := os.Stat(root); statErr != nil {
		return 0, false
	}
	dfOut, err := Command("df", "-Pk", root).Output()
	if err != nil {
		return 0, false
	}
	return parseDFAvailableBytes(string(dfOut))
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
