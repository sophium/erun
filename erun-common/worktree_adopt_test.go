package eruncommon

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

type adoptFixture struct {
	legacy string
	claim  string
	output bytes.Buffer
}

// newAdoptFixture stages the home volume and the worktree claim side by side,
// as the runtime chart's init container mounts them.
func newAdoptFixture(t *testing.T) *adoptFixture {
	t.Helper()
	root := t.TempDir()
	f := &adoptFixture{
		legacy: filepath.Join(root, "mnt", "erun-home", "git", "petios"),
		claim:  filepath.Join(root, "mnt", "erun-worktree"),
	}
	mustMkdirAll(t, f.legacy)
	mustMkdirAll(t, f.claim)
	return f
}

// seedLegacyRepo writes what a pre-worktree-volume environment's checkout
// held: a real .git plus tracked and untracked working files.
func (f *adoptFixture) seedLegacyRepo(t *testing.T) {
	t.Helper()
	mustWriteFile(t, filepath.Join(f.legacy, ".git", "HEAD"), "ref: refs/heads/main\n")
	mustMkdirAll(t, filepath.Join(f.legacy, ".git", "refs"))
	mustWriteFile(t, filepath.Join(f.legacy, "src", "main.go"), "package main\n")
	mustWriteFile(t, filepath.Join(f.legacy, ".env.local"), "uncommitted\n")
}

func (f *adoptFixture) adopt(t *testing.T, dryRun bool) WorktreeAdoptResult {
	t.Helper()
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &f.output, &f.output), DryRun: dryRun}
	result, err := AdoptWorktree(ctx, WorktreeAdoptParams{LegacyDir: f.legacy, ClaimDir: f.claim})
	if err != nil {
		t.Fatalf("adoption failed: %v\n%s", err, f.output.String())
	}
	return result
}

func (f *adoptFixture) preserved() string {
	return f.legacy + worktreeAdoptPreservedSuffix
}

func assertExists(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("%s: %v", why, err)
	}
}

func assertAbsent(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Lstat(path); err == nil {
		t.Fatal(why)
	}
}

func assertEmptyDir(t *testing.T, path, why string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("%s: found %d entries", why, len(entries))
	}
}

func TestAdoptWorktreeAdoptsARepositoryAndPreservesTheOriginal(t *testing.T) {
	f := newAdoptFixture(t)
	f.seedLegacyRepo(t)
	result := f.adopt(t, false)
	if result.Outcome != WorktreeAdoptAdopted || result.PreservedDir != f.preserved() {
		t.Fatalf("result = %+v", result)
	}
	assertExists(t, filepath.Join(f.claim, ".git", "HEAD"), "the repository's .git should land on the worktree volume")
	assertExists(t, filepath.Join(f.claim, ".git", "refs"), "empty directories inside .git should land too")
	assertExists(t, filepath.Join(f.claim, "src", "main.go"), "tracked files should land on the worktree volume")
	assertExists(t, filepath.Join(f.claim, ".env.local"), "untracked working files should land on the worktree volume")
	assertAbsent(t, filepath.Join(f.claim, worktreeAdoptStagingName), "the staging dir should be gone once the copy is promoted")
	assertExists(t, filepath.Join(f.preserved(), "src", "main.go"), "the pre-move copy must be preserved, not deleted")
	assertEmptyDir(t, f.legacy, "the mount point should be left in place, empty, after the original moves aside")
	if !strings.Contains(f.output.String(), "worktree-adopt: adopted; the pre-move copy is preserved at "+f.preserved()) {
		t.Fatalf("expected the preserved path to be reported:\n%s", f.output.String())
	}
}

func TestAdoptWorktreeIsIdempotentAcrossBoots(t *testing.T) {
	f := newAdoptFixture(t)
	f.seedLegacyRepo(t)
	f.adopt(t, false)
	mustWriteFile(t, filepath.Join(f.claim, "src", "main.go"), "edited in pod\n")
	if result := f.adopt(t, false); result.Outcome != WorktreeAdoptClaimPopulated {
		t.Fatalf("a later boot should find the claim populated, got %s", result.Outcome)
	}
	if got := readTrimmed(t, filepath.Join(f.claim, "src", "main.go")); got != "edited in pod" {
		t.Fatalf("a later boot must not overwrite work done on the worktree volume, got %q", got)
	}
}

