package shared

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/gitx"
)

// Callers must surface git's stderr, not a bare "exit status N".
func TestGitRemoteTagExistsIncludesGitStderr(t *testing.T) {
	writeGitStub(t, "fatal: 'not-a-remote' does not appear to be a git repository", 128)
	_, err := GitRemoteTagExists(testTraceContext(false), t.TempDir(), "not-a-remote", "v1.0.0")
	if err == nil {
		t.Fatal("expected an error for a failing git invocation")
	}
	if !strings.Contains(err.Error(), "does not appear to be a git repository") {
		t.Fatalf("error must include git's own stderr, got: %v", err)
	}
}

func TestGitResolvedRefIncludesGitOutputForNonExitError(t *testing.T) {
	// Only a non-exit failure reaches the bare-error path; an exit error means "no such ref".
	t.Setenv("ERUN_GIT_BIN", t.TempDir()+"/does-not-exist")
	_, ok, err := GitResolvedRef(testTraceContext(false), t.TempDir(), "HEAD")
	if ok {
		t.Fatal("expected ok=false when git could not even be started")
	}
	if err == nil {
		t.Fatal("expected an error when git could not even be started")
	}
}

func TestGitCurrentBranchIncludesGitStderr(t *testing.T) {
	writeGitStub(t, "fatal: not a git repository (or any of the parent directories): .git", 128)
	_, err := gitx.GitCurrentBranch(testTraceContext(false), t.TempDir())
	if err == nil {
		t.Fatal("expected an error for a failing git invocation")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("error must include git's own stderr, got: %v", err)
	}
}

func TestGitShortCommitIncludesGitStderr(t *testing.T) {
	writeGitStub(t, "fatal: not a git repository (or any of the parent directories): .git", 128)
	_, err := gitx.GitShortCommit(testTraceContext(false), t.TempDir())
	if err == nil {
		t.Fatal("expected an error for a failing git invocation")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("error must include git's own stderr, got: %v", err)
	}
}

func TestGitWorktreeCleanIncludesGitStderr(t *testing.T) {
	writeGitStub(t, "fatal: not a git repository (or any of the parent directories): .git", 128)
	_, err := GitWorktreeClean(testTraceContext(false), t.TempDir())
	if err == nil {
		t.Fatal("expected an error for a failing git invocation")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("error must include git's own stderr, got: %v", err)
	}
}

func TestSyncMarketplaceReleaseSHAIncludesGitStderr(t *testing.T) {
	writeGitStub(t, "fatal: ambiguous argument 'v1.0.0^{}': unknown revision or path not in the working tree.", 128)
	_, _, err := SyncMarketplaceReleaseSHA(testTraceContext(false), model.ReleasePackagingSyncSpec{
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
