package build

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared/diskheadroom"
)

// buildCacheTestVolume is the chart's default 50 GiB docker volume.
const buildCacheTestVolume uint64 = 50 << 30

func buildCacheTestBounds() diskheadroom.BuildCacheBounds {
	return diskheadroom.ResolveBuildCacheBounds(buildCacheTestVolume)
}

type buildCacheRetentionCase struct {
	name       string
	dryRun     bool
	volume     uint64
	volumeErr  error
	cacheBytes uint64
	cacheErr   error
	pruneErr   error

	wantWarn   bool
	wantPrune  bool
	prunedTo   uint64
	wantReads  int
	wantTraces []string
	// wantSilent requires no output at all.
	wantSilent bool
}

func buildCacheRetentionCases() []buildCacheRetentionCase {
	bounds := buildCacheTestBounds()
	return []buildCacheRetentionCase{
		{
			// Far past the share, with healthy free space: the state that grew unnoticed.
			name:       "a cache past its ceiling is reclaimed even with free space to spare",
			volume:     buildCacheTestVolume,
			cacheBytes: bounds.Ceiling + (1 << 30),
			wantReads:  1,
			wantPrune:  true,
			prunedTo:   bounds.Ceiling,
		},
		{
			name:       "a cache exactly at the ceiling is reclaimed",
			volume:     buildCacheTestVolume,
			cacheBytes: bounds.Ceiling,
			wantReads:  1,
			wantPrune:  true,
			prunedTo:   bounds.Ceiling,
		},
		{
			name:       "a cache past the warning mark is reported before it is reclaimed",
			volume:     buildCacheTestVolume,
			cacheBytes: bounds.Warning + (1 << 20),
			wantReads:  1,
			wantWarn:   true,
			wantTraces: []string{"warning:"},
		},
		{
			name:       "a cache below the warning mark is left alone and silent",
			volume:     buildCacheTestVolume,
			cacheBytes: bounds.Warning - (1 << 20),
			wantReads:  1,
		},
		{
			name:       "an empty cache is silent",
			volume:     buildCacheTestVolume,
			cacheBytes: 0,
			wantReads:  1,
		},
		{
			name:       "a failed reclaim is reported rather than assumed to have happened",
			volume:     buildCacheTestVolume,
			cacheBytes: bounds.Ceiling + (1 << 30),
			pruneErr:   errors.New("boom"),
			wantReads:  1,
			wantPrune:  true,
			prunedTo:   bounds.Ceiling,
			wantTraces: []string{"the build-cache reclaim did not complete"},
		},
		{
			// Reclaiming an unmeasured cache would destroy layers on a guess.
			name:       "an unreadable cache size is left bounded by the disk floor alone",
			volume:     buildCacheTestVolume,
			cacheErr:   errors.New("docker system df is unreadable"),
			wantReads:  1,
			wantTraces: []string{"could not be read"},
		},
		{
			// Runtime envs have no dind volume; the common case stays quiet.
			name:      "an environment with no docker volume is left alone and silent",
			volumeErr: diskheadroom.ErrNoDockerVolume,
			wantReads: 0,
		},
		{
			// A declared but unusable size is a deployment mistake and must be reported.
			name:       "a declared but unusable volume size is reported",
			volumeErr:  errors.New(`ERUN_DOCKER_VOLUME_BYTES="50Gi" is not a positive byte count`),
			wantReads:  0,
			wantTraces: []string{"no build-cache ceiling applies"},
		},
		{
			// A preview cannot know whether a reclaim is due.
			name:       "dry run neither measures nor reclaims nor announces a bound",
			dryRun:     true,
			volume:     buildCacheTestVolume,
			cacheBytes: buildCacheTestBounds().Ceiling + (1 << 30),
			wantReads:  0,
			wantSilent: true,
		},
	}
}

func TestEnsureBuildCacheRetentionWith(t *testing.T) {
	for _, tc := range buildCacheRetentionCases() {
		t.Run(tc.name, func(t *testing.T) {
			runBuildCacheRetentionCase(t, tc)
		})
	}
}