func TestAdoptWorktreeLeavesAPopulatedClaimAndTheLegacyTreeAlone(t *testing.T) {
	f := newAdoptFixture(t)
	f.seedLegacyRepo(t)
	mustWriteFile(t, filepath.Join(f.claim, "already-here.txt"), "live\n")
	if result := f.adopt(t, false); result.Outcome != WorktreeAdoptClaimPopulated {
		t.Fatalf("outcome = %s", result.Outcome)
	}
	assertExists(t, filepath.Join(f.claim, "already-here.txt"), "existing claim contents must survive")
	assertAbsent(t, filepath.Join(f.claim, ".git"), "a populated claim must not be overwritten by the legacy tree")
	assertExists(t, filepath.Join(f.legacy, ".git", "HEAD"), "the legacy tree must stay put when nothing was adopted")
	assertAbsent(t, f.preserved(), "nothing should be set aside when nothing was adopted")
}

func TestAdoptWorktreeStartsANewEnvironmentEmpty(t *testing.T) {
	f := newAdoptFixture(t)
	if err := os.RemoveAll(f.legacy); err != nil {
		t.Fatal(err)
	}
	if result := f.adopt(t, false); result.Outcome != WorktreeAdoptNoLegacyTree {
		t.Fatalf("outcome = %s", result.Outcome)
	}
	assertEmptyDir(t, f.claim, "a new environment's worktree volume should stay empty")
}

func TestAdoptWorktreeLeavesANonRepositoryOnTheHomeVolume(t *testing.T) {
	f := newAdoptFixture(t)
	mustWriteFile(t, filepath.Join(f.legacy, "notes.txt"), "stray\n")
	if result := f.adopt(t, false); result.Outcome != WorktreeAdoptNotARepository {
		t.Fatalf("outcome = %s", result.Outcome)
	}
	assertEmptyDir(t, f.claim, "a non-repository must not be adopted")
	assertExists(t, filepath.Join(f.legacy, "notes.txt"), "a non-repository must be left where it is")
}

func TestAdoptWorktreeIgnoresASymlinkedReleaseTree(t *testing.T) {
	// A sourceless runtime environment's git folder is a symlink at its baked
	// release, never a checkout to take over.
	f := newAdoptFixture(t)
	release := filepath.Join(filepath.Dir(f.claim), "opt-release")
	mustWriteFile(t, filepath.Join(release, "main.tf"), "baked\n")
	mustMkdirAll(t, filepath.Join(release, ".git"))
	if err := os.Remove(f.legacy); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(release, f.legacy); err != nil {
		t.Fatal(err)
	}
	if result := f.adopt(t, false); result.Outcome != WorktreeAdoptNoLegacyTree {
		t.Fatalf("outcome = %s", result.Outcome)
	}
	assertEmptyDir(t, f.claim, "a symlinked release tree must not be adopted onto the worktree volume")
	if info, err := os.Lstat(f.legacy); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink must be left intact")
	}
}

func TestAdoptWorktreeRetriesAfterAnInterruptedCopy(t *testing.T) {
	f := newAdoptFixture(t)
	f.seedLegacyRepo(t)
	mustWriteFile(t, filepath.Join(f.claim, worktreeAdoptStagingName, "src", "main.go"), "half\n")
	if result := f.adopt(t, false); result.Outcome != WorktreeAdoptAdopted {
		t.Fatalf("outcome = %s", result.Outcome)
	}
	assertExists(t, filepath.Join(f.claim, ".git", "HEAD"), "an abandoned staging dir must not block a retry")
	assertAbsent(t, filepath.Join(f.claim, worktreeAdoptStagingName), "the retry should clear the abandoned staging dir")
	if got := readTrimmed(t, filepath.Join(f.claim, "src", "main.go")); got != "package main" {
		t.Fatalf("the retry must copy the legacy tree, not keep the half copy: %q", got)
	}
}

