package build

import (
	"bytes"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// diskHeadroomPruneStubDocker writes the docker stand-in the two tests below
// drive: every read answers, and the prune itself never returns, so only the
// bound can end it.
//
// The prune branch is `exec`ed so that the sleeping process *is* the one the
// bound fires on. Bounded execution kills the direct child alone --
// os/exec.CommandContext, which diskHeadroomRun builds on -- so a shell holding
// the sleep as a child is killed while the sleep itself is orphaned and runs on
// for the rest of the hour. That leftover is not a private nuisance: a job's
// record is built from what the job left running, so it makes the run that
// spawned it read as abandoned work.
func diskHeadroomPruneStubDocker(t *testing.T, root string) string {
	t.Helper()
	return writeExecutableScript(t, `case "$1" in
  info) echo "`+root+`" ;;
  system) echo "Build Cache|40GB" ;;
  buildx) exec sleep 3600 ;;
esac`)
}

// setDiskHeadroomOneGiBFree installs the df stand-in that puts the disk below
// the floor, which is what makes the read-first check reach the prune at all:
// 1 GiB free of ~435 GiB.
func setDiskHeadroomOneGiBFree(t *testing.T, root string) {
	t.Helper()
	t.Setenv("ERUN_DF_BIN", writeExecutableScript(t, `echo "Filesystem     1024-blocks     Used Available Capacity Mounted on"
echo "/dev/fake       456340275 455291699  1048576     100% `+root+`"`))
	t.Setenv(shared.ReleaseMinDiskHeadroomEnv, strconv.FormatUint(diskHeadroomTestFloor, 10))
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
	t.Setenv("ERUN_DOCKER_BIN", diskHeadroomPruneStubDocker(t, root))
	// 1 GiB free of ~435 GiB: below the floor, so the prune is reached.
	setDiskHeadroomOneGiBFree(t, root)

	logs := &strings.Builder{}
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, logs, logs)}
	policy := buildDiskHeadroomPolicy
	policy.Limits = diskHeadroomShortLimits()

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

// stubProcessesStillAlive returns the pids of every process on this host whose
// own environment still names stubPath: the stand-in a bounded run started, or
// a descendant it left holding the same inherited environment. The stub path is
// a per-test temp file, so a match is this run's own leftover rather than
// whatever else the host happens to be running.
func stubProcessesStillAlive(t *testing.T, stubPath string) []int {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatalf("read the process table from /proc: %v", err)
	}
	needle := []byte(stubPath)
	var alive []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		// This test's own process carries the same ERUN_DOCKER_BIN.
		if err != nil || pid == os.Getpid() {
			continue
		}
		environ, err := os.ReadFile("/proc/" + entry.Name() + "/environ")
		if err != nil {
			// Exited between the listing and the read, or not visible under a
			// hidepid mount. Either way there is nothing to compare.
			continue
		}
		if bytes.Contains(environ, needle) {
			alive = append(alive, pid)
		}
	}
	return alive
}

// TestDiskHeadroomPruneBoundLeavesNothingRunningBehind is the gate-hygiene half
// of the verdict test beside it, and the reproduction of the leak it was
// reported with: the prune's stand-in used to be a shell holding `sleep 3600`
// as a child, and the bound kills the direct child alone. The shell died, the
// sleep was orphaned, and every run left one behind for the rest of the hour.
//
// The leftover reached past its own test. A job's record is built from what the
// job left running, so a gate whose targets all passed still recorded itself as
// abandoned rather than passed -- the signal that exists to catch a job that
// really did leave work running, spent on every clean run, which is what makes
// it ignorable when it is true.
//
// It outlives the gate a second way, measured on a gate run of this package:
// the orphan inherits make's jobserver file descriptors, and `make -jN` then
// blocks in a read on that pipe with every recipe already finished. There the
// gate does not merely record itself as abandoned -- it stops finishing at all
// until someone kills the stray by hand.
func TestDiskHeadroomPruneBoundLeavesNothingRunningBehind(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only Linux exposes a process's own environment under /proc, which is what identifies a survivor as this run's leftover")
	}
	root := t.TempDir()
	stub := diskHeadroomPruneStubDocker(t, root)
	t.Setenv("ERUN_DOCKER_BIN", stub)
	setDiskHeadroomOneGiBFree(t, root)

	logs := &strings.Builder{}
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, logs, logs)}
	policy := buildDiskHeadroomPolicy
	policy.Limits = diskHeadroomShortLimits()

	if err := awaitHeadroomPreflight(t, ctx, policy); err != nil {
		t.Fatalf("a build must still proceed when its prune does not finish, got %v", err)
	}
	if alive := stubProcessesStillAlive(t, stub); len(alive) > 0 {
		t.Fatalf("the prune's stand-in is still running as pid(s) %v after the bound ended it: what the bound kills is not the process that was running, so the leftover outlives the run that started it and is recorded as abandoned work", alive)
	}
}

