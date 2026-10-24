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

// diskHeadroomPruneStubDocker answers every read and never returns from the prune.
// The prune is `exec`ed because the bound kills only the direct child.
func diskHeadroomPruneStubDocker(t *testing.T, root string) string {
	t.Helper()
	return writeExecutableScript(t, `case "$1" in
  info) echo "`+root+`" ;;
  system) echo "Build Cache|40GB" ;;
  buildx) exec sleep 3600 ;;
esac`)
}

func setDiskHeadroomOneGiBFree(t *testing.T, root string) {
	t.Helper()
	t.Setenv("ERUN_DF_BIN", writeExecutableScript(t, `echo "Filesystem     1024-blocks     Used Available Capacity Mounted on"
echo "/dev/fake       456340275 455291699  1048576     100% `+root+`"`))
	t.Setenv(shared.ReleaseMinDiskHeadroomEnv, strconv.FormatUint(diskHeadroomTestFloor, 10))
}

func TestDiskHeadroomPruneBoundStillReportsAVerdict(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ERUN_DOCKER_BIN", diskHeadroomPruneStubDocker(t, root))
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

// stubProcessesStillAlive returns pids whose environment still names stubPath.
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
		if err != nil || pid == os.Getpid() {
			continue
		}
		environ, err := os.ReadFile("/proc/" + entry.Name() + "/environ")
		if err != nil {
			// Exited, or hidden by a hidepid mount.
			continue
		}
		if bytes.Contains(environ, needle) {
			alive = append(alive, pid)
		}
	}
	return alive
}

// A leftover child makes a gate record itself as abandoned and can block `make -jN`
// on the inherited jobserver pipe.
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

// The docker CLI's classic `builder prune` rejects --min-free-space, so the real argv
// is asserted through the ERUN_DOCKER_BIN seam.
func TestDiskHeadroomPruneNamesACommandThatAcceptsItsBound(t *testing.T) {
	root := t.TempDir()
	argvLog := t.TempDir() + "/docker-argv"
	t.Setenv("ERUN_DOCKER_BIN", writeExecutableScript(t, `printf '%s ' "$@" >> `+argvLog+`
printf '\n' >> `+argvLog+`
case "$1" in
  info) echo "`+root+`" ;;
  system) echo "Build Cache|40GB" ;;
esac`))
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

// `docker system df` reports Build Cache 0B while build layers are attributed to
// Images, so the prune must be attempted regardless of that column.
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

// The read must name a missing docker in its own words, not splice the runtime's
// "executable file not found" error.
func TestDiskHeadroomAbsentExecutableIsNamedNotSpliced(t *testing.T) {
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
