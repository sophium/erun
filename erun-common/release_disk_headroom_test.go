package eruncommon

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseDFDiskBytes(t *testing.T) {
	t.Run("standard one-line output", func(t *testing.T) {
		output := "Filesystem     1024-blocks     Used Available Capacity Mounted on\n" +
			"/dev/sda1        102400000 51200000  41943040      56% /var/lib/docker\n"
		free, total, ok := parseDFDiskBytes(output)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if want := uint64(41943040) * 1024; free != want {
			t.Fatalf("free = %d, want %d", free, want)
		}
		if want := uint64(102400000) * 1024; total != want {
			t.Fatalf("total = %d, want %d", total, want)
		}
	})

	t.Run("long filesystem name wraps onto its own line", func(t *testing.T) {
		output := "Filesystem                                                          1024-blocks     Used Available Capacity Mounted on\n" +
			"a-very-long-overlay-filesystem-identifier-that-wraps-the-data-row\n" +
			"                                                                       102400000 51200000  20971520      51% /var/lib/docker\n"
		free, total, ok := parseDFDiskBytes(output)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if want := uint64(20971520) * 1024; free != want {
			t.Fatalf("free = %d, want %d", free, want)
		}
		// The wrapped row drops the filesystem field, shifting every column
		// one left — the total still has to be found relative to Capacity.
		if want := uint64(102400000) * 1024; total != want {
			t.Fatalf("total = %d, want %d", total, want)
		}
	})

	t.Run("empty output is inconclusive", func(t *testing.T) {
		if _, _, ok := parseDFDiskBytes(""); ok {
			t.Fatal("expected ok=false for empty output")
		}
	})

	t.Run("malformed data row is inconclusive", func(t *testing.T) {
		output := "Filesystem     1024-blocks     Used Available Capacity Mounted on\nnot enough fields\n"
		if _, _, ok := parseDFDiskBytes(output); ok {
			t.Fatal("expected ok=false for a data row with too few fields")
		}
	})
}

func TestResolveMinDiskHeadroomBytes(t *testing.T) {
	// A filesystem small enough that 10% of it is under the absolute floor.
	const smallDisk uint64 = 100 << 30 // 100 GiB -> 10 GiB proportional
	// A filesystem big enough that 10% of it clears the absolute floor — the
	// shape of the node that evicted every pod while free space still sat
	// above a 20 GiB floor.
	const largeDisk uint64 = 435 << 30 // 435 GiB -> 43.5 GiB proportional

	t.Run("defaults to the absolute floor when total is unknown", func(t *testing.T) {
		t.Setenv(releaseMinDiskHeadroomEnv, "")
		if got := resolveMinDiskHeadroomBytes(0); got != releaseMinDiskHeadroomBytes {
			t.Fatalf("got %d, want default %d", got, releaseMinDiskHeadroomBytes)
		}
	})

	t.Run("keeps the absolute floor when it exceeds the proportional one", func(t *testing.T) {
		t.Setenv(releaseMinDiskHeadroomEnv, "")
		if got := resolveMinDiskHeadroomBytes(smallDisk); got != releaseMinDiskHeadroomBytes {
			t.Fatalf("got %d, want the absolute floor %d", got, releaseMinDiskHeadroomBytes)
		}
	})

	t.Run("scales past the absolute floor on a large disk", func(t *testing.T) {
		t.Setenv(releaseMinDiskHeadroomEnv, "")
		want := largeDisk / 100 * minDiskHeadroomPercent
		got := resolveMinDiskHeadroomBytes(largeDisk)
		if got != want {
			t.Fatalf("got %d, want proportional %d", got, want)
		}
		if got <= releaseMinDiskHeadroomBytes {
			t.Fatalf("proportional floor %d should exceed the absolute floor %d", got, releaseMinDiskHeadroomBytes)
		}
	})

	// The whole point of the proportional floor: kubelet's evictionHard for
	// nodefs defaults to 5% available, so a floor at or below that line can
	// only prune after eviction has already begun.
	t.Run("clears kubelet's 5 percent eviction threshold", func(t *testing.T) {
		t.Setenv(releaseMinDiskHeadroomEnv, "")
		evictionThreshold := largeDisk / 100 * 5
		if got := resolveMinDiskHeadroomBytes(largeDisk); got <= evictionThreshold {
			t.Fatalf("floor %d must sit above the eviction threshold %d", got, evictionThreshold)
		}
	})

	t.Run("honors a valid override outright", func(t *testing.T) {
		t.Setenv(releaseMinDiskHeadroomEnv, "1073741824")
		if got := resolveMinDiskHeadroomBytes(largeDisk); got != 1073741824 {
			t.Fatalf("got %d, want 1073741824", got)
		}
	})

	t.Run("falls back to the resolved floor on a malformed override", func(t *testing.T) {
		t.Setenv(releaseMinDiskHeadroomEnv, "not-a-number")
		if got := resolveMinDiskHeadroomBytes(0); got != releaseMinDiskHeadroomBytes {
			t.Fatalf("got %d, want default %d", got, releaseMinDiskHeadroomBytes)
		}
	})
}

