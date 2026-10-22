package eruncommon

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// consumedTestStageDockerfileContent is a Dockerfile whose final image is only
// produced once its `test` stage passed: the builder copies the stage's marker.
const consumedTestStageDockerfileContent = "" +
	"FROM --platform=$BUILDPLATFORM golang:1.26.0 AS test\n" +
	"RUN make check && touch /test-ok\n" +
	"\n" +
	"FROM golang:1.26.0 AS builder\n" +
	"COPY --from=test /test-ok /tmp/erun-test-ok\n" +
	"RUN go build ./...\n"

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

var dockerfileCopyFromTestPattern = regexp.MustCompile(`(?im)^\s*COPY\s+--from=test(?:\s|$)`)

// dockerfileHasConsumedTestStage reports whether a Dockerfile declares a
// `test` stage that a later stage consumes via `COPY --from=test`, which is
// what makes BuildKit run the tests as part of building the image: no image is
// produced when they fail. It is a structural contract the Dockerfile tests
// pin, not a build decision.
func dockerfileHasConsumedTestStage(dockerfilePath string) bool {
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return false
	}
	return dockerfileTestStagePattern.Match(data) && dockerfileCopyFromTestPattern.Match(data)
}

func TestDockerfileHasConsumedTestStageDetectsTheMarkerConvention(t *testing.T) {
	path := writeTestDockerfile(t, t.TempDir(), consumedTestStageDockerfileContent)
	if !dockerfileHasConsumedTestStage(path) {
		t.Fatal("expected a Dockerfile with AS test + COPY --from=test to be detected as consuming its test stage")
	}
}

func TestDockerfileHasConsumedTestStageIgnoresOrdinaryDockerfiles(t *testing.T) {
	path := writeTestDockerfile(t, t.TempDir(), "FROM scratch\nCOPY . /app\n")
	if dockerfileHasConsumedTestStage(path) {
		t.Fatal("expected an ordinary Dockerfile with no test stage not to be detected as consuming one")
	}
}

// A stage merely *prefixed* with `test` is a different stage, and the
// erun-backend-api Dockerfile carries exactly such a stage beside its real one.
// Reading it as a test stage would grant the build the host-network
// entitlement for nothing.
func TestDockerfileTestStagePredicatesIgnoreATestPrefixedStageName(t *testing.T) {
	path := writeTestDockerfile(t, t.TempDir(), "FROM alpine:3.20 AS test-toolchain\nRUN apk add curl\n\n"+
		"FROM golang:1.26.0 AS builder\nCOPY --from=test-toolchain /usr/bin/curl /usr/bin/curl\n")
	if dockerfileHasConsumedTestStage(path) {
		t.Error("a `test-toolchain` stage and a `COPY --from=test-toolchain` were read as a consumed test stage")
	}
	if dockerfileDeclaresTestStage(path) {
		t.Error("a `test-toolchain` stage was read as declaring a test stage, which would grant this build the host-network entitlement")
	}
}

// A `test` stage nobody depends on still declares a place for tests to run,
// which is what the host-network entitlement is scoped by, but nothing in the
// build graph requires it to have run.
func TestDockerfileDeclaresTestStageIsBroaderThanTheConsumedCheck(t *testing.T) {
	declared := writeTestDockerfile(t, t.TempDir(), "FROM golang:1.26.0 AS test\nRUN make check\n\nFROM scratch\nCOPY . /app\n")
	if dockerfileHasConsumedTestStage(declared) {
		t.Fatal("a test stage with no COPY --from=test dependent was read as consumed")
	}
	if !dockerfileDeclaresTestStage(declared) {
		t.Fatal("expected a Dockerfile declaring AS test to be detected as declaring a test stage even when no later stage consumes it")
	}
	ordinary := writeTestDockerfile(t, t.TempDir(), "FROM scratch\nCOPY . /app\n")
	if dockerfileDeclaresTestStage(ordinary) {
		t.Fatal("expected an ordinary Dockerfile with no test stage not to be detected as declaring one")
	}
}

// TestRealCLIDockerfileStillConsumesItsTestStage pins the real artefact the
// synthetic cases only model: the erun-cli image must be produced only once
// its test stage has passed, so a successful build of it -- and the fp tag
// written after one -- means its tests ran and passed.
//
// Reads a sibling module, so it must run uncached (go test -count=1).
func TestRealCLIDockerfileStillConsumesItsTestStage(t *testing.T) {
	const path = "../erun-devops/docker/erun-cli/Dockerfile"
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the erun-cli Dockerfile is not readable at %s: %v", path, err)
	}
	if !dockerfileHasConsumedTestStage(path) {
		t.Fatalf("%s no longer declares a stage `AS test` consumed by a later `COPY --from=test`, so its image would build and publish with its tests never run", path)
	}
}

