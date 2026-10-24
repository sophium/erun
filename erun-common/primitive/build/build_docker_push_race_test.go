package build

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/primitive/shared"
)

// newCountingFakeDockerOnPath fails the first failuresPerTag pushes of each tag.
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

func noWait(int) {}

const concurrentPushTestTag = "ghcr.io/sophium/erun-backend-api:1.0.250-pr.4fa33ad5-arm64"

func TestTheUnknownBlobRetryIsBoundedAndStillFails(t *testing.T) {
	attempted := newCountingFakeDockerOnPath(t, "unknown blob", 99)

	var stdout, stderr bytes.Buffer
	err := shared.PushDockerImageWithRetries(concurrentPushTestTag, 0, &stdout, &stderr, noWait)
	if err == nil {
		t.Fatal("expected a blob rejection that never clears to fail")
	}
	if !shared.IsDockerUnknownBlobError(err.Error()) {
		t.Errorf("expected the original blob failure to surface, got: %v", err)
	}
	if !strings.Contains(err.Error(), concurrentPushTestTag) {
		t.Errorf("expected the error to name the pushed image, got: %v", err)
	}
	want := shared.DockerPushUnknownBlobRetries + 1
	if got := countDockerPushAttempts(t, attempted, concurrentPushTestTag); got != want {
		t.Errorf("expected exactly %d attempts (1 initial + %d retries), got %d", want, shared.DockerPushUnknownBlobRetries, got)
	}
}

func TestANonBlobPushFailureIsNotRetried(t *testing.T) {
	attempted := newCountingFakeDockerOnPath(t, "internal server error", 99)

	var stdout, stderr bytes.Buffer
	err := shared.PushDockerImageWithRetries(concurrentPushTestTag, 0, &stdout, &stderr, noWait)
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
