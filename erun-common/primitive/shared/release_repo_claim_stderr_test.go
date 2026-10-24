package shared

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
)

// Callers must surface git's stderr, not a bare "exit status N".
func TestWriteReleaseRepoClaimBlobIncludesGitStderr(t *testing.T) {
	writeGitStub(t, "fatal: could not write blob object", 128)
	now := time.Now()
	_, err := WriteReleaseRepoClaimBlob(testTraceContext(false), t.TempDir(), "prod",
		model.EnvironmentActivityLeaseHolder{Tenant: "acme"}, now, now)
	if err == nil {
		t.Fatal("expected an error for a failing git invocation")
	}
	if !strings.Contains(err.Error(), "could not write blob object") {
		t.Fatalf("error must include git's own stderr, got: %v", err)
	}
}

func TestLoadReleaseRepoClaimRecordCatFileIncludesGitStderr(t *testing.T) {
	// Fetch succeeds so only cat-file fails.
	dir := t.TempDir()
	script := "#!/bin/sh\ncase \"$*\" in\n  *cat-file*) echo 'fatal: Not a valid object name probe-ref' >&2; exit 128 ;;\n  *) exit 0 ;;\nesac\n"
	path := dir + "/git-stub"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write git stub: %v", err)
	}
	t.Setenv("ERUN_GIT_BIN", path)

	_, err := LoadReleaseRepoClaimRecord(testTraceContext(false), t.TempDir(), "origin", "refs/release-claims/prod", "1.0.0")
	if err == nil {
		t.Fatal("expected an error for a failing git invocation")
	}
	if !strings.Contains(err.Error(), "Not a valid object name") {
		t.Fatalf("error must include git's own stderr, got: %v", err)
	}
}