func runBuildCacheRetentionCase(t *testing.T, tc buildCacheRetentionCase) {
	t.Helper()

	// Clear the node bound so an ambient declaration cannot rebind these cases.
	t.Setenv(diskheadroom.BuildCacheNodeBytesEnv, "")
	t.Setenv(diskheadroom.BuildCacheCoTenantsEnv, "")

	readCalls := 0
	readCacheBytes := func(time.Duration) (uint64, error) {
		readCalls++
		if tc.cacheErr != nil {
			return 0, tc.cacheErr
		}
		return tc.cacheBytes, nil
	}
	readVolume := func() (uint64, error) {
		if tc.volumeErr != nil {
			return 0, tc.volumeErr
		}
		return tc.volume, nil
	}
	pruneCalls := 0
	var prunedTo uint64
	prune := func(ceiling uint64, _ time.Duration) error {
		pruneCalls++
		prunedTo = ceiling
		return tc.pruneErr
	}

	logs := &strings.Builder{}
	ctx := core.Context{DryRun: tc.dryRun, Logger: core.NewLoggerWithWriters(core.VerbosityInfo, logs, logs)}
	diskheadroom.EnsureBuildCacheRetentionWith(ctx, buildDiskHeadroomPolicy, readVolume, readCacheBytes, prune)

	if readCalls != tc.wantReads {
		t.Fatalf("expected %d cache-size reads, got %d", tc.wantReads, readCalls)
	}
	if gotPrune := pruneCalls > 0; gotPrune != tc.wantPrune {
		t.Fatalf("prune called = %v, want %v (calls=%d)", gotPrune, tc.wantPrune, pruneCalls)
	}
	if tc.wantPrune && prunedTo != tc.prunedTo {
		t.Fatalf("the reclaim must be bounded to the ceiling %d, got %d", tc.prunedTo, prunedTo)
	}
	assertBuildCacheRetentionOutput(t, tc, logs.String())
}

func assertBuildCacheRetentionOutput(t *testing.T, tc buildCacheRetentionCase, message string) {
	t.Helper()
	if gotWarn := strings.Contains(message, "warning:"); gotWarn != tc.wantWarn {
		t.Fatalf("warning = %v, want %v; output was %q", gotWarn, tc.wantWarn, message)
	}
	for _, want := range tc.wantTraces {
		if !strings.Contains(message, want) {
			t.Fatalf("expected the output to contain %q, got %q", want, message)
		}
	}
	if tc.wantSilent && message != "" {
		t.Fatalf("expected no output at all, got %q", message)
	}
}

// A cache past its volume share on a disk with healthy free space must still
// be bounded, before the node reaches disk pressure.
func TestBuildDiskHeadroomBoundsTheCacheWhileFreeSpaceIsStillHealthy(t *testing.T) {
	bounds := buildCacheTestBounds()
	if bounds.Ceiling >= buildCacheTestVolume {
		t.Fatalf("the ceiling %d must bound the cache below the whole volume %d", bounds.Ceiling, buildCacheTestVolume)
	}

	const reportedCacheBytes uint64 = 43580000000
	if reportedCacheBytes <= bounds.Ceiling {
		t.Fatalf("the reported cache %d must be past the ceiling %d for this to reproduce", reportedCacheBytes, bounds.Ceiling)
	}

	root := t.TempDir()
	record := filepath.Join(t.TempDir(), "docker-invocations")
	t.Setenv("ERUN_DOCKER_BIN", writeExecutableScript(t, `case "$1" in
  info) echo "`+root+`" ;;
  system) echo "Build Cache|43.58GB" ;;
  *) printf '%s\n' "$@" >> `+record+` ;;
esac`))
	// Free space well above any floor, so only the bound can fire.
	t.Setenv("ERUN_DF_BIN", writeExecutableScript(t, `echo "Filesystem     1024-blocks     Used Available Capacity Mounted on"
echo "/dev/fake       419430400 314572800  104857600     75% `+root+`"`))
	t.Setenv(diskheadroom.DockerVolumeBytesEnv, strconv.FormatUint(buildCacheTestVolume, 10))
	t.Setenv(diskheadroom.ReleaseMinDiskHeadroomEnv, strconv.FormatUint(1<<30, 10))

	logs := &strings.Builder{}
	if err := awaitDiskHeadroomPreflight(t, core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, logs, logs)},
		EnsureBuildDiskHeadroom); err != nil {
		t.Fatalf("a build must proceed on a healthy disk, got %v", err)
	}

	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("no reclaim reached docker at all: a cache %d bytes past its %d ceiling was left unbounded, which is the reported growth", reportedCacheBytes, bounds.Ceiling)
	}
	assertReclaimIsBoundedToTheCeiling(t, string(raw), bounds.Ceiling)
	if message := logs.String(); !strings.Contains(message, "ceiling") {
		t.Fatalf("expected the over-ceiling cache to be reported, got %q", message)
	}
}