func testStagePromotionBuild(t *testing.T) DockerBuildSpec {
	t.Helper()
	dir := t.TempDir()
	return DockerBuildSpec{
		ContextDir:     dir,
		DockerfilePath: writeTestDockerfile(t, dir, consumedTestStageDockerfileContent),
		Image:          DockerImageReference{ImageName: "erun-devops", Tag: "ghcr.io/sophium/erun-devops:1.0.248"},
		Platforms:      []string{"linux/amd64"},
	}
}

// An fp tag is written only after a successful docker build, which ran the
// Dockerfile's test stage, so an unchanged tested component reuses its last
// successful result like any other image.
func TestApplyIncrementalPromotionPromotesATestedDockerfileWhenItsFingerprintExists(t *testing.T) {
	out, err := applyIncrementalPromotion([]DockerBuildSpec{testStagePromotionBuild(t)}, func(string) (bool, error) { return true, nil })
	if err != nil {
		t.Fatalf("applyIncrementalPromotion: %v", err)
	}
	if len(out) != 1 || !out[0].Promote {
		t.Fatalf("expected the tested Dockerfile to promote from its present fingerprint image, got %+v", out)
	}
}

// The other side of reuse: a changed tested component has no fp tag for its
// inputs, so it rebuilds and runs its tests.
func TestApplyIncrementalPromotionRebuildsATestedDockerfileWhenItsFingerprintIsMissing(t *testing.T) {
	out, err := applyIncrementalPromotion([]DockerBuildSpec{testStagePromotionBuild(t)}, func(string) (bool, error) { return false, nil })
	if err != nil {
		t.Fatalf("applyIncrementalPromotion: %v", err)
	}
	if out[0].Promote {
		t.Fatal("a tested Dockerfile with no fingerprint image must rebuild")
	}
	if len(out[0].MissingFingerprintPlatforms) != 1 {
		t.Fatalf("expected the missing platform to be recorded for the trace, got %v", out[0].MissingFingerprintPlatforms)
	}
}

func TestApplyIncrementalPromotionStillPromotesOrdinaryDockerfiles(t *testing.T) {
	dir := t.TempDir()
	build := DockerBuildSpec{
		ContextDir:     dir,
		DockerfilePath: writeTestDockerfile(t, dir, "FROM scratch\nCOPY . /app\n"),
		Image:          DockerImageReference{ImageName: "erun-console", Tag: "ghcr.io/sophium/erun-console:1.0.248"},
		Platforms:      []string{"linux/amd64"},
	}
	out, err := applyIncrementalPromotion([]DockerBuildSpec{build}, func(string) (bool, error) { return true, nil })
	if err != nil {
		t.Fatalf("applyIncrementalPromotion: %v", err)
	}
	if !out[0].Promote {
		t.Error("an ordinary Dockerfile with a present fingerprint image should still promote")
	}
}

// --no-incremental skips promotion entirely and inspects nothing, so nothing
// here may shell out.
func TestApplyIncrementalToDockerBuildsPromotesNothingWithoutIncremental(t *testing.T) {
	out, err := ApplyIncrementalToDockerBuilds(Context{}, []DockerBuildSpec{testStagePromotionBuild(t)}, true)
	if err != nil {
		t.Fatalf("ApplyIncrementalToDockerBuilds: %v", err)
	}
	if len(out) != 1 || out[0].Promote || out[0].Fingerprint != "" {
		t.Fatalf("--no-incremental must leave the build unpromoted and unfingerprinted, got %+v", out)
	}
}

// An all-promoted plan is a valid result: every component reused its last
// successful result. Driven through the umbrella a real `erun build` runs, the
// builder must be handed the promote and the run must report success.
func TestBuildSucceedsForAnAllPromotedPlanWithATestStageDockerfile(t *testing.T) {
	build := testStagePromotionBuild(t)
	build.Fingerprint = "abc123"
	build.Promote = true

	if err := RunBuildExecution(Context{DryRun: true}, BuildExecutionSpec{dockerBuilds: []DockerBuildSpec{build}}, nil, nil, nil, nil, CloudDependencies{}); err != nil {
		t.Fatalf("expected an all-promoted plan to be accepted, got %v", err)
	}

	var log bytes.Buffer
	ctx, finish := traceBuildUmbrella(Context{Logger: NewLoggerWithWriters(VerbosityInfo, &log, &log)})
	promoted := false
	err := RunDockerBuild(ctx, build, func(input DockerBuildSpec, _, _ io.Writer) error {
		promoted = input.Promote
		if input.PlatformObserver != nil {
			input.PlatformObserver("linux/amd64", time.Second, nil, nil, "")
		}
		return nil
	})
	finish(&err)
	if err != nil {
		t.Fatalf("expected the promoted build to succeed, got %v", err)
	}
	if !promoted {
		t.Fatal("expected the builder to be handed the promote rather than a rebuild")
	}
	if !strings.Contains(log.String(), "==> Built in ") {
		t.Fatalf("expected the run to close with the success marker, got:\n%s", log.String())
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
