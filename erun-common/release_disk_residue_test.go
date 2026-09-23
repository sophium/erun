package eruncommon

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
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
	t.Setenv(releaseMinDiskHeadroomEnv, strconv.FormatUint(diskHeadroomTestFloor, 10))
	// The cache ceiling above the floor is a separate reading. Leaving the
	// volume unset keeps it out of the way so the floor is what refuses.
	t.Setenv(dockerVolumeBytesEnv, "")
}

// gitTagsForTest lists the repository's tags, one per line, or "" when it has
// none — the before/after comparison for "no new tag".
func gitTagsForTest(t *testing.T, dir string) string {
	t.Helper()
	return gitOutputForTest(t, dir, "tag", "--list")
}

// releaseVersionPathForTest seeds a committed, clean VERSION file the release
// stage has something to rewrite, and returns its path.
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

// TestRefusedReleaseDiskPreflightLeavesNoStampCommitAndNoTag is the
// reproduction. It asserts the repository is exactly as it was found after the
// refusal — same HEAD, same tags, and the version file the release stage would
// have rewritten still holding what it held — because the preflight now runs
// before the release stage rather than after it.
func TestRefusedReleaseDiskPreflightLeavesNoStampCommitAndNoTag(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Not a tool-availability skip: dockerRootDiskBytes is a structural
		// no-op on Windows, so there is no refusal there to order against the
		// release stage. The ordering property is real but unobservable.
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
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, logs, logs)}

	// A publisher is what makes this a build-and-publish release rather than
	// `erun release` marking source control: it is the only caller that runs
	// the headroom preflight at all. Publish must never be reached — a refusal
	// that got as far as the build is a different defect from this one.
	published := false
	publisher := ReleasePublisher{
		Publish: func(Context) error { published = true; return nil },
		Verify:  func(Context) error { return nil },
	}

	spec := ReleaseSpec{
		ProjectRoot: repo,
		Branch:      "main",
		Version:     version,
		Mode:        ReleaseModeStable,
		Stages: []ReleaseStage{
			newReleaseStage(repo, []ReleaseFileUpdate{{Path: versionPath, Content: version + "\n"}}, version, ReleaseModeStable),
		},
	}

	err := runClaimedReleaseSpec(ctx, spec, GitCommandRunner, nil, nil, &publisher)
	if err == nil {
		t.Fatalf("expected the headroom preflight to refuse; the repository now holds tags %q", gitTagsForTest(t, repo))
	}
	if !strings.Contains(err.Error(), "below the") || !strings.Contains(err.Error(), "before retrying") {
		t.Fatalf("the refusal must report the disk shortfall and its remedy, got: %v", err)
	}
	if published {
		t.Fatal("the publish ran despite the refusal")
	}

	// The whole point: the refusal left nothing of the release's own behind.
	assertReleaseLeftNoResidue(t, repo, versionPath, before, headBefore, tagsBefore)

	// The refusal is still the disk's, which is what the operator has to act on
	// rather than a tag collision they would otherwise delete and re-earn.
	message := logs.String()
	if !strings.Contains(message, "free of") || !strings.Contains(message, "below the") {
		t.Errorf("expected the trace to name the measured free space against the floor, got %q", message)
	}
	if strings.Contains(message, "stage: ") {
		t.Errorf("no stage may be entered before the refusal, got trace %q", message)
	}
}

// assertReleaseLeftNoResidue checks the three things the release stage writes —
// the commit, the tag and the version file — against what they held before the
// refused run.
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

	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, io.Discard, io.Discard)}
	spec := ReleaseSpec{
		ProjectRoot: repo,
		Branch:      "main",
		Version:     "1.0.301",
		Mode:        ReleaseModeStable,
		Stages: []ReleaseStage{
			newReleaseStage(repo, nil, "1.0.301", ReleaseModeStable),
		},
	}

	if err := runClaimedReleaseSpec(ctx, spec, GitCommandRunner, nil, nil, nil); err != nil {
		t.Fatalf("a release with no publisher marks source control only and must not be refused for disk, got %v", err)
	}
	if tags := gitTagsForTest(t, repo); !strings.Contains(tags, "v1.0.301") {
		t.Fatalf("the control run reached no stage: expected the release tag, got tags %q", tags)
	}
	runGitForTest(t, repo, "tag", "-d", "v1.0.301")

	publisher := ReleasePublisher{
		Publish: func(Context) error { t.Fatal("the publish ran despite the refusal"); return nil },
		Verify:  func(Context) error { return nil },
	}
	if err := runClaimedReleaseSpec(ctx, spec, GitCommandRunner, nil, nil, &publisher); err == nil {
		t.Fatal("expected the disk-headroom preflight to refuse a build-and-publish release")
	}
	if tags := gitTagsForTest(t, repo); tags != "" {
		t.Fatalf("the release stage ran before the refusal: expected no tags, got %q", tags)
	}
}
