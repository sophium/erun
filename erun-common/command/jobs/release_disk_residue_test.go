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

// A release refused for disk headroom must leave the repository exactly as it
// was found: no version files, stamp commit, or tag.

// releaseDiskResidueStubDocker answers every headroom read, including buildx.
func releaseDiskResidueStubDocker(t *testing.T, root string) string {
	t.Helper()
	return writeExecutableScript(t, `case "$1" in
  info) echo "`+root+`" ;;
  system) echo "Images|1GB" ;;
  buildx) exit 0 ;;
esac`)
}

// refuseReleaseDiskHeadroom reports 1 GiB free under a 20 GiB floor for every df read.
func refuseReleaseDiskHeadroom(t *testing.T, root string) {
	t.Helper()
	t.Setenv("ERUN_DOCKER_BIN", releaseDiskResidueStubDocker(t, root))
	t.Setenv("ERUN_DF_BIN", writeExecutableScript(t, `echo "Filesystem     1024-blocks     Used Available Capacity Mounted on"
echo "/dev/fake       456340275 455291699  1048576     100% `+root+`"`))
	t.Setenv(shared.ReleaseMinDiskHeadroomEnv, strconv.FormatUint(diskHeadroomTestFloor, 10))
	// Leave the cache volume unset so the floor is what refuses.
	t.Setenv(shared.DockerVolumeBytesEnv, "")
}

func gitTagsForTest(t *testing.T, dir string) string {
	t.Helper()
	return gitOutputForTest(t, dir, "tag", "--list")
}

// With the preflight refusing, no release stage is entered; the no-publisher
// control still reaches its stage.
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
