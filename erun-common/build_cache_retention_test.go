package eruncommon

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// buildCacheTestVolume is the docker volume the chart declares by default, in
// bytes: 50 GiB. The marks derived from it are asserted rather than restated,
// so a change to the shares cannot pass by moving both sides together.
const buildCacheTestVolume uint64 = 50 << 30

func buildCacheTestBounds() buildCacheBounds {
	return resolveBuildCacheBounds(buildCacheTestVolume)
}

func TestResolveBuildCacheBounds(t *testing.T) {
	bounds := buildCacheTestBounds()

	t.Run("the ceiling leaves a fifth of the volume to what a cache prune cannot reach", func(t *testing.T) {
		if want := uint64(40 << 30); bounds.ceiling != want {
			t.Fatalf("ceiling = %d, want %d (80%% of a 50 GiB volume)", bounds.ceiling, want)
		}
	})

	// A warning at the threshold it warns about is a status line printed after
	// the decision, not a warning: the whole value of it is arriving while the
	// remedy is still ahead rather than already taken.
	t.Run("the warning sits strictly below the ceiling", func(t *testing.T) {
		if want := uint64(35 << 30); bounds.warning != want {
			t.Fatalf("warning = %d, want %d (70%% of a 50 GiB volume)", bounds.warning, want)
		}
		if bounds.warning >= bounds.ceiling {
			t.Fatalf("warning %d must sit below ceiling %d", bounds.warning, bounds.ceiling)
		}
	})

	t.Run("scales with the volume rather than being a fixed size", func(t *testing.T) {
		// The same environment on a volume four times the size is allowed four
		// times the cache; a fixed ceiling would be far too tight for one and far
		// too loose for the other.
		double := resolveBuildCacheBounds(buildCacheTestVolume * 4)
		if double.ceiling != bounds.ceiling*4 {
			t.Fatalf("ceiling = %d, want %d", double.ceiling, bounds.ceiling*4)
		}
	})
}

func TestDockerVolumeBytes(t *testing.T) {
	t.Run("reads the declared volume size", func(t *testing.T) {
		t.Setenv(dockerVolumeBytesEnv, strconv.FormatUint(buildCacheTestVolume, 10))
		got, err := dockerVolumeBytes()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != buildCacheTestVolume {
			t.Fatalf("got %d, want %d", got, buildCacheTestVolume)
		}
	})

	// Absent is not zero. An environment with no docker volume has no cache to
	// bound, and a zero-byte volume would put every cache over its ceiling.
	t.Run("an absent declaration is an error, not a zero-byte volume", func(t *testing.T) {
		t.Setenv(dockerVolumeBytesEnv, "")
		if _, err := dockerVolumeBytes(); err == nil {
			t.Fatal("expected an unset volume size to be reported as unknown")
		}
	})

	t.Run("a malformed declaration is not a size to reclaim against", func(t *testing.T) {
		for _, raw := range []string{"not-a-number", "0", "-1", "50Gi"} {
			t.Setenv(dockerVolumeBytesEnv, raw)
			if _, err := dockerVolumeBytes(); err == nil {
				t.Fatalf("expected %q to be rejected", raw)
			}
		}
	})
}

// buildCacheRetentionCase drives one pass of the retention decision with
// injected readings, in the shape release_disk_headroom_test.go's cases use.
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
	// wantSilent is the strongest of the output assertions: the case must
	// produce nothing at all, which is what a preview of this check owes a
	// reader who cannot act on anything it would say.
	wantSilent bool
}

