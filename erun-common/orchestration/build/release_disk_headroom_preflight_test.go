package build

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared/diskheadroom"
)

func TestDiskHeadroomRemedyFollowsTheMeasurement(t *testing.T) {
	const (
		free  = diskHeadroomBelow
		floor = diskHeadroomTestFloor
	)
	dockerCanStillFree := diskheadroom.DiskHeadroomShortfall{
		Free: free, Floor: floor, Path: dockerRootPath,
		DockerReclaimExhausted: false,
	}
	if got := dockerCanStillFree.Remedy(); !strings.Contains(got, "docker system prune") {
		t.Fatalf("a shortfall docker can still reach must name the docker remedy, got %q", got)
	}

	dockerHasNothingToFree := diskheadroom.DiskHeadroomShortfall{
		Free: free, Floor: floor, Path: dockerRootPath,
		SharedPaths:            []string{environmentWorkCloneDir},
		DockerReclaimExhausted: true,
	}
	got := dockerHasNothingToFree.Remedy()
	if strings.Contains(got, "docker system prune") || strings.Contains(got, "remove unused images") {
		t.Fatalf("a shortfall docker cannot free must not name a docker remedy, got %q", got)
	}
	for _, want := range []string{"pruning docker cannot free any of it", environmentWorkCloneDir, "grow the volume"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected the remedy to contain %q, got %q", want, got)
		}
	}

	// Reclaimable images can be reported while the bytes sit in the build cache.
	buildCacheStillHoldsIt := diskheadroom.DiskHeadroomShortfall{
		Free: free, Floor: floor, Path: dockerRootPath,
		DockerReclaimExhausted:     false,
		BuildCacheStillReclaimable: 43 << 30,
	}
	got = buildCacheStillHoldsIt.Remedy()
	if strings.Contains(got, "remove unused images") || strings.Contains(got, "docker system prune") {
		t.Fatalf("a shortfall the build cache is holding must not send the operator to remove images, got %q", got)
	}
	for _, want := range []string{"build cache still holds", "43.0 GiB", "docker buildx prune -a", "grow the volume"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected the remedy to contain %q, got %q", want, got)
		}
	}
}

func diskHeadroomShortLimits() diskheadroom.DiskHeadroomTimeouts {
	const short = 500 * time.Millisecond
	return diskheadroom.DiskHeadroomTimeouts{Read: short, Probe: short, Prune: short}
}

// headroomTestBound is far above the short limits so a loaded machine is not read as a hang.
const headroomTestBound = 30 * time.Second

func awaitHeadroomPreflight(t *testing.T, ctx core.Context, policy diskheadroom.DiskHeadroomPolicy) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- diskheadroom.EnsureDiskHeadroomWith(ctx, policy, diskheadroom.DockerRootDiskBytes, diskheadroom.DockerReclaimableBytes, diskheadroom.DockerBuildCacheReclaimable, diskheadroom.RunDiskHeadroomPrune)
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(headroomTestBound):
		t.Fatalf("the disk headroom preflight had not returned after %s: the build neither proceeds nor fails, and nothing records a terminal outcome", headroomTestBound)
		return nil
	}
}

func TestDiskHeadroomPreflightEndsWhenTheDaemonStopsAnswering(t *testing.T) {
	// `exec` so the stub is the sleeping process: the bound kills only the direct child.
	t.Setenv("ERUN_DOCKER_BIN", writeExecutableScript(t, "exec sleep 3600"))
	t.Setenv("ERUN_DF_BIN", writeExecutableScript(t, "exec sleep 3600"))
	t.Setenv(diskheadroom.ReleaseMinDiskHeadroomEnv, strconv.FormatUint(diskHeadroomTestFloor, 10))

	logs := &strings.Builder{}
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, logs, logs)}
	policy := buildDiskHeadroomPolicy
	policy.Limits = diskHeadroomShortLimits()

	err := awaitHeadroomPreflight(t, ctx, policy)
	if err != nil {
		t.Fatalf("a build must still proceed on a disk it could not measure, got %v", err)
	}
	message := logs.String()
	if !strings.Contains(message, "not observable") {
		t.Fatalf("expected the skipped check to say so, got %q", message)
	}
	if !strings.Contains(message, "did not answer within") {
		t.Fatalf("expected the skipped check to name the daemon that stopped answering, got %q", message)
	}
}