func TestFormatGiB(t *testing.T) {
	if got, want := formatGiB(20<<30), "20.0 GiB"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// diskHeadroomRead is one scripted response from a test's fake
// diskHeadroomFreeSpaceFunc, including the filesystem the fake reports having
// taken it on.
type diskHeadroomRead struct {
	free        uint64
	total       uint64
	path        string
	sharedPaths []string
	ok          bool
}

func (r diskHeadroomRead) measurement() diskHeadroomMeasurement {
	return diskHeadroomMeasurement{free: r.free, total: r.total, path: r.path, sharedPaths: r.sharedPaths}
}

// diskHeadroomCase drives one pass of the decision logic. The floor is pinned
// by env in the test body, so free-space figures here are relative to it.
type diskHeadroomCase struct {
	name          string
	policy        diskHeadroomPolicy
	dryRun        bool
	reads         []diskHeadroomRead
	reclaimable   dockerReclaimable
	reclaimableOK bool
	pruneErr      error
	wantErr       bool
	wantErrSubstr string
	// What the remedy a failure names gets wrong is the whole defect, so the
	// assertions are on the rendered message: what it must say, and — when the
	// space is one docker cannot free — what it must not.
	wantErrContains []string
	wantErrExcludes []string
	wantPrune       bool
}

const (
	diskHeadroomTestFloor uint64 = 20 << 30 // 20 GiB
	diskHeadroomBelow            = diskHeadroomTestFloor - (1 << 30)
	diskHeadroomAbove            = diskHeadroomTestFloor + (1 << 30)

	// The docker root as this check normally resolves it, and the environment's
	// own space on it — the two things the reported failure had to tell apart.
	dockerRootPath          = "/var/lib/docker"
	dindDockerRootPath      = "/dind/docker"
	environmentWorkCloneDir = "/home/erun/work"
	environmentCacheDir     = "/home/erun/.cache"
)

func diskHeadroomCases() []diskHeadroomCase {
	return []diskHeadroomCase{
		{
			name:   "free space above floor: no prune",
			policy: releaseDiskHeadroomPolicy,
			reads:  []diskHeadroomRead{{free: diskHeadroomAbove, ok: true}},
		},
		{
			name:        "free space below floor: prune runs, re-check above floor passes",
			policy:      releaseDiskHeadroomPolicy,
			reads:       []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}, {free: diskHeadroomAbove, ok: true}},
			reclaimable: dockerReclaimable{buildCache: 1 << 40, total: 1 << 40}, reclaimableOK: true,
			wantPrune: true,
		},
		{
			name:   "release still below floor after pruning refuses",
			policy: releaseDiskHeadroomPolicy,
			reads: []diskHeadroomRead{
				{free: diskHeadroomBelow, path: dockerRootPath, ok: true},
				{free: diskHeadroomBelow, path: dockerRootPath, ok: true},
			},
			// Unreadable: an unknown figure is not evidence the remedy is inert,
			// so the docker remedy still stands.
			wantPrune:     true,
			wantErr:       true,
			wantErrSubstr: "filling this disk is what evicts the pod running the release",
			wantErrContains: []string{
				"docker system prune, remove unused images",
				dockerRootPath,
			},
		},
		{
			// A build is small enough that proceeding is usually fine, and
			// refusing every build on a full node blocks the work that clears it.
			name:        "build still below floor after pruning warns but proceeds",
			policy:      buildDiskHeadroomPolicy,
			reads:       []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}, {free: diskHeadroomBelow, ok: true}},
			reclaimable: dockerReclaimable{buildCache: 1 << 40, total: 1 << 40}, reclaimableOK: true,
			wantPrune: true,
		},
		{
			name:        "a failed prune is non-fatal but the disk can still refuse afterward",
			policy:      releaseDiskHeadroomPolicy,
			reads:       []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}, {free: diskHeadroomBelow, ok: true}},
			pruneErr:    errors.New("boom"),
			reclaimable: dockerReclaimable{buildCache: 1 << 40, total: 1 << 40}, reclaimableOK: true,
			wantPrune: true,
			wantErr:   true,
		},
		{
			// docker system df understated real reclaimable build cache by 4.4x
			// on a real node: a small-but-nonzero reported figure must not be
			// read as "the prune can't help" — it is only a lower bound, so the
			// prune still runs, and here it turns out to close the gap.
			name:        "reclaimable understated but non-zero: prunes instead of refusing on the stale figure",
			policy:      releaseDiskHeadroomPolicy,
			reads:       []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}, {free: diskHeadroomAbove, ok: true}},
			reclaimable: dockerReclaimable{buildCache: 1 << 20, total: 1 << 20}, reclaimableOK: true,
			wantPrune: true,
		},
		{
			// Zero, unlike a small positive figure, is trusted: there is
			// genuinely nothing a build-cache prune could do, so skip it rather
			// than run a real no-op.
			name:          "reclaimable genuinely zero: declined, not attempted",
			policy:        releaseDiskHeadroomPolicy,
			reads:         []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}},
			reclaimable:   dockerReclaimable{},
			reclaimableOK: true,
			wantPrune:     false,
			wantErr:       true,
			wantErrSubstr: "filling this disk is what evicts the pod running the release",
		},
		{
			// A build declines the same no-op prune but still proceeds.
			name:          "a build declines a genuinely empty prune and proceeds",
			policy:        buildDiskHeadroomPolicy,
			reads:         []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}},
			reclaimable:   dockerReclaimable{},
			reclaimableOK: true,
			wantPrune:     false,
		},
		{
			// An unreadable figure is not a reason to skip the remedy.
			name:      "an unknown reclaimable figure still prunes",
			policy:    releaseDiskHeadroomPolicy,
			reads:     []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}, {free: diskHeadroomAbove, ok: true}},
			wantPrune: true,
		},
		{
			name:   "inconclusive read: skip the check, no prune, no refusal",
			policy: releaseDiskHeadroomPolicy,
			reads:  []diskHeadroomRead{{ok: false}},
		},
		{
			name:   "dry run: neither reads free space nor prunes",
			policy: releaseDiskHeadroomPolicy,
			dryRun: true,
			// Never consumed: dry run must return before the first read.
			reads: []diskHeadroomRead{{free: diskHeadroomBelow, ok: true}},
		},
	}
}

