package build

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerregistry"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

// newFakeDockerOnPath fakes docker on PATH; `docker push` of a tag fails with
// pushFailureMessage until DockerImagePusher's bounded retry is exhausted.
func newFakeDockerOnPath(t *testing.T, pushFailureMessage string) {
	t.Helper()
	binDir := t.TempDir()
	stateDir := t.TempDir()
	failuresPerTag := dockerregistry.DockerPushUnknownBlobRetries + 1
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

// newFakeDockerImageStore fakes a local store holding only present; invocations are logged.
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

// fakeDockerStoreKey must match the shell fake's key().
func fakeDockerStoreKey(tag string) string {
	return strings.NewReplacer("/", "_", ":", "_", ".", "_").Replace(tag)
}

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

func testPromoteBuildInput() dockerspec.DockerBuildSpec {
	return dockerspec.DockerBuildSpec{
		ContextDir:     "/tmp",
		DockerfilePath: "Dockerfile",
		Image: model.DockerImageReference{
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

func TestAPromoteThatRebuiltIsNotReportedAsACacheHit(t *testing.T) {
	newFakeDockerImageStore(t)

	clock := newFakeClock()
	root := core.NewStepTiming("build", clock.now)
	var stdout, stderr bytes.Buffer
	ctx := core.Context{Stdout: &stdout, Stderr: &stderr, Timing: root}

	buildInput := testPromoteBuildInput()
	buildInput.Platforms = []string{"linux/amd64", "linux/arm64"}
	if err := executeDockerBuild(ctx, buildInput, nil, &stdout, &stderr); err != nil {
		t.Fatalf("expected the missing cached image to rebuild instead of failing, got: %v", err)
	}
	root.Finish(nil)

	// One image step plus its two platform children.
	assertCorrectedTimingTable(t, core.RenderStepTimingRows(root, 0), 3)
	assertCorrectedTimingRecord(t, root.ToRecord("build"))
}

func assertCorrectedTimingTable(t *testing.T, rows []string, wantRows int) {
	t.Helper()
	joined := strings.Join(rows, "\n")
	if strings.Contains(joined, "(cache hit)") {
		t.Fatalf("a promote that rebuilt from source must not be reported as a cache hit, got:\n%s", joined)
	}
	if got := strings.Count(joined, "cache miss: "+core.PromoteFallbackMissReason); got != wantRows {
		t.Fatalf("expected the correction on the image row and both platform rows (%d), got %d:\n%s", wantRows, got, joined)
	}
}

func assertCorrectedTimingRecord(t *testing.T, record model.TimingRecord) {
	t.Helper()
	if len(record.Steps) != 1 {
		t.Fatalf("expected one image step in the record, got %d", len(record.Steps))
	}
	image := record.Steps[0]
	if image.CacheHit == nil || *image.CacheHit {
		t.Fatalf("expected the image step's cacheHit to be false, got %v", image.CacheHit)
	}
	if image.CacheMissReason != core.PromoteFallbackMissReason {
		t.Fatalf("expected the record to carry the corrected miss reason, got %q", image.CacheMissReason)
	}
	for _, platform := range image.Steps {
		if platform.CacheHit == nil || *platform.CacheHit {
			t.Fatalf("expected platform step %s to report a cache miss, got %v", platform.Name, platform.CacheHit)
		}
	}
}
