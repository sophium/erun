package podruntime

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

type adoptFixture struct {
	legacy string
	claim  string
	output bytes.Buffer
}

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
	result, err := AdoptWorktree(ctx, model.WorktreeAdoptParams{LegacyDir: f.legacy, ClaimDir: f.claim})
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

func TestAdoptWorktreeIgnoresASymlinkedReleaseTree(t *testing.T) {
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
