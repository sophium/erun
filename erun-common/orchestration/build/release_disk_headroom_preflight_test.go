package build

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared/diskheadroom"
)

const headroomShortBound = 500 * time.Millisecond

// headroomAnsweringBound is far above what a stand-in that answers needs, so a loaded
// machine slowing its reply is never read as the daemon not answering.
const headroomAnsweringBound = 5 * time.Second

// headroomTestBound is far above every bound above so a loaded machine is not read as a hang.
const headroomTestBound = 30 * time.Second

// diskHeadroomShortLimits is only for stand-ins that never answer: a call that cannot
// return times out the same way however loaded the machine is.
func diskHeadroomShortLimits() diskheadroom.DiskHeadroomTimeouts {
	return diskheadroom.DiskHeadroomTimeouts{Read: headroomShortBound, Probe: headroomShortBound, Prune: headroomShortBound}
}

func diskHeadroomAnsweringLimits() diskheadroom.DiskHeadroomTimeouts {
	return diskheadroom.DiskHeadroomTimeouts{Read: headroomAnsweringBound, Probe: headroomAnsweringBound, Prune: headroomAnsweringBound}
}

// diskHeadroomHangingPruneLimits shortens only the prune, whose stand-in never returns.
func diskHeadroomHangingPruneLimits() diskheadroom.DiskHeadroomTimeouts {
	limits := diskHeadroomAnsweringLimits()
	limits.Prune = headroomShortBound
	return limits
}

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