// diskHeadroomRemedyCases are the shortfalls where the remedy, not the
// decision, is what is under test.
func diskHeadroomRemedyCases() []diskHeadroomCase {
	return []diskHeadroomCase{
		// The reported failure: every docker store on the node already reported
		// 0 B reclaimable, so the docker remediation the message named could
		// not free a byte of the space that was actually short — it is held
		// outside docker, on the same filesystem, by the environment's own
		// checkouts and caches. The message must say so instead of sending the
		// operator through the prune it has already measured as a no-op.
		{
			name:   "a shortage docker cannot free drops the docker remedy and names what holds the space",
			policy: releaseDiskHeadroomPolicy,
			reads: []diskHeadroomRead{{
				free:        diskHeadroomBelow,
				path:        dockerRootPath,
				sharedPaths: []string{environmentWorkCloneDir, environmentCacheDir},
				ok:          true,
			}},
			reclaimable:   dockerReclaimable{},
			reclaimableOK: true,
			wantPrune:     false,
			wantErr:       true,
			wantErrContains: []string{
				"nothing left to reclaim there",
				"pruning docker cannot free any of it",
				"held outside docker",
				environmentWorkCloneDir,
				environmentCacheDir,
				dockerRootPath,
				"grow the volume",
			},
			wantErrExcludes: []string{"docker system prune", "remove unused images"},
		},
		{
			// The space docker's wider remedy can still reach: the build-cache
			// prune is a no-op, but unused images are docker's to remove, so the
			// docker remedy still gets named.
			name:   "a shortage docker's stores can still reach keeps the docker remedy",
			policy: releaseDiskHeadroomPolicy,
			reads: []diskHeadroomRead{{
				free: diskHeadroomBelow,
				path: dockerRootPath,
				ok:   true,
			}},
			reclaimable:   dockerReclaimable{buildCache: 0, total: 2 << 30},
			reclaimableOK: true,
			wantPrune:     false,
			wantErr:       true,
			wantErrContains: []string{
				"docker system prune, remove unused images",
				dockerRootPath,
			},
			wantErrExcludes: []string{"held outside docker"},
		},
		{
			// The daemon root is whatever docker reports, not the path this
			// file's default happens to be: the message names the site the read
			// was taken at.
			name:   "the message names the path the read was taken at",
			policy: releaseDiskHeadroomPolicy,
			reads: []diskHeadroomRead{
				{free: diskHeadroomBelow, path: dindDockerRootPath, ok: true},
				{free: diskHeadroomBelow, path: dindDockerRootPath, ok: true},
			},
			reclaimable:     dockerReclaimable{buildCache: 1 << 40, total: 1 << 40},
			reclaimableOK:   true,
			wantPrune:       true,
			wantErr:         true,
			wantErrContains: []string{dindDockerRootPath},
			wantErrExcludes: []string{dockerRootPath},
		},
	}
}