// assertReclaimIsBoundedToTheCeiling checks the docker invocation prunes to the ceiling.
func assertReclaimIsBoundedToTheCeiling(t *testing.T, recorded string, ceiling uint64) {
	t.Helper()
	args := strings.Fields(recorded)
	if len(args) < 2 || args[0] != "buildx" || args[1] != "prune" {
		t.Fatalf("expected the reclaim to run through the command that implements its bound, got %v", args)
	}
	want := strconv.FormatUint(ceiling, 10)
	for i, arg := range args {
		if arg == "--max-used-space" && i+1 < len(args) && args[i+1] == want {
			return
		}
	}
	t.Fatalf("expected the reclaim bounded by --max-used-space %s, got %v", want, args)
}

// Growth must be reported before it is reclaimed.
func TestBuildCacheRetentionReclaimsOnlyAtTheCeiling(t *testing.T) {
	bounds := buildCacheTestBounds()
	const step uint64 = buildCacheTestVolume / 20

	firstWarning := uint64(0)
	firstPrune := uint64(0)
	for size := step; size <= buildCacheTestVolume; size += step {
		logs := &strings.Builder{}
		pruned := false
		diskheadroom.EnsureBuildCacheRetentionWith(core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, logs, logs)}, buildDiskHeadroomPolicy,
			func() (uint64, error) { return buildCacheTestVolume, nil },
			func(time.Duration) (uint64, error) { return size, nil },
			func(uint64, time.Duration) error { pruned = true; return nil })

		if firstWarning == 0 && strings.Contains(logs.String(), "warning:") {
			firstWarning = size
		}
		if firstPrune == 0 && pruned {
			firstPrune = size
		}
	}

	if firstWarning == 0 {
		t.Fatal("no cache size on the way to the ceiling was ever reported as growing")
	}
	if firstPrune == 0 {
		t.Fatal("no cache size on the way to the ceiling was ever reclaimed")
	}
	if firstWarning >= firstPrune {
		t.Fatalf("growth must be reported before it is reclaimed: first warning at %d, first reclaim at %d", firstWarning, firstPrune)
	}
	if firstPrune < bounds.Ceiling {
		t.Fatalf("the first reclaim at %d must not fire below the ceiling %d", firstPrune, bounds.Ceiling)
	}
}

// `docker builder prune` rejects unknown flags, so this asserts against a real
// invocation of the stub rather than the built argument list.
func TestBuildCacheRetentionPruneNamesACommandThatAcceptsItsBound(t *testing.T) {
	record := filepath.Join(t.TempDir(), "prune-invocation")
	t.Setenv("ERUN_DOCKER_BIN", writeExecutableScript(t, `case "$1" in
  system) echo "Build Cache|60GB" ;;
  *) printf '%s\n' "$@" > `+record+` ;;
esac`))
	t.Setenv(diskheadroom.DockerVolumeBytesEnv, strconv.FormatUint(buildCacheTestVolume, 10))

	if err := awaitBuildCacheRetention(t, buildDiskHeadroomPolicy); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the prune never reached the docker command: %v", err)
	}
	assertReclaimIsBoundedToTheCeiling(t, string(raw), buildCacheTestBounds().Ceiling)
}

// awaitDiskHeadroomPreflight fails the test if the check blocks.
func awaitDiskHeadroomPreflight(t *testing.T, ctx core.Context, check func(core.Context) error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- check(ctx) }()
	select {
	case err := <-done:
		return err
	case <-time.After(headroomTestBound):
		t.Fatalf("the disk headroom check had not returned after %s", headroomTestBound)
		return nil
	}
}

// awaitBuildCacheRetention runs the production wiring with shrunk bounds.
func awaitBuildCacheRetention(t *testing.T, policy diskheadroom.DiskHeadroomPolicy) error {
	t.Helper()
	policy.Limits = diskHeadroomShortLimits()
	return awaitDiskHeadroomPreflight(t, core.Context{}, func(ctx core.Context) error {
		diskheadroom.EnsureBuildCacheRetention(ctx, policy)
		return nil
	})
}
