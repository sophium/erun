package eruncommon

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestWriteReleaseRepoClaimBlobIncludesGitStderr and
// TestLoadReleaseRepoClaimRecordCatFileIncludesGitStderr are erun#1768's
// pattern applied to the release-claim CAS helpers: git's stderr is already
// captured onto exec.ExitError.Stderr by Output(), and each caller (
// takeReleaseRepoClaim/claimReleaseVersion) used to discard it in favor of
// the content-free "exit status N" a bare "%w" wrap renders.
func TestWriteReleaseRepoClaimBlobIncludesGitStderr(t *testing.T) {
	writeGitStub(t, "fatal: could not write blob object", 128)
	now := time.Now()
	_, err := writeReleaseRepoClaimBlob(testTraceContext(false), t.TempDir(), "prod",
		EnvironmentActivityLeaseHolder{Tenant: "acme"}, now, now)
	if err == nil {
		t.Fatal("expected an error for a failing git invocation")
	}
	if !strings.Contains(err.Error(), "could not write blob object") {
		t.Fatalf("error must include git's own stderr, got: %v", err)
	}
}

func TestLoadReleaseRepoClaimRecordCatFileIncludesGitStderr(t *testing.T) {
	// The fetch itself must succeed so the failure is isolated to the
	// cat-file read; the stub answers every git invocation with the same
	// script, so make fetch a no-op success and only cat-file fail.
	dir := t.TempDir()
	script := "#!/bin/sh\ncase \"$*\" in\n  *cat-file*) echo 'fatal: Not a valid object name probe-ref' >&2; exit 128 ;;\n  *) exit 0 ;;\nesac\n"
	path := dir + "/git-stub"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write git stub: %v", err)
	}
	t.Setenv("ERUN_GIT_BIN", path)

	_, err := loadReleaseRepoClaimRecord(testTraceContext(false), t.TempDir(), "origin", "refs/release-claims/prod", "1.0.0")
	if err == nil {
		t.Fatal("expected an error for a failing git invocation")
	}
	if !strings.Contains(err.Error(), "Not a valid object name") {
		t.Fatalf("error must include git's own stderr, got: %v", err)
	}
}