// TestEnsureDiskHeadroomWith drives the decision logic with injected
// fakes instead of a real docker daemon, per erun-common/AGENTS.md's
// dependency-injection-over-globals guidance (mirroring the existing
// GitCommandRunnerFunc-injection shape ensureReleaseBaseBranchUnmoved uses).
func TestEnsureDiskHeadroomWith(t *testing.T) {
	cases := append(diskHeadroomCases(), diskHeadroomRemedyCases()...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runDiskHeadroomCase(t, tc)
		})
	}
}

func runDiskHeadroomCase(t *testing.T, tc diskHeadroomCase) {
	t.Helper()
	t.Setenv(releaseMinDiskHeadroomEnv, strconv.FormatUint(diskHeadroomTestFloor, 10))

	wantReads := 0
	if !tc.dryRun {
		wantReads = len(tc.reads)
	}

	readCalls := 0
	readFree := func(diskHeadroomTimeouts) (diskHeadroomMeasurement, error) {
		read := tc.reads[readCalls]
		readCalls++
		if !read.ok {
			return read.measurement(), errors.New("free disk space is unreadable")
		}
		return read.measurement(), nil
	}
	readReclaimable := func(time.Duration) (dockerReclaimable, error) {
		if !tc.reclaimableOK {
			return dockerReclaimable{}, errors.New("docker system df is unreadable")
		}
		return tc.reclaimable, nil
	}
	pruneCalls := 0
	var prunedTo uint64
	prune := func(target uint64, _ time.Duration) error {
		pruneCalls++
		prunedTo = target
		return tc.pruneErr
	}

	err := ensureDiskHeadroomWith(Context{DryRun: tc.dryRun}, tc.policy, readFree, readReclaimable, prune)
	assertDiskHeadroomMessage(t, tc, err)

	if gotPrune := pruneCalls > 0; gotPrune != tc.wantPrune {
		t.Fatalf("prune called = %v, want %v (calls=%d)", gotPrune, tc.wantPrune, pruneCalls)
	}
	if tc.wantPrune && prunedTo != diskHeadroomTestFloor {
		t.Fatalf("expected the prune bounded to the floor (%d), got %d", diskHeadroomTestFloor, prunedTo)
	}
	if readCalls != wantReads {
		t.Fatalf("expected %d free-space reads, got %d", wantReads, readCalls)
	}
}

