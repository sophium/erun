package eruncommon

import (
	"strings"
	"testing"
)

// These are erun#1768's pattern applied to release.go's git-backed helpers:
// git's stderr is already captured (onto exec.ExitError.Stderr for Output(),
// or directly in CombinedOutput()'s own return value), and each caller used
// to discard it in favor of the content-free "exit status N" a bare "%w"
// wrap renders.
func TestGitRemoteTagExistsIncludesGitStderr(t *testing.T) {
	writeGitStub(t, "fatal: 'not-a-remote' does not appear to be a git repository", 128)
	_, err := gitRemoteTagExists(testTraceContext(false), t.TempDir(), "not-a-remote", "v1.0.0")
	if err == nil {
		t.Fatal("expected an error for a failing git invocation")
	}
	if !strings.Contains(err.Error(), "does not appear to be a git repository") {
		t.Fatalf("error must include git's own stderr, got: %v", err)
	}
}

func TestGitResolvedRefIncludesGitOutputForNonExitError(t *testing.T) {
	// gitResolvedRef treats every *exec.ExitError with a nonzero exit code as
	// "ref does not exist" (ok=false, err=nil) by design -- only a non-exit
	// failure (e.g. the git binary itself missing) reaches the bare-error
	// path, so point ERUN_GIT_BIN at a binary that cannot run at all.
	t.Setenv("ERUN_GIT_BIN", t.TempDir()+"/does-not-exist")
	_, ok, err := gitResolvedRef(testTraceContext(false), t.TempDir(), "HEAD")
	if ok {
		t.Fatal("expected ok=false when git could not even be started")
	}
	if err == nil {
		t.Fatal("expected an error when git could not even be started")
	}
}

func TestGitCurrentBranchIncludesGitStderr(t *testing.T) {
	writeGitStub(t, "fatal: not a git repository (or any of the parent directories): .git", 128)
	_, err := GitCurrentBranch(testTraceContext(false), t.TempDir())
	if err == nil {
		t.Fatal("expected an error for a failing git invocation")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("error must include git's own stderr, got: %v", err)
	}
}

func TestGitShortCommitIncludesGitStderr(t *testing.T) {
	writeGitStub(t, "fatal: not a git repository (or any of the parent directories): .git", 128)
	_, err := GitShortCommit(testTraceContext(false), t.TempDir())
	if err == nil {
		t.Fatal("expected an error for a failing git invocation")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("error must include git's own stderr, got: %v", err)
	}
}

func TestGitWorktreeCleanIncludesGitStderr(t *testing.T) {
	writeGitStub(t, "fatal: not a git repository (or any of the parent directories): .git", 128)
	_, err := gitWorktreeClean(testTraceContext(false), t.TempDir())
	if err == nil {
		t.Fatal("expected an error for a failing git invocation")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("error must include git's own stderr, got: %v", err)
	}
}

func TestSyncMarketplaceReleaseSHAIncludesGitStderr(t *testing.T) {
	writeGitStub(t, "fatal: ambiguous argument 'v1.0.0^{}': unknown revision or path not in the working tree.", 128)
	_, _, err := syncMarketplaceReleaseSHA(testTraceContext(false), ReleasePackagingSyncSpec{
		ProjectRoot:     t.TempDir(),
		Version:         "1.0.0",
		MarketplacePath: "marketplace.yaml",
	})
	if err == nil {
		t.Fatal("expected an error for a failing git invocation")
	}
	if !strings.Contains(err.Error(), "ambiguous argument") {
		t.Fatalf("error must include git's own stderr, got: %v", err)
	}
}