func buildCacheRetentionCases() []buildCacheRetentionCase {
	bounds := buildCacheTestBounds()
	return []buildCacheRetentionCase{
		{
			// The state the reported growth passed through silently: far more
			// cache than this environment is sized to keep, on a disk whose free
			// space is still healthy enough that no floor anywhere would fire.
			name:       "a cache past its ceiling is reclaimed even with free space to spare",
			volume:     buildCacheTestVolume,
			cacheBytes: bounds.ceiling + (1 << 30),
			wantReads:  1,
			wantPrune:  true,
			prunedTo:   bounds.ceiling,
		},
		{
			name:       "a cache exactly at the ceiling is reclaimed",
			volume:     buildCacheTestVolume,
			cacheBytes: bounds.ceiling,
			wantReads:  1,
			wantPrune:  true,
			prunedTo:   bounds.ceiling,
		},
		{
			// Over the warning mark but inside the bound: reported, not touched.
			name:       "a cache past the warning mark is reported before it is reclaimed",
			volume:     buildCacheTestVolume,
			cacheBytes: bounds.warning + (1 << 20),
			wantReads:  1,
			wantWarn:   true,
			wantTraces: []string{"warning:"},
		},
		{
			name:       "a cache below the warning mark is left alone and silent",
			volume:     buildCacheTestVolume,
			cacheBytes: bounds.warning - (1 << 20),
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
			cacheBytes: bounds.ceiling + (1 << 30),
			pruneErr:   errors.New("boom"),
			wantReads:  1,
			wantPrune:  true,
			prunedTo:   bounds.ceiling,
			wantTraces: []string{"the build-cache reclaim did not complete"},
		},
		{
			// An unmeasured cache is not a cache that is over its bound.
			// Reclaiming one would destroy layers on a guess.
			name:       "an unreadable cache size is left bounded by the disk floor alone",
			volume:     buildCacheTestVolume,
			cacheErr:   errors.New("docker system df is unreadable"),
			wantReads:  1,
			wantTraces: []string{"could not be read"},
		},
		{
			// An environment with no docker volume — a runtime env runs no dind
			// sidecar — has no cache to bound, and does not measure against a
			// size it invented. It is also the common case, so it stays quiet
			// rather than announcing itself on every build of every one of them.
			name:      "an environment with no docker volume is left alone and silent",
			volumeErr: errNoDockerVolume,
			wantReads: 0,
		},
		{
			// A size the deployment did declare but this process cannot use is
			// not that quiet state: it is a deployment mistake, and one that
			// would otherwise leave the cache unbounded with nothing to say why.
			name:       "a declared but unusable volume size is reported",
			volumeErr:  errors.New(`ERUN_DOCKER_VOLUME_BYTES="50Gi" is not a positive byte count`),
			wantReads:  0,
			wantTraces: []string{"no build-cache ceiling applies"},
		},
		{
			// Nothing is read, reclaimed, or printed: a preview cannot know
			// whether a reclaim is due, so it must not imply that one is not.
			name:       "dry run neither measures nor reclaims nor announces a bound",
			dryRun:     true,
			volume:     buildCacheTestVolume,
			cacheBytes: buildCacheTestBounds().ceiling + (1 << 30),
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
	ctx := Context{DryRun: tc.dryRun, Logger: NewLoggerWithWriters(VerbosityInfo, logs, logs)}
	ensureBuildCacheRetentionWith(ctx, buildDiskHeadroomPolicy, readVolume, readCacheBytes, prune)

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

// assertBuildCacheRetentionOutput checks what one pass said against what the
// case expects it to say — and, where a preview is concerned, that it said
// nothing at all.
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

// TestBuildDiskHeadroomBoundsTheCacheWhileFreeSpaceIsStillHealthy is the
// reproduction of the reported growth. The report's state was a build cache
// accumulating per environment with nothing anywhere comparing it to a bound:
// measured at 51.39GB on one environment and 43.58GB on another, on disks whose
// free space was still above every floor — so nothing warned, and nothing
// reclaimed, until the node crossed into disk pressure and evicted every pod on
// it.
//
// The case below is exactly that state: a cache far past the share its own
// docker volume is sized for, on a docker root with free space well above the
// floor. It runs the production entrypoint an ordinary build uses, so the floor
// guard is live and silent — there is room to spare — and the only thing that
// can possibly fire is the missing bound. Before the bound existed this test
// saw no prune at all and no output about the cache; the reported growth is
// what that silence looks like from inside a build.
func TestBuildDiskHeadroomBoundsTheCacheWhileFreeSpaceIsStillHealthy(t *testing.T) {
	bounds := buildCacheTestBounds()
	if bounds.ceiling >= buildCacheTestVolume {
		t.Fatalf("the ceiling %d must bound the cache below the whole volume %d", bounds.ceiling, buildCacheTestVolume)
	}

	// The figure the report measured on a live environment: 43.58GB of build
	// cache, past the ceiling a 50GiB volume allows.
	const reportedCacheBytes uint64 = 43580000000
	if reportedCacheBytes <= bounds.ceiling {
		t.Fatalf("the reported cache %d must be past the ceiling %d for this to reproduce", reportedCacheBytes, bounds.ceiling)
	}

	root := t.TempDir()
	record := filepath.Join(t.TempDir(), "docker-invocations")
	t.Setenv("ERUN_DOCKER_BIN", writeExecutableScript(t, `case "$1" in
  info) echo "`+root+`" ;;
  system) echo "Build Cache|43.58GB" ;;
  *) printf '%s\n' "$@" >> `+record+` ;;
esac`))
	// 100 GiB free of 400 GiB at the docker root: comfortably above any floor,
	// so the disk-floor guard never reaches its prune.
	t.Setenv("ERUN_DF_BIN", writeExecutableScript(t, `echo "Filesystem     1024-blocks     Used Available Capacity Mounted on"
echo "/dev/fake       419430400 314572800  104857600     75% `+root+`"`))
	t.Setenv(dockerVolumeBytesEnv, strconv.FormatUint(buildCacheTestVolume, 10))
	t.Setenv(releaseMinDiskHeadroomEnv, strconv.FormatUint(1<<30, 10))

	logs := &strings.Builder{}
	if err := awaitDiskHeadroomPreflight(t, Context{Logger: NewLoggerWithWriters(VerbosityInfo, logs, logs)},
		func(ctx Context) error { return ensureBuildDiskHeadroom(ctx) }); err != nil {
		t.Fatalf("a build must proceed on a healthy disk, got %v", err)
	}

	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("no reclaim reached docker at all: a cache %d bytes past its %d ceiling was left unbounded, which is the reported growth", reportedCacheBytes, bounds.ceiling)
	}
	assertReclaimIsBoundedToTheCeiling(t, string(raw), bounds.ceiling)
	if message := logs.String(); !strings.Contains(message, "ceiling") {
		t.Fatalf("expected the over-ceiling cache to be reported, got %q", message)
	}
}

// assertReclaimIsBoundedToTheCeiling checks one recorded docker invocation is
// the bounded build-cache reclaim this policy is supposed to issue: the command
// that implements the bound, and the ceiling as that bound rather than the
// whole cache.
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

// TestBuildCacheRetentionReclaimsOnlyAtTheCeiling pins the ordering the policy
// exists for: growth is reported strictly before it is acted on. A warning that
// only appears once the reclaim has already run is not a warning, and the point
// of the mark below the ceiling is that an operator sees the trend while the
// cache is still inside its bound.
func TestBuildCacheRetentionReclaimsOnlyAtTheCeiling(t *testing.T) {
	bounds := buildCacheTestBounds()
	// Walk the cache up to the ceiling in twentieths of the volume, so the
	// transition is located by the sweep rather than restated as two constants.
	const step uint64 = buildCacheTestVolume / 20

	firstWarning := uint64(0)
	firstPrune := uint64(0)
	for size := step; size <= buildCacheTestVolume; size += step {
		logs := &strings.Builder{}
		pruned := false
		ensureBuildCacheRetentionWith(Context{Logger: NewLoggerWithWriters(VerbosityInfo, logs, logs)}, buildDiskHeadroomPolicy,
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
	if firstPrune < bounds.ceiling {
		t.Fatalf("the first reclaim at %d must not fire below the ceiling %d", firstPrune, bounds.ceiling)
	}
}

// TestBuildCacheRetentionPruneNamesACommandThatAcceptsItsBound covers the
// failure this bound is one flag away from repeating. The disk-floor prune once
// passed --min-free-space to `docker builder prune`, a command that implements
// only --all, --filter, --force and --keep-storage: every prune was rejected as
// an unknown flag, so the reclaim never happened while the trace advertised one
// and the shortfall was then measured against a cache nothing had moved.
//
// A ceiling handed to the same command would fail the same way and for the same
// reason, so this asserts against a real invocation of the real stub rather
// than against the argument list this file happens to build.
func TestBuildCacheRetentionPruneNamesACommandThatAcceptsItsBound(t *testing.T) {
	record := filepath.Join(t.TempDir(), "prune-invocation")
	t.Setenv("ERUN_DOCKER_BIN", writeExecutableScript(t, `case "$1" in
  system) echo "Build Cache|60GB" ;;
  *) printf '%s\n' "$@" > `+record+` ;;
esac`))
	t.Setenv(dockerVolumeBytesEnv, strconv.FormatUint(buildCacheTestVolume, 10))

	if err := awaitBuildCacheRetention(t, buildDiskHeadroomPolicy); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the prune never reached the docker command: %v", err)
	}
	assertReclaimIsBoundedToTheCeiling(t, string(raw), buildCacheTestBounds().ceiling)
}

// awaitDiskHeadroomPreflight runs a check to its own return and fails the test
// if it does not get there, since one that blocks is a build that neither
// proceeds nor fails and writes no terminal record for a gate to report.
func awaitDiskHeadroomPreflight(t *testing.T, ctx Context, check func(Context) error) error {
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

// awaitBuildCacheRetention runs the production wiring — the real readers, the
// real prune — with the production bounds shrunk to what a test can wait out.
func awaitBuildCacheRetention(t *testing.T, policy diskHeadroomPolicy) error {
	t.Helper()
	policy.limits = diskHeadroomShortLimits()
	return awaitDiskHeadroomPreflight(t, Context{}, func(ctx Context) error {
		ensureBuildCacheRetention(ctx, policy)
		return nil
	})
}
