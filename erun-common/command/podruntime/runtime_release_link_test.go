package podruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func newReleaseLinkFixture(t *testing.T) (repo, release string) {
	t.Helper()
	root := t.TempDir()
	release = filepath.Join(root, "release")
	writeAssetFile(t, filepath.Join(release, "terraform-frs", "prod", "main.tf"), "resource \"null_resource\" \"x\" {}\n")
	return filepath.Join(root, "git", "frs"), release
}

func assertLinkedTo(t *testing.T, repo, release string) {
	t.Helper()
	target, err := os.Readlink(repo)
	if err != nil {
		t.Fatalf("expected %s to be a symlink: %v", repo, err)
	}
	if target != release {
		t.Fatalf("link target = %q, want %q", target, release)
	}
}

func TestLinkRuntimeReleaseLinksAFreshRepoDir(t *testing.T) {
	repo, release := newReleaseLinkFixture(t)
	if linked, err := LinkRuntimeRelease(repo, release); err != nil || !linked {
		t.Fatalf("fresh link: linked=%v err=%v", linked, err)
	}
	assertLinkedTo(t, repo, release)
	if _, err := os.Stat(filepath.Join(repo, "terraform-frs", "prod", "main.tf")); err != nil {
		t.Fatalf("baked terraform should be visible through the link: %v", err)
	}
}

func TestLinkRuntimeReleaseReplacesAnEmptyBootStub(t *testing.T) {
	repo, release := newReleaseLinkFixture(t)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := LinkRuntimeRelease(repo, release); err != nil {
		t.Fatal(err)
	}
	assertLinkedTo(t, repo, release)
}

func TestLinkRuntimeReleaseKeepsAPopulatedWorktree(t *testing.T) {
	repo, release := newReleaseLinkFixture(t)
	writeAssetFile(t, filepath.Join(repo, "local.txt"), "live edit\n")
	if linked, err := LinkRuntimeRelease(repo, release); err != nil || linked {
		t.Fatalf("a populated worktree must not be linked: linked=%v err=%v", linked, err)
	}
	assertAssetFile(t, filepath.Join(repo, "local.txt"), "live edit\n")
}

func TestLinkRuntimeReleaseRelinksAnExistingOrStaleLink(t *testing.T) {
	repo, release := newReleaseLinkFixture(t)
	stale := filepath.Join(t.TempDir(), "stale")
	if err := os.MkdirAll(filepath.Dir(repo), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(stale, repo); err != nil {
		t.Fatal(err)
	}
	if _, err := LinkRuntimeRelease(repo, release); err != nil {
		t.Fatal(err)
	}
	assertLinkedTo(t, repo, release)
	if _, err := LinkRuntimeRelease(repo, release); err != nil {
		t.Fatalf("relinking an already-correct link: %v", err)
	}
	assertLinkedTo(t, repo, release)
}

func TestLinkRuntimeReleaseLeavesTheRepoAloneWithNothingBaked(t *testing.T) {
	repo, release := newReleaseLinkFixture(t)
	if err := os.RemoveAll(release); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if linked, err := LinkRuntimeRelease(repo, release); err != nil || linked {
		t.Fatalf("nothing baked must change nothing: linked=%v err=%v", linked, err)
	}
	if info, err := os.Lstat(repo); err != nil || !info.IsDir() {
		t.Fatalf("the repo dir should be left as a directory: %v", err)
	}
}
