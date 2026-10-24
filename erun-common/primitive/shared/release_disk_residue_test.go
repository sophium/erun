package shared

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/gitx"
)

// A refused disk preflight must leave the repository exactly as found: no stamp commit, no tag.

func releaseDiskResidueStubDocker(t *testing.T, root string) string {
	t.Helper()
	return writeExecutableScript(t, `case "$1" in
  info) echo "`+root+`" ;;
  system) echo "Images|1GB" ;;
  buildx) exit 0 ;;
esac`)
}

// refuseReleaseDiskHeadroom reports 1 GiB free under a 20 GiB floor.
func refuseReleaseDiskHeadroom(t *testing.T, root string) {
	t.Helper()
	t.Setenv("ERUN_DOCKER_BIN", releaseDiskResidueStubDocker(t, root))
	t.Setenv("ERUN_DF_BIN", writeExecutableScript(t, `echo "Filesystem     1024-blocks     Used Available Capacity Mounted on"
echo "/dev/fake       456340275 455291699  1048576     100% `+root+`"`))
	t.Setenv(ReleaseMinDiskHeadroomEnv, strconv.FormatUint(diskHeadroomTestFloor, 10))
	// Keep the cache ceiling out of the way so the floor is what refuses.
	t.Setenv(DockerVolumeBytesEnv, "")
}

func gitTagsForTest(t *testing.T, dir string) string {
	t.Helper()
	return gitOutputForTest(t, dir, "tag", "--list")
}

func releaseVersionPathForTest(t *testing.T, repo, content string) string {
	t.Helper()
	path := filepath.Join(repo, "VERSION")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	runGitForTest(t, repo, "add", "VERSION")
	runGitForTest(t, repo, "commit", "-q", "-m", "seed version file")
	return path
}

func TestRefusedReleaseDiskPreflightLeavesNoStampCommitAndNoTag(t *testing.T) {
	if runtime.GOOS == "windows" {
		// dockerRootDiskBytes is a no-op on Windows, so there is no refusal to order against.
		t.Skip("the disk-headroom preflight cannot refuse on Windows, so no refusal exists to order against the stages")
	}

	repo := newAgentJobTestRepo(t)
	const before = "1.0.300\n"
	versionPath := releaseVersionPathForTest(t, repo, before)

	const version = "1.0.301"
	headBefore := gitOutputForTest(t, repo, "rev-parse", "HEAD")
	tagsBefore := gitTagsForTest(t, repo)

	refuseReleaseDiskHeadroom(t, t.TempDir())

	logs := &strings.Builder{}
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, logs, logs)}

	// Only a publisher runs the headroom preflight; Publish must never be reached.
	published := false
	publisher := ReleasePublisher{
		Publish: func(core.Context) error { published = true; return nil },
		Verify:  func(core.Context) error { return nil },
	}

	spec := model.ReleaseSpec{
		ProjectRoot: repo,
		Branch:      "main",
		Version:     version,
		Mode:        model.ReleaseModeStable,
		Stages: []model.ReleaseStage{
			NewReleaseStage(repo, []model.ReleaseFileUpdate{{Path: versionPath, Content: version + "\n"}}, version, model.ReleaseModeStable),
		},
	}

	err := RunClaimedReleaseSpec(ctx, spec, gitx.GitCommandRunner, nil, &publisher)
	if err == nil {
		t.Fatalf("expected the headroom preflight to refuse; the repository now holds tags %q", gitTagsForTest(t, repo))
	}
	if !strings.Contains(err.Error(), "below the") || !strings.Contains(err.Error(), "before retrying") {
		t.Fatalf("the refusal must report the disk shortfall and its remedy, got: %v", err)
	}
	if published {
		t.Fatal("the publish ran despite the refusal")
	}

	assertReleaseLeftNoResidue(t, repo, versionPath, before, headBefore, tagsBefore)

	message := logs.String()
	if !strings.Contains(message, "free of") || !strings.Contains(message, "below the") {
		t.Errorf("expected the trace to name the measured free space against the floor, got %q", message)
	}
	if strings.Contains(message, "stage: ") {
		t.Errorf("no stage may be entered before the refusal, got trace %q", message)
	}
}

func assertReleaseLeftNoResidue(t *testing.T, repo, versionPath, content, headBefore, tagsBefore string) {
	t.Helper()
	if headAfter := gitOutputForTest(t, repo, "rev-parse", "HEAD"); headAfter != headBefore {
		t.Errorf("the refused release moved HEAD from %s to %s; the stamp commit it left is the residue the next attempt fails on", headBefore, headAfter)
	}
	if tagsAfter := gitTagsForTest(t, repo); tagsAfter != tagsBefore {
		t.Errorf("the refused release left a tag behind: tags went from %q to %q", tagsBefore, tagsAfter)
	}
	written, err := os.ReadFile(versionPath)
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	if string(written) != content {
		t.Errorf("the refused release rewrote VERSION to %q, want %q", string(written), content)
	}
}
