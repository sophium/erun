package jobs

import (
	"io"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// The reproduction this closes: `erun build --release` ran its
// disk-headroom preflight immediately before the publish, which is *after* the
// "release" stage has written the version files, committed the stamp and
// created the annotated tag. A run refused for space therefore exited having
// left all three behind, unpushed — and the next attempt reported
//
//	release tag "vX" already exists at <sha>, expected current HEAD <sha>
//
// instead of the disk that was still too short. The remedy that message names
// ("delete it with `git tag -d vX` to retry") then reapplied to a symptom, one
// fresh tag per attempt, for as long as the node stayed full.
//
// These tests drive the real release entrypoint against a real git repository
// and a docker/df stand-in that makes the headroom read refuse, so the claim
// under test is the one that matters: after the refusal the repository is
// exactly as it was found.

// releaseDiskResidueStubDocker is a docker stand-in whose every headroom read
// answers. The `buildx` branch covers both the prune and the post-prune cache
// reading that follows it.
func releaseDiskResidueStubDocker(t *testing.T, root string) string {
	t.Helper()
	return writeExecutableScript(t, `case "$1" in
  info) echo "`+root+`" ;;
  system) echo "Images|1GB" ;;
  buildx) exit 0 ;;
esac`)
}

// refuseReleaseDiskHeadroom points the headroom reads at a filesystem with 1 GiB
// free under a 20 GiB floor, which is the measured shortage the preflight
// refuses on. The df stand-in answers every `df -Pk <path>` the check makes,
// including the mount-point comparisons it uses to name co-located space.
func refuseReleaseDiskHeadroom(t *testing.T, root string) {
	t.Helper()
	t.Setenv("ERUN_DOCKER_BIN", releaseDiskResidueStubDocker(t, root))
	t.Setenv("ERUN_DF_BIN", writeExecutableScript(t, `echo "Filesystem     1024-blocks     Used Available Capacity Mounted on"
echo "/dev/fake       456340275 455291699  1048576     100% `+root+`"`))
	t.Setenv(shared.ReleaseMinDiskHeadroomEnv, strconv.FormatUint(diskHeadroomTestFloor, 10))
	// The cache ceiling above the floor is a separate reading. Leaving the
	// volume unset keeps it out of the way so the floor is what refuses.
	t.Setenv(shared.DockerVolumeBytesEnv, "")
}

// gitTagsForTest lists the repository's tags, one per line, or "" when it has
// none — the before/after comparison for "no new tag".
func gitTagsForTest(t *testing.T, dir string) string {
	t.Helper()
	return gitOutputForTest(t, dir, "tag", "--list")
}

// TestReleaseDiskPreflightRunsBeforeTheReleaseStageIsEntered states the ordering
// where the test above states its effect: with the preflight refusing, no stage
// of the release is entered at all. The same graph with no publisher still
// reaches its stage — that is the control, and it is what shows this is about
// ordering rather than about the stage having gone missing from the fixture.
func TestReleaseDiskPreflightRunsBeforeTheReleaseStageIsEntered(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the disk-headroom preflight cannot refuse on Windows, so no refusal exists to order against the stages")
	}

	repo := newAgentJobTestRepo(t)
	refuseReleaseDiskHeadroom(t, t.TempDir())

	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, io.Discard, io.Discard)}
	spec := shared.ReleaseSpec{
		ProjectRoot: repo,
		Branch:      "main",
		Version:     "1.0.301",
		Mode:        shared.ReleaseModeStable,
		Stages: []shared.ReleaseStage{
			shared.NewReleaseStage(repo, nil, "1.0.301", shared.ReleaseModeStable),
		},
	}

	if err := shared.RunClaimedReleaseSpec(ctx, spec, gitx.GitCommandRunner, nil, nil); err != nil {
		t.Fatalf("a release with no publisher marks source control only and must not be refused for disk, got %v", err)
	}
	if tags := gitTagsForTest(t, repo); !strings.Contains(tags, "v1.0.301") {
		t.Fatalf("the control run reached no stage: expected the release tag, got tags %q", tags)
	}
	runGitForTest(t, repo, "tag", "-d", "v1.0.301")

	publisher := shared.ReleasePublisher{
		Publish: func(core.Context) error { t.Fatal("the publish ran despite the refusal"); return nil },
		Verify:  func(core.Context) error { return nil },
	}
	if err := shared.RunClaimedReleaseSpec(ctx, spec, gitx.GitCommandRunner, nil, &publisher); err == nil {
		t.Fatal("expected the disk-headroom preflight to refuse a build-and-publish release")
	}
	if tags := gitTagsForTest(t, repo); tags != "" {
		t.Fatalf("the release stage ran before the refusal: expected no tags, got %q", tags)
	}
}

const diskHeadroomTestFloor uint64 = 20 << 30 // 20 GiB

// gitOutputForTest returns git's trimmed stdout, so a scenario asserts the
// state it actually reached rather than assuming it.
func gitOutputForTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}