func TestAdoptWorktreeIgnoresLostAndFound(t *testing.T) {
	f := newAdoptFixture(t)
	f.seedLegacyRepo(t)
	mustMkdirAll(t, filepath.Join(f.claim, "lost+found"))
	if result := f.adopt(t, false); result.Outcome != WorktreeAdoptAdopted {
		t.Fatalf("lost+found must not be mistaken for an operator's tree, outcome = %s", result.Outcome)
	}
	assertExists(t, filepath.Join(f.claim, ".git", "HEAD"), "the repository should be adopted beside lost+found")
}

func TestAdoptWorktreeWithoutAStagedClaimDoesNothing(t *testing.T) {
	f := newAdoptFixture(t)
	f.seedLegacyRepo(t)
	if err := os.Remove(f.claim); err != nil {
		t.Fatal(err)
	}
	if result := f.adopt(t, false); result.Outcome != WorktreeAdoptNoClaim {
		t.Fatalf("outcome = %s", result.Outcome)
	}
	assertExists(t, filepath.Join(f.legacy, ".git", "HEAD"), "with no claim the legacy tree stays put")
}

func TestAdoptWorktreeKeepsTheOriginalWhenThePreservedNameIsTaken(t *testing.T) {
	f := newAdoptFixture(t)
	f.seedLegacyRepo(t)
	mustWriteFile(t, filepath.Join(f.preserved(), "older.txt"), "from an earlier adoption\n")
	result := f.adopt(t, false)
	if result.Outcome != WorktreeAdoptAdoptedOriginal || result.PreservedDir != "" {
		t.Fatalf("result = %+v", result)
	}
	assertExists(t, filepath.Join(f.claim, ".git", "HEAD"), "the copy should still be adopted")
	assertExists(t, filepath.Join(f.legacy, ".git", "HEAD"), "the original stays where it is when the preserved name is taken")
	assertExists(t, filepath.Join(f.preserved(), "older.txt"), "an existing preserved copy is never overwritten")
}

func TestAdoptWorktreeDryRunTouchesNeitherVolume(t *testing.T) {
	f := newAdoptFixture(t)
	f.seedLegacyRepo(t)
	result := f.adopt(t, true)
	if result.Outcome != WorktreeAdoptAdopted || !result.DryRun || result.PreservedDir != f.preserved() {
		t.Fatalf("result = %+v", result)
	}
	assertEmptyDir(t, f.claim, "a dry run must not copy onto the worktree volume")
	assertExists(t, filepath.Join(f.legacy, ".git", "HEAD"), "a dry run must not move the legacy tree")
	assertAbsent(t, f.preserved(), "a dry run must not set the original aside")
	if !strings.Contains(f.output.String(), "would set the original aside at "+f.preserved()) {
		t.Fatalf("the dry run should name what it would do:\n%s", f.output.String())
	}
}

func TestAdoptWorktreeRequiresBothDirectories(t *testing.T) {
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &bytes.Buffer{}, &bytes.Buffer{})}
	if _, err := AdoptWorktree(ctx, WorktreeAdoptParams{LegacyDir: "/mnt/erun-home/git/petios"}); err == nil {
		t.Fatal("a missing claim directory must be an error, not a silent no-op")
	}
}

func TestAdoptWorktreeCopyKeepsSymlinksAndTimes(t *testing.T) {
	f := newAdoptFixture(t)
	f.seedLegacyRepo(t)
	script := filepath.Join(f.legacy, "bin", "run.sh")
	mustWriteFile(t, script, "#!/bin/sh\n")
	stamp := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(script, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("bin/run.sh", filepath.Join(f.legacy, "run")); err != nil {
		t.Fatal(err)
	}
	f.adopt(t, false)

	copied, err := os.Stat(filepath.Join(f.claim, "bin", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !copied.ModTime().Equal(stamp) {
		t.Fatalf("modification time = %s, want %s", copied.ModTime(), stamp)
	}
	target, err := os.Readlink(filepath.Join(f.claim, "run"))
	if err != nil || target != "bin/run.sh" {
		t.Fatalf("the symlink should be copied as a symlink, got %q, %v", target, err)
	}
}