// assertDiskHeadroomMessage checks the rendered verdict against what the case
// expects it to say — and, for space docker was measured unable to free, what
// it must not say.
func assertDiskHeadroomMessage(t *testing.T, tc diskHeadroomCase, err error) {
	t.Helper()
	if tc.wantErr != (err != nil) {
		t.Fatalf("error = %v, wantErr = %v", err, tc.wantErr)
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	if tc.wantErrSubstr != "" && !strings.Contains(message, tc.wantErrSubstr) {
		t.Fatalf("expected error to contain %q, got %v", tc.wantErrSubstr, err)
	}
	for _, want := range tc.wantErrContains {
		if !strings.Contains(message, want) {
			t.Fatalf("expected the message to contain %q, got %q", want, message)
		}
	}
	for _, unwanted := range tc.wantErrExcludes {
		if strings.Contains(message, unwanted) {
			t.Fatalf("expected the message not to contain %q, got %q", unwanted, message)
		}
	}
}

// TestDiskHeadroomRemedyFollowsTheMeasurement pins the remedy itself, for both
// callers: a build renders the same derived remedy into its warning that a
// release renders into its refusal, so neither can name a remediation the
// measurement has already ruled out.
func TestDiskHeadroomRemedyFollowsTheMeasurement(t *testing.T) {
	const (
		free  = diskHeadroomBelow
		floor = diskHeadroomTestFloor
	)
	dockerCanStillFree := diskHeadroomShortfall{
		free: free, floor: floor, path: dockerRootPath,
		dockerReclaimExhausted: false,
	}
	if got := dockerCanStillFree.remedy(); !strings.Contains(got, "docker system prune") {
		t.Fatalf("a shortfall docker can still reach must name the docker remedy, got %q", got)
	}

	dockerHasNothingToFree := diskHeadroomShortfall{
		free: free, floor: floor, path: dockerRootPath,
		sharedPaths:            []string{environmentWorkCloneDir},
		dockerReclaimExhausted: true,
	}
	got := dockerHasNothingToFree.remedy()
	if strings.Contains(got, "docker system prune") || strings.Contains(got, "remove unused images") {
		t.Fatalf("a shortfall docker cannot free must not name a docker remedy, got %q", got)
	}
	for _, want := range []string{"pruning docker cannot free any of it", environmentWorkCloneDir, "grow the volume"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected the remedy to contain %q, got %q", want, got)
		}
	}
}

// diskHeadroomShortLimits is the production check's wall-clock bounds shrunk
// to what a test can wait out. The scenario is unchanged — a docker that stops
// answering — so the only thing the shorter bound changes is how long the
// regression takes to prove itself, not what it proves.
func diskHeadroomShortLimits() diskHeadroomTimeouts {
	const short = 500 * time.Millisecond
	return diskHeadroomTimeouts{read: short, probe: short, prune: short}
}

// headroomTestBound is how long a test waits for the preflight before calling
// it non-terminating. It is deliberately far above the short limits, so a
// loaded machine cannot turn a bounded return into a reported hang.
const headroomTestBound = 30 * time.Second

// awaitHeadroomPreflight runs one check and fails the test if it is still
// running at headroomTestBound, which is the reported failure: a preflight
// that neither lets the build proceed nor fails it, and so writes no terminal
// record for a gate to report.
func awaitHeadroomPreflight(t *testing.T, ctx Context, policy diskHeadroomPolicy) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- ensureDiskHeadroomWith(ctx, policy, dockerRootDiskBytes, dockerReclaimableBytes, runDiskHeadroomPrune)
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
	t.Setenv(releaseMinDiskHeadroomEnv, strconv.FormatUint(diskHeadroomTestFloor, 10))

	logs := &strings.Builder{}
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, logs, logs)}
	policy := buildDiskHeadroomPolicy
	policy.limits = diskHeadroomShortLimits()

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

