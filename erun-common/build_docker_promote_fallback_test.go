package eruncommon

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newFakeDockerOnPath puts a fake `docker` executable ahead of PATH so these
// tests exercise the real promote/build/push call graph in
// build_docker_commands.go without a real daemon or registry. `docker push`
// fails with pushFailureMessage the first time a given tag is pushed and
// succeeds on any later push of that same tag (recording state under a temp
// dir keyed by env var so both this process and the fake script agree on it);
// every other subcommand (build, tag, manifest) succeeds unconditionally.
func newFakeDockerOnPath(t *testing.T, pushFailureMessage string) {
	t.Helper()
	binDir := t.TempDir()
	stateDir := t.TempDir()
	script := "#!/bin/bash\n" +
		"case \"$1\" in\n" +
		"  push)\n" +
		"    tag=\"${@: -1}\"\n" +
		"    safe=$(echo \"$tag\" | tr '/:.' '_')\n" +
		"    marker=\"" + stateDir + "/pushed_$safe\"\n" +
		"    if [ ! -f \"$marker\" ]; then\n" +
		"      touch \"$marker\"\n" +
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
}

func testPromoteBuildInput() DockerBuildSpec {
	return DockerBuildSpec{
		ContextDir:     "/tmp",
		DockerfilePath: "Dockerfile",
		Image: DockerImageReference{
			Registry:  "ghcr.io/sophium",
			ImageName: "erun-console",
			Tag:       "ghcr.io/sophium/erun-console:1.0.246",
		},
		Platforms:   []string{"linux/amd64"},
		Push:        true,
		Fingerprint: "abc123",
		Promote:     true,
	}
}

// TestPromoteDockerImageFallsBackToARealBuildOnUnknownBlob reproduces the
// erun#1921 symptom: a promote's push is rejected because the registry
// doesn't hold a blob docker's cache believed was already there. The fix
// treats that as proof the cache hit cannot be trusted this run and rebuilds
// the platform from source instead of failing the release.
func TestPromoteDockerImageFallsBackToARealBuildOnUnknownBlob(t *testing.T) {
	newFakeDockerOnPath(t, "unknown blob")

	var stdout, stderr bytes.Buffer
	if err := DockerImageBuilder(testPromoteBuildInput(), &stdout, &stderr); err != nil {
		t.Fatalf("expected the fallback rebuild to recover, got: %v", err)
	}
	if !strings.Contains(stderr.String(), "rebuilding from source") {
		t.Errorf("expected a rebuild note naming the fallback, got: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "ghcr.io/sophium/erun-console:1.0.246-amd64") {
		t.Errorf("expected the fallback note to name the promoted tag, got: %s", stderr.String())
	}
}

// TestPromoteDockerImageNamesTheTagOnANonBlobPushFailure guards the other
// half of the fix: a failure that a rebuild could not fix (anything other
// than the registry-missing-a-blob shape) is not silently retried, and the
// error it returns names the image and the promote operation instead of a
// bare daemon message.
func TestPromoteDockerImageNamesTheTagOnANonBlobPushFailure(t *testing.T) {
	newFakeDockerOnPath(t, "internal server error")

	var stdout, stderr bytes.Buffer
	err := DockerImageBuilder(testPromoteBuildInput(), &stdout, &stderr)
	if err == nil {
		t.Fatal("expected the promote to fail without retrying")
	}
	if !strings.Contains(err.Error(), "ghcr.io/sophium/erun-console:1.0.246-amd64") {
		t.Errorf("expected the error to name the promoted tag, got: %v", err)
	}
	if !strings.Contains(err.Error(), "fp-abc123-amd64") {
		t.Errorf("expected the error to name the fingerprint source it promoted from, got: %v", err)
	}
	if strings.Contains(stderr.String(), "rebuilding from source") {
		t.Errorf("a non-blob failure must not trigger the rebuild fallback, got: %s", stderr.String())
	}
}
