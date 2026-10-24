package build

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// TestDiskHeadroomRemedyFollowsTheMeasurement pins the remedy itself, for both
// callers: a build renders the same derived remedy into its warning that a
// release renders into its refusal, so neither can name a remediation the
// measurement has already ruled out.
func TestDiskHeadroomRemedyFollowsTheMeasurement(t *testing.T) {
	const (
		free  = diskHeadroomBelow
		floor = diskHeadroomTestFloor
	)
	dockerCanStillFree := shared.DiskHeadroomShortfall{
		Free: free, Floor: floor, Path: dockerRootPath,
		DockerReclaimExhausted: false,
	}
	if got := dockerCanStillFree.Remedy(); !strings.Contains(got, "docker system prune") {
		t.Fatalf("a shortfall docker can still reach must name the docker remedy, got %q", got)
	}

	dockerHasNothingToFree := shared.DiskHeadroomShortfall{
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

	// The build cache still holding the space outranks both branches above: a
	// store can report reclaimable images — it did, 28.09GB of them — while the
	// bytes are in the cache, and naming the removal that frees none of them is
	// the misdirection this pins out.
	buildCacheStillHoldsIt := shared.DiskHeadroomShortfall{
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

// diskHeadroomShortLimits is the production check's wall-clock bounds shrunk
// to what a test can wait out. The scenario is unchanged — a docker that stops
// answering — so the only thing the shorter bound changes is how long the
// regression takes to prove itself, not what it proves.
func diskHeadroomShortLimits() shared.DiskHeadroomTimeouts {
	const short = 500 * time.Millisecond
	return shared.DiskHeadroomTimeouts{Read: short, Probe: short, Prune: short}
}

// headroomTestBound is how long a test waits for the preflight before calling
// it non-terminating. It is deliberately far above the short limits, so a
// loaded machine cannot turn a bounded return into a reported hang.
const headroomTestBound = 30 * time.Second

// awaitHeadroomPreflight runs one check and fails the test if it is still
// running at headroomTestBound, which is the reported failure: a preflight
// that neither lets the build proceed nor fails it, and so writes no terminal
// record for a gate to report.
func awaitHeadroomPreflight(t *testing.T, ctx core.Context, policy shared.DiskHeadroomPolicy) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- shared.EnsureDiskHeadroomWith(ctx, policy, shared.DockerRootDiskBytes, shared.DockerReclaimableBytes, shared.DockerBuildCacheReclaimable, shared.RunDiskHeadroomPrune)
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(headroomTestBound):
		t.Fatalf("the disk headroom preflight had not returned after %s: the build neither proceeds nor fails, and nothing records a terminal outcome", headroomTestBound)
		return nil
	}
}

// TestDiskHeadroomPreflightEndsWhenTheDaemonStopsAnswering reproduces the
// reported hang through the real commands this check runs: a docker daemon
// that stops answering — the state the incident's own readiness probe caught,
// with a two-second `docker info` timing out — used to leave the preflight
// blocked in an unbounded subprocess forever. Nothing in erun's build is
// bounded, so the build it fronts never returned either.
func TestDiskHeadroomPreflightEndsWhenTheDaemonStopsAnswering(t *testing.T) {
	// Neither binary ever answers, matching a daemon wedged across its whole
	// API rather than one slow call. Both are bounded sleeps rather than
	// infinite ones so a failure to kill them cannot outlive the test.
	// `exec` so the stub is the sleeping process itself rather than a shell
	// holding it as a child: a real docker is killed on its own, and an
	// orphaned grandchild would otherwise hold the read's pipe past the kill.
	t.Setenv("ERUN_DOCKER_BIN", writeExecutableScript(t, "exec sleep 3600"))
	t.Setenv("ERUN_DF_BIN", writeExecutableScript(t, "exec sleep 3600"))
	t.Setenv(shared.ReleaseMinDiskHeadroomEnv, strconv.FormatUint(diskHeadroomTestFloor, 10))

	logs := &strings.Builder{}
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, logs, logs)}
	policy := buildDiskHeadroomPolicy
	policy.Limits = diskHeadroomShortLimits()

	err := awaitHeadroomPreflight(t, ctx, policy)
	if err != nil {
		t.Fatalf("a build must still proceed on a disk it could not measure, got %v", err)
	}
	// Proceeding silently would be the other half of the defect: the operator
	// has to be able to tell an unmeasured disk from a healthy one, and a
	// daemon that did not answer from one that is simply absent.
	message := logs.String()
	if !strings.Contains(message, "not observable") {
		t.Fatalf("expected the skipped check to say so, got %q", message)
	}
	if !strings.Contains(message, "did not answer within") {
		t.Fatalf("expected the skipped check to name the daemon that stopped answering, got %q", message)
	}
}
