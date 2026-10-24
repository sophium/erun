package build

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/primitive/shared"
)

// consumedTestStageDockerfileContent is the repository's component shape:
// build -> test -> dist -> runtime. The `runtime` image takes its artifact from
// `dist`, which is built FROM test, so the image is only produced once the
// tests passed, and a version stamp in `dist` leaves `test` cached.
const consumedTestStageDockerfileContent = "" +
	"FROM --platform=$BUILDPLATFORM golang:1.26.0 AS build\n" +
	"RUN go build -o /out/app ./...\n" +
	"\n" +
	"FROM build AS test\n" +
	"RUN go test ./...\n" +
	"\n" +
	"FROM test AS dist\n" +
	"ARG TARGETARCH\n" +
	"RUN GOARCH=$TARGETARCH go build -o /dist/app ./...\n" +
	"\n" +
	"FROM scratch AS runtime\n" +
	"COPY --from=dist /dist/app /app\n"

func writeTestDockerfile(t *testing.T, dir, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDockerfileHasConsumedTestStageDetectsTheComponentShape(t *testing.T) {
	path := writeTestDockerfile(t, t.TempDir(), consumedTestStageDockerfileContent)
	if !shared.DockerfileHasConsumedTestStage(path) {
		t.Fatal("expected a Dockerfile whose dist stage is built FROM test to be detected as consuming its test stage")
	}
}

func TestDockerfileHasConsumedTestStageIgnoresOrdinaryDockerfiles(t *testing.T) {
	path := writeTestDockerfile(t, t.TempDir(), "FROM scratch\nCOPY . /app\n")
	if shared.DockerfileHasConsumedTestStage(path) {
		t.Fatal("expected an ordinary Dockerfile with no test stage not to be detected as consuming one")
	}
}

// A stage merely *prefixed* with `test` is a different stage: naming a stage
// after the toolchain it installs is ordinary. Reading it as a test stage would
// grant the build the host-network entitlement for nothing.
func TestDockerfileTestStagePredicatesIgnoreATestPrefixedStageName(t *testing.T) {
	path := writeTestDockerfile(t, t.TempDir(), "FROM alpine:3.20 AS test-toolchain\nRUN apk add curl\n\n"+
		"FROM golang:1.26.0 AS builder\nCOPY --from=test-toolchain /usr/bin/curl /usr/bin/curl\n")
	if shared.DockerfileHasConsumedTestStage(path) {
		t.Error("a `test-toolchain` stage and a `COPY --from=test-toolchain` were read as a consumed test stage")
	}
	if shared.DockerfileDeclaresTestStage(path) {
		t.Error("a `test-toolchain` stage was read as declaring a test stage, which would grant this build the host-network entitlement")
	}
}

// A `test` stage nobody depends on still declares a place for tests to run,
// which is what the host-network entitlement is scoped by, but nothing in the
// build graph requires it to have run.
func TestDockerfileDeclaresTestStageIsBroaderThanTheConsumedCheck(t *testing.T) {
	declared := writeTestDockerfile(t, t.TempDir(), "FROM golang:1.26.0 AS test\nRUN make check\n\nFROM scratch\nCOPY . /app\n")
	if shared.DockerfileHasConsumedTestStage(declared) {
		t.Fatal("a test stage with no FROM test or COPY --from=test dependent was read as consumed")
	}
	if !shared.DockerfileDeclaresTestStage(declared) {
		t.Fatal("expected a Dockerfile declaring AS test to be detected as declaring a test stage even when no later stage consumes it")
	}
	ordinary := writeTestDockerfile(t, t.TempDir(), "FROM scratch\nCOPY . /app\n")
	if shared.DockerfileDeclaresTestStage(ordinary) {
		t.Fatal("expected an ordinary Dockerfile with no test stage not to be detected as declaring one")
	}
}

// DockerImageBuilder promotes a tested Dockerfile by re-tagging its cached
// fingerprint image, with no docker build behind it.
func TestDockerImageBuilderPromotesATestStageDockerfile(t *testing.T) {
	invocations := newFakeDockerImageStore(t, "ghcr.io/sophium/erun-console:fp-abc123-amd64")
	build := testPromoteBuildInput()
	build.DockerfilePath = writeTestDockerfile(t, t.TempDir(), consumedTestStageDockerfileContent)

	var stdout, stderr bytes.Buffer
	if err := DockerImageBuilder(build, &stdout, &stderr); err != nil {
		t.Fatalf("expected the tested Dockerfile to promote, got %v\n%s", err, stderr.String())
	}
	if strings.Contains(invocations(), "build ") {
		t.Fatalf("expected a promote with no docker build, got invocations:\n%s", invocations())
	}
}

// TestRealDevopsDockerfileStillConsumesItsTestStage pins the real artefact the
// synthetic cases only model: the erun-devops image, which ships erun and
// emcp, must be produced only once its test stage has passed, so a successful
// build of it -- and the fp tag written after one -- means its tests ran and
// passed.
//
// Reads a sibling module, so it must run uncached (go test -count=1).
func TestRealDevopsDockerfileStillConsumesItsTestStage(t *testing.T) {
	path := filepath.Join(erunRepoRoot(t), "erun-devops", "docker", "erun-devops", "Dockerfile")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the erun-devops Dockerfile is not readable at %s: %v", path, err)
	}
	if !shared.DockerfileHasConsumedTestStage(path) {
		t.Fatalf("%s no longer declares a stage `AS test` that a later stage consumes (`FROM test` or `COPY --from=test`), so its image would build and publish with its tests never run", path)
	}
}
