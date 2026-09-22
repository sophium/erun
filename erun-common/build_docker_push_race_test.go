package eruncommon

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A real per-process concurrency test is not practical here: the race needs two
// `erun build --release` runs pushing overlapping layers to one live registry,
// and the loser's failure is decided by the registry's own blob-commit timing,
// which no unit-level harness can schedule. What is testable — and what the fix
// actually decides — is the retry predicate: which push failures are re-pushed,
// how many times, and what a failure that is not this race does instead. The
// fake docker below models the observable precondition the race produces (a
// push rejected with "unknown blob" while a peer's upload commits, then
// accepted once it has) rather than asserting nothing.

// newCountingFakeDockerOnPath puts a fake `docker` ahead of PATH whose `docker
// push` fails with pushFailureMessage for the first failuresPerTag pushes of a
// given tag and succeeds afterwards, recording every attempted tag in
// attemptedPath. Every other subcommand succeeds unconditionally.
func newCountingFakeDockerOnPath(t *testing.T, pushFailureMessage string, failuresPerTag int) (attemptedPath string) {
	t.Helper()
	binDir := t.TempDir()
	stateDir := t.TempDir()
	attemptedPath = filepath.Join(t.TempDir(), "attempted")
	script := "#!/bin/bash\n" +
		"case \"$1\" in\n" +
		"  push)\n" +
		"    tag=\"${@: -1}\"\n" +
		"    echo \"$tag\" >> \"" + attemptedPath + "\"\n" +
		"    safe=$(echo \"$tag\" | tr '/:.' '_')\n" +
		"    marker=\"" + stateDir + "/count_$safe\"\n" +
		"    n=0\n" +
		"    if [ -f \"$marker\" ]; then n=$(cat \"$marker\"); fi\n" +
		"    n=$((n+1))\n" +
		"    echo \"$n\" > \"$marker\"\n" +
		"    if [ \"$n\" -le " + strconv.Itoa(failuresPerTag) + " ]; then\n" +
		"      echo \"" + pushFailureMessage + "\" >&2\n" +
		"      exit 1\n" +
		"    fi\n" +
		"    exit 0\n" +
		"    ;;\n" +
		"  *)\n" +
		"    exit 0\n" +
		"    ;;\n" +
		"esac\n"
	path := filepath.Join(binDir, "docker")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return attemptedPath
}

func countDockerPushAttempts(t *testing.T, attemptedPath, tag string) int {
	t.Helper()
	data, err := os.ReadFile(attemptedPath)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read attempted pushes: %v", err)
	}
	attempts := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == tag {
			attempts++
		}
	}
	return attempts
}

// noWait keeps the retry decision under test without its backoff; the backoff
// is a wall-clock pause on a registry-side condition and is not what the fix
// decides.
func noWait(int) {}

const concurrentPushTestTag = "ghcr.io/sophium/erun-backend-api:1.0.250-pr.4fa33ad5-arm64"

// TestAConcurrentPublishersUnknownBlobIsAbsorbedByRepushing is the case the
// fix exists for: the push is rejected because a peer run's upload of a shared
// layer has not committed, and the identical push succeeds once it has. The
// failure must not reach the release as a build failure.
func TestAConcurrentPublishersUnknownBlobIsAbsorbedByRepushing(t *testing.T) {
	attempted := newCountingFakeDockerOnPath(t, "unknown blob", 1)

	var stdout, stderr bytes.Buffer
	if err := dockerImagePusher(concurrentPushTestTag, 0, &stdout, &stderr, noWait); err != nil {
		t.Fatalf("expected the re-push to absorb the peer's blob race, got: %v", err)
	}
	if got := countDockerPushAttempts(t, attempted, concurrentPushTestTag); got != 2 {
		t.Errorf("expected the push to be re-run once, got %d attempts", got)
	}
	if !strings.Contains(stderr.String(), "re-pushing (1/2)") {
		t.Errorf("expected the retry to be reported and bounded, got: %s", stderr.String())
	}
}

// TestTheUnknownBlobRetryIsBoundedAndStillFails guards the other half: a blob
// rejection that re-pushing does not clear is a real failure, reported after a
// bounded number of attempts rather than retried indefinitely.
func TestTheUnknownBlobRetryIsBoundedAndStillFails(t *testing.T) {
	attempted := newCountingFakeDockerOnPath(t, "unknown blob", 99)

	var stdout, stderr bytes.Buffer
	err := dockerImagePusher(concurrentPushTestTag, 0, &stdout, &stderr, noWait)
	if err == nil {
		t.Fatal("expected a blob rejection that never clears to fail")
	}
	if !IsDockerUnknownBlobError(err.Error()) {
		t.Errorf("expected the original blob failure to surface, got: %v", err)
	}
	if !strings.Contains(err.Error(), concurrentPushTestTag) {
		t.Errorf("expected the error to name the pushed image, got: %v", err)
	}
	want := dockerPushUnknownBlobRetries + 1
	if got := countDockerPushAttempts(t, attempted, concurrentPushTestTag); got != want {
		t.Errorf("expected exactly %d attempts (1 initial + %d retries), got %d", want, dockerPushUnknownBlobRetries, got)
	}
}

// TestANonBlobPushFailureIsNotRetried keeps the retry orthogonal to genuine
// errors: only the registry-refused-a-blob-it-lacks shape is transient, so a
// server-side failure must surface on its first occurrence with no extra push
// and no retry note.
func TestANonBlobPushFailureIsNotRetried(t *testing.T) {
	attempted := newCountingFakeDockerOnPath(t, "internal server error", 99)

	var stdout, stderr bytes.Buffer
	err := dockerImagePusher(concurrentPushTestTag, 0, &stdout, &stderr, noWait)
	if err == nil {
		t.Fatal("expected the push to fail")
	}
	if !strings.Contains(err.Error(), "internal server error") {
		t.Errorf("expected the daemon's own failure to surface, got: %v", err)
	}
	if got := countDockerPushAttempts(t, attempted, concurrentPushTestTag); got != 1 {
		t.Errorf("expected a non-blob failure to be pushed exactly once, got %d attempts", got)
	}
	if strings.Contains(stderr.String(), "re-pushing") {
		t.Errorf("a non-blob failure must not trigger the blob retry, got: %s", stderr.String())
	}
}