// TestDiskHeadroomPruneNamesACommandThatAcceptsItsBound covers the prune's own
// argv, which the decision-logic tests above cannot see: they inject the prune
// and so pass no matter which command the real one is built from. That is how
// this check came to be inert — it issued `docker builder prune -f
// --min-free-space <floor>`, and the classic command the docker CLI implements
// accepts only --all, --filter, --force and --keep-storage, so every prune was
// rejected as an unknown flag and reclaimed nothing. The trace above it
// advertised a reclaim anyway, and the shortfall was then measured against a
// disk nothing had moved: the read-first guarding elsewhere in this file held,
// while the protective act it exists to fall back on did not.
//
// The bound is what keeps a prune from reclaiming everything reclaimable, so
// the command and its bound are asserted together — through the
// ERUN_DOCKER_BIN seam, which drives the real command rather than a fake.
func TestDiskHeadroomPruneNamesACommandThatAcceptsItsBound(t *testing.T) {
	root := t.TempDir()
	argvLog := t.TempDir() + "/docker-argv"
	t.Setenv("ERUN_DOCKER_BIN", writeExecutableScript(t, `printf '%s ' "$@" >> `+argvLog+`
printf '\n' >> `+argvLog+`
case "$1" in
  info) echo "`+root+`" ;;
  system) echo "Build Cache|40GB" ;;
esac`))
	// 1 GiB free of ~435 GiB: below the floor, so the prune is reached.
	t.Setenv("ERUN_DF_BIN", writeExecutableScript(t, `echo "Filesystem     1024-blocks     Used Available Capacity Mounted on"
echo "/dev/fake       456340275 455291699  1048576     100% `+root+`"`))
	t.Setenv(shared.ReleaseMinDiskHeadroomEnv, strconv.FormatUint(diskHeadroomTestFloor, 10))

	logs := &strings.Builder{}
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, logs, logs)}
	policy := buildDiskHeadroomPolicy
	policy.Limits = diskHeadroomShortLimits()

	if err := awaitHeadroomPreflight(t, ctx, policy); err != nil {
		t.Fatalf("a build must proceed on a disk it pruned, got %v", err)
	}

	argv := recordedPruneArgv(t, argvLog)
	if len(argv) == 0 {
		t.Fatalf("expected the below-floor check to issue a prune, got docker invocations %q", logs.String())
	}
	if argv[0] != "buildx" || argv[1] != "prune" {
		t.Fatalf("expected the prune to run through the command that accepts --min-free-space (`docker buildx prune`), got %q", argv)
	}
	if argv[2] != "-f" {
		t.Fatalf("expected the prune to be non-interactive, got %q", argv)
	}
	if argv[3] != "--min-free-space" {
		t.Fatalf("expected the prune bounded so it reclaims only down to the floor, got %q", argv)
	}
	if want := strconv.FormatUint(diskHeadroomTestFloor, 10); argv[4] != want {
		t.Fatalf("expected the prune bounded to the %s-byte floor, got %q", want, argv)
	}
}

// recordedPruneArgv returns the argv of the one recorded docker invocation
// that is the build-cache prune. The stub the seam installs records every call
// rather than only the prune, because which command the prune names is exactly
// what is under test.
func recordedPruneArgv(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the recorded docker invocations: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "prune" {
			return fields
		}
	}
	return nil
}

// TestDiskHeadroomPrunesWhenDockerSystemDfReportsAnEmptyBuildCache is the
// reproduction of the reported refusal, driven through the real commands this
// check runs rather than the injected decision fakes.
//
// The store it stands up is the reported one: `docker system df` reports Build
// Cache 0B while its own wider figure advertises 28.09GB of reclaimable images,
// and BuildKit reports 43.37GB of cache it can still free. Those are the same
// bytes, seen twice — a layer a build produced is attributed to Images for as
// long as the image holding it exists, and becomes Build Cache only once that
// image is removed — so the Build Cache column is structurally unable to see
// them on the store this check exists for. Read as "a build-cache prune has
// nothing reclaimable", it skipped the one act that would have freed the
// space, and refused the release over space the cache was holding.
//
// Both halves of that refusal are asserted here: the bounded prune has to be
// attempted whatever that column says — --min-free-space is what makes
// attempting it safe when the cache really is empty — and the message must not
// send the operator to remove images that were measured to free none of it.
func TestDiskHeadroomPrunesWhenDockerSystemDfReportsAnEmptyBuildCache(t *testing.T) {
	root := t.TempDir()
	argvLog := t.TempDir() + "/docker-argv"
	t.Setenv("ERUN_DOCKER_BIN", writeExecutableScript(t, `printf '%s ' "$@" >> `+argvLog+`
printf '\n' >> `+argvLog+`
case "$1 $2" in
  "info -f") echo "`+root+`" ;;
  "system df") echo "Images|28.09GB (100%)"
               echo "Containers|0B"
               echo "Local Volumes|0B"
               echo "Build Cache|0B" ;;
  "buildx du") printf 'Shared:\t24.17GB\nPrivate:\t19.2GB\nReclaimable:\t43.37GB\nTotal:\t43.37GB\n' ;;
esac`))
	// 1 GiB free of ~435 GiB: below the floor, so the prune branch is reached.
	setDiskHeadroomOneGiBFree(t, root)

	logs := &strings.Builder{}
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, logs, logs)}
	policy := shared.ReleaseDiskHeadroomPolicy
	policy.Limits = diskHeadroomShortLimits()

	err := awaitHeadroomPreflight(t, ctx, policy)
	if err == nil {
		t.Fatal("expected the release to still refuse on a disk this prune could not free enough of")
	}
	if argv := recordedPruneArgv(t, argvLog); len(argv) == 0 {
		t.Fatalf("a build cache the size column reports as 0B must still be pruned: it holds the bytes that column cannot see, and skipping it is the reported refusal (docker invocations were %q)", logs.String())
	}
	if message := err.Error(); strings.Contains(message, "remove unused images") {
		t.Fatalf("a shortfall measured to be build cache must not name removing images the operator already removed for nothing, got %q", message)
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
	t.Setenv(shared.ReleaseMinDiskHeadroomEnv, strconv.FormatUint(diskHeadroomTestFloor, 10))

	logs := &strings.Builder{}
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, logs, logs)}
	policy := buildDiskHeadroomPolicy
	policy.Limits = diskHeadroomShortLimits()

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