// TestDiskHeadroomPruneBoundStillReportsAVerdict covers the other half of the
// prune path: the daemon answers every read and goes silent on the prune
// itself. The prune is not what the build depends on, so the build must still
// proceed — but it must proceed having said the prune did not complete, and
// still render the shortfall against the disk it actually has. A prune that
// was announced and never ran, followed by a build that reports nothing, is
// the shape where the mechanism goes inert without anyone able to tell.
func TestDiskHeadroomPruneBoundStillReportsAVerdict(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ERUN_DOCKER_BIN", writeExecutableScript(t, `case "$1" in
  info) echo "`+root+`" ;;
  system) echo "Build Cache|40GB" ;;
  builder) sleep 3600 ;;
esac`))
	// 1 GiB free of ~435 GiB: below the floor, so the prune is reached.
	t.Setenv("ERUN_DF_BIN", writeExecutableScript(t, `echo "Filesystem     1024-blocks     Used Available Capacity Mounted on"
echo "/dev/fake       456340275 455291699  1048576     100% `+root+`"`))
	t.Setenv(releaseMinDiskHeadroomEnv, strconv.FormatUint(diskHeadroomTestFloor, 10))

	logs := &strings.Builder{}
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, logs, logs)}
	policy := buildDiskHeadroomPolicy
	policy.limits = diskHeadroomShortLimits()

	err := awaitHeadroomPreflight(t, ctx, policy)
	if err != nil {
		t.Fatalf("a build must still proceed when its prune does not finish, got %v", err)
	}
	message := logs.String()
	if !strings.Contains(message, "the build-cache prune did not complete") {
		t.Fatalf("expected the prune's failure to be reported rather than assumed, got %q", message)
	}
	if !strings.Contains(message, "did not answer within") {
		t.Fatalf("expected the prune bound to be named as the cause, got %q", message)
	}
	if !strings.Contains(message, "below the") {
		t.Fatalf("expected the shortfall to still be reported against the disk that is actually free, got %q", message)
	}
}

// TestDiskHeadroomAbsentExecutableIsNamedNotSpliced covers the third state the
// read has to tell apart, alongside a daemon that answers and one that stops
// answering: no docker at all on PATH. The read must still report why — but in
// its own words, never by splicing the runtime's own "exec: ...: executable
// file not found in $PATH". That string is what any run reaching for an
// undeclared binary produces, so a trace carrying it is indistinguishable from
// a build silently depending on whatever the host happens to have installed,
// which is a different fault from a daemon that is present but unhealthy.
func TestDiskHeadroomAbsentExecutableIsNamedNotSpliced(t *testing.T) {
	// A name that resolves nowhere, so the read reaches a missing binary rather
	// than any real docker the host has installed.
	t.Setenv("ERUN_DOCKER_BIN", "erun-no-such-docker-binary-for-headroom-test")
	t.Setenv(releaseMinDiskHeadroomEnv, strconv.FormatUint(diskHeadroomTestFloor, 10))

	logs := &strings.Builder{}
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, logs, logs)}
	policy := buildDiskHeadroomPolicy
	policy.limits = diskHeadroomShortLimits()

	if err := awaitHeadroomPreflight(t, ctx, policy); err != nil {
		t.Fatalf("a build must still proceed on a disk it could not measure, got %v", err)
	}
	message := logs.String()
	if !strings.Contains(message, "not observable") {
		t.Fatalf("expected the skipped check to say so, got %q", message)
	}
	if !strings.Contains(message, "the executable is not on PATH") {
		t.Fatalf("expected an absent docker to be named as absent, got %q", message)
	}
	if strings.Contains(message, "executable file not found in") {
		t.Fatalf("expected the missing binary's cause to be reported in the check's own words rather than spliced from the runtime, got %q", message)
	}
}

func TestParseDockerSize(t *testing.T) {
	// Shapes taken from real `docker system df --format` output on a build box.
	cases := []struct {
		in   string
		want uint64
		ok   bool
	}{
		{"8.914GB", 8914000000, true},
		{"0B", 0, true},
		{"250.8MB", 250800000, true},
		{"103.4GB", 103400000000, true},
		{"512kB", 512000, true},
		// Some rows carry a trailing percentage; the number is still the size.
		{"77.29GB (100%)", 77290000000, true},
		{"", 0, false},
		{"not-a-size", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseDockerSize(tc.in)
		if ok != tc.ok {
			t.Errorf("parseDockerSize(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("parseDockerSize(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
