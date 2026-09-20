package eruncommon

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// newFakeDockerOnPath puts a fake `docker` executable ahead of PATH so these
// tests exercise the real promote/build/push call graph in
// build_docker_commands.go without a real daemon or registry. `docker push`
// fails with pushFailureMessage for the first dockerPushUnknownBlobRetries+1
// pushes of a given tag and succeeds on any later push of that same tag
// (recording state under a temp dir so both this process and the fake script
// agree on it); every other subcommand (build, tag, manifest) succeeds
// unconditionally.
//
// The count matters: DockerImagePusher already re-pushes a blob rejection
// (see build_docker_push_race_test.go), so the failure has to outlast that
// bounded retry for these tests to reach the promote fallback underneath it —
// which is the point, since a rebuild is the recovery a stale local
// "already pushed" record needs and a re-push cannot provide.
func newFakeDockerOnPath(t *testing.T, pushFailureMessage string) {
	t.Helper()
	binDir := t.TempDir()
	stateDir := t.TempDir()
	failuresPerTag := dockerPushUnknownBlobRetries + 1
	script := "#!/bin/bash\n" +
		"case \"$1\" in\n" +
		"  push)\n" +
		"    tag=\"${@: -1}\"\n" +
		"    safe=$(echo \"$tag\" | tr '/:.' '_')\n" +
		"    marker=\"" + stateDir + "/pushed_$safe\"\n" +
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
}

// newFakeDockerImageStore puts a fake `docker` executable ahead of PATH that
// models a local image store holding exactly the tags in present: `docker image
// inspect` succeeds only for those, `docker tag` fails with the daemon's own
// "No such image" for any other source, and every other subcommand (build,
// push, manifest) succeeds. Every invocation is appended to invocations.log in
// the returned state dir, so a test can tell a promote from a rebuild.
func newFakeDockerImageStore(t *testing.T, present ...string) (invocations func() string) {
	t.Helper()
	binDir := t.TempDir()
	stateDir := t.TempDir()
	var seed strings.Builder
	for _, tag := range present {
		fmt.Fprintf(&seed, "touch \"$store/%s\"\n", fakeDockerStoreKey(tag))
	}
	script := "#!/bin/bash\n" +
		"store=\"" + stateDir + "\"\n" +
		"key() { echo \"$1\" | tr '/:.' '_'; }\n" +
		"has() { [ -f \"$store/$(key \"$1\")\" ]; }\n" +
		"mark() { touch \"$store/$(key \"$1\")\"; }\n" +
		seed.String() +
		"printf '%s\\n' \"$*\" >> \"$store/invocations.log\"\n" +
		"case \"$1\" in\n" +
		"  image)\n" +
		"    tag=\"${@: -1}\"\n" +
		"    if [ \"$2\" = \"inspect\" ] && ! has \"$tag\"; then\n" +
		"      echo \"Error: No such image: $tag\" >&2\n" +
		"      exit 1\n" +
		"    fi\n" +
		"    exit 0\n" +
		"    ;;\n" +
		"  tag)\n" +
		"    src=\"$2\"\n" +
		"    if ! has \"$src\"; then\n" +
		"      echo \"Error response from daemon: No such image: $src\" >&2\n" +
		"      exit 1\n" +
		"    fi\n" +
		"    mark \"$3\"\n" +
		"    exit 0\n" +
		"    ;;\n" +
		"  build)\n" +
		"    prev=\"\"\n" +
		"    for arg in \"$@\"; do\n" +
		"      if [ \"$prev\" = \"-t\" ]; then mark \"$arg\"; fi\n" +
		"      prev=\"$arg\"\n" +
		"    done\n" +
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
	return func() string {
		data, err := os.ReadFile(filepath.Join(stateDir, "invocations.log"))
		if err != nil {
			return ""
		}
		return string(data)
	}
}

// fakeDockerStoreKey mirrors the shell fake's key() so both sides agree on which
// file stands for which tag.
func fakeDockerStoreKey(tag string) string {
	return strings.NewReplacer("/", "_", ":", "_", ".", "_").Replace(tag)
}

// TestPromoteDockerImageRebuildsWhenTheCachedFingerprintImageIsGone reproduces
// the reported symptom: the fingerprint check that chose promotion ran before
// every other image in the run built, and by the time the promote runs the
// cached image is no longer in the local store (a disk-floor prune mid-release
// is enough). The fix treats that absence as a cache miss and rebuilds the
// platform from source instead of aborting a release whose other images have
// all built.
func TestPromoteDockerImageRebuildsWhenTheCachedFingerprintImageIsGone(t *testing.T) {
	invocations := newFakeDockerImageStore(t)

	var stdout, stderr bytes.Buffer
	if err := DockerImageBuilder(testPromoteBuildInput(), &stdout, &stderr); err != nil {
		t.Fatalf("expected the missing cached image to rebuild instead of failing, got: %v", err)
	}
	if !strings.Contains(stderr.String(), "is no longer in the local image store") {
		t.Errorf("expected a note naming the missing cached image, got: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "fp-abc123-amd64") {
		t.Errorf("expected the note to name the fingerprint source that was gone, got: %s", stderr.String())
	}
	if !strings.Contains(invocations(), "build --platform linux/amd64") {
		t.Errorf("expected a real build to replace the promote, got invocations:\n%s", invocations())
	}
}

// TestPromoteDockerImageStillPromotesWhenTheCachedFingerprintImageIsPresent
// guards the other half: a cached image that is really there is still promoted,
// with no rebuild behind it.
func TestPromoteDockerImageStillPromotesWhenTheCachedFingerprintImageIsPresent(t *testing.T) {
	invocations := newFakeDockerImageStore(t, "ghcr.io/sophium/erun-console:fp-abc123-amd64")

	var stdout, stderr bytes.Buffer
	if err := DockerImageBuilder(testPromoteBuildInput(), &stdout, &stderr); err != nil {
		t.Fatalf("expected the present cached image to promote, got: %v", err)
	}
	if strings.Contains(stderr.String(), "rebuilding") {
		t.Errorf("a present cached image must not rebuild, got: %s", stderr.String())
	}
	if strings.Contains(invocations(), "build ") {
		t.Errorf("a present cached image must not run docker build, got invocations:\n%s", invocations())
	}
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
// reported symptom: a promote's push is rejected because the registry
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
