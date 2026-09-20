package eruncommon

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const gateDockerfileContent = "" +
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

func TestDockerfileHasGateTestStageDetectsTheMarkerConvention(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDockerfile(t, dir, gateDockerfileContent)
	if !dockerfileHasGateTestStage(path) {
		t.Fatal("expected a Dockerfile with AS test + COPY --from=test to be detected as a gate")
	}
}

func TestDockerfileHasGateTestStageIgnoresOrdinaryDockerfiles(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDockerfile(t, dir, "FROM scratch\nCOPY . /app\n")
	if dockerfileHasGateTestStage(path) {
		t.Fatal("expected an ordinary Dockerfile with no test stage to not be flagged as a gate")
	}
}

func TestDockerfileHasGateTestStageRequiresBothTheStageAndTheDependency(t *testing.T) {
	dir := t.TempDir()
	// A `test` stage nobody depends on is not a gate: nothing in the real build
	// graph requires it to have run.
	path := writeTestDockerfile(t, dir, "FROM golang:1.26.0 AS test\nRUN make check\n\nFROM scratch\nCOPY . /app\n")
	if dockerfileHasGateTestStage(path) {
		t.Fatal("expected a test stage with no COPY --from=test dependent to not be flagged as a gate")
	}
}

// TestRealDevopsDockerfileStillMatchesTheGateConvention pins the artefact every
// case above only models. Detection is what exempts erun-devops from
// fingerprint promotion, so if the real Dockerfile stops matching the
// convention the exemption stops firing silently: a wholly cached build then
// reports the same exit 0 in seconds as one that ran make check, while every
// synthetic case above stays green. The real Dockerfile is the one input those
// cases cannot cover, which is why editing it -- renaming the stage, dropping
// the dependency on it -- has to fail here rather than only making the gate
// fast again.
//
// Reads a sibling module, so it must run uncached: a cached pass cannot
// establish that this file was re-read. test-erun-common already passes
// -count=1. Same shape as the cloud-context entrypoint parity test.
func TestRealDevopsDockerfileStillMatchesTheGateConvention(t *testing.T) {
	const path = "../erun-devops/docker/erun-devops/Dockerfile"
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the gate Dockerfile this exemption exists for is not readable at %s: %v", path, err)
	}
	if !dockerfileHasGateTestStage(path) {
		t.Fatalf("%s no longer matches the gate convention (a stage declared `AS test` plus a later `COPY --from=test`), so applyIncrementalPromotion would promote it from a cached fingerprint image and skip make check, reporting a wholly unverified build as a pass", path)
	}
}

// TestApplyIncrementalPromotionNeverPromotesAGateDockerfile reproduces
// The defect: a `docker build` promotion decision is a pure function of
// whether the fp-tagged image already exists locally, with no way to prove
// the gate that image's Dockerfile runs (make check, in the `test` stage) was
// ever actually executed against the current tree. Before the fix, an
// already-present fp tag made this promote just like any other image;
// after it, a Dockerfile whose builder stage depends on a `test` stage marker
// must always go through a real docker build instead.
func TestApplyIncrementalPromotionNeverPromotesAGateDockerfile(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDockerfile(t, dir, gateDockerfileContent)
	build := DockerBuildSpec{
		ContextDir:     dir,
		DockerfilePath: path,
		Image:          DockerImageReference{ImageName: "erun-devops", Tag: "ghcr.io/sophium/erun-devops:1.0.248"},
		Platforms:      []string{"linux/amd64"},
	}

	// inspect reports every fp-tag as already present locally -- the exact
	// condition that makes an ordinary image promote instead of rebuild.
	inspect := func(tag string) (bool, error) { return true, nil }

	out, err := applyIncrementalPromotion([]DockerBuildSpec{build}, inspect)
	if err != nil {
		t.Fatalf("applyIncrementalPromotion: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected exactly one build, got %d", len(out))
	}
	if !out[0].GateTestStage {
		t.Error("expected the gate Dockerfile to be marked GateTestStage")
	}
	if out[0].Promote {
		t.Error("a gate Dockerfile must never be promoted from a cached fingerprint image, even when one exists locally -- this is the exact erun#2090 failure mode: a cache hit standing in for the gate having run")
	}
}

// TestApplyIncrementalPromotionStillPromotesOrdinaryDockerfiles guards
// against an overly broad fix: only a Dockerfile that actually declares the
// test-stage-gate convention should be exempted from promotion. Everything
// else keeps the existing, desirable artifact-caching behavior.
func TestApplyIncrementalPromotionStillPromotesOrdinaryDockerfiles(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDockerfile(t, dir, "FROM scratch\nCOPY . /app\n")
	build := DockerBuildSpec{
		ContextDir:     dir,
		DockerfilePath: path,
		Image:          DockerImageReference{ImageName: "erun-console", Tag: "ghcr.io/sophium/erun-console:1.0.248"},
		Platforms:      []string{"linux/amd64"},
	}

	inspect := func(tag string) (bool, error) { return true, nil }

	out, err := applyIncrementalPromotion([]DockerBuildSpec{build}, inspect)
	if err != nil {
		t.Fatalf("applyIncrementalPromotion: %v", err)
	}
	if out[0].GateTestStage {
		t.Error("an ordinary Dockerfile must not be flagged as a gate")
	}
	if !out[0].Promote {
		t.Error("an ordinary Dockerfile with a present fingerprint image should still promote")
	}
}

// TestDockerImageBuilderRefusesToPromoteAGateDockerfile is the defense-in-depth
// backstop at the actual execution fork point: applyIncrementalPromotion
// should already make Promote+GateTestStage unreachable together, but if it
// ever happened anyway, DockerImageBuilder must fail loudly instead of
// silently re-tagging a cached image and skipping the gate. No fake `docker`
// is put on PATH here: if this guard were missing, the call would either
// reach a real `docker` (mutating state this test never wants to touch) or
// fail with an unrelated "executable file not found" error, so asserting the
// specific sentinel error also proves the refusal fired before any
// subprocess was attempted.
func TestDockerImageBuilderRefusesToPromoteAGateDockerfile(t *testing.T) {
	build := testPromoteBuildInput()
	build.GateTestStage = true

	var stdout, stderr bytes.Buffer
	err := DockerImageBuilder(build, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected DockerImageBuilder to refuse promoting a gate Dockerfile")
	}
	if !errors.Is(err, errGateTestStagePromoted) {
		t.Fatalf("expected the gate-promotion sentinel error, got: %v", err)
	}
}

// TestApplyIncrementalToDockerBuildsKeepsTheGateMarkedWithoutIncremental
// reproduces the report that `--no-incremental` builds silently. It returned early from
// ApplyIncrementalToDockerBuilds, before applyIncrementalPromotion ever ran, and
// the *only* thing that sets GateTestStage is the gate branch inside that
// function. So the flag skipped the marking, and with it both surfaces that tell
// an operator whether this build runs make check: the rebuild-trigger trace line
// and gateTestStageProvenanceLines' `test stage (...): LIVE` declaration. The
// result was a build that exited 0 with no guard line and no stage declaration --
// less auditable than the plain build it was meant to improve on, and
// indistinguishable from a real pass.
//
// Before the fix this fails on the marking itself: with the flag set, a gate
// Dockerfile came back with GateTestStage false. Ordinary Dockerfiles are
// asserted alongside it so the fix cannot pass by marking everything.
func TestApplyIncrementalToDockerBuildsKeepsTheGateMarkedWithoutIncremental(t *testing.T) {
	dir := t.TempDir()
	gate := DockerBuildSpec{
		ContextDir:     dir,
		DockerfilePath: writeTestDockerfile(t, filepath.Join(dir, "gate"), gateDockerfileContent),
		Image:          DockerImageReference{ImageName: "erun-devops", Tag: "ghcr.io/sophium/erun-devops:1.0.248"},
		Platforms:      []string{"linux/amd64"},
	}
	ordinary := DockerBuildSpec{
		ContextDir:     filepath.Join(dir, "ordinary"),
		DockerfilePath: writeTestDockerfile(t, filepath.Join(dir, "ordinary"), "FROM scratch\nCOPY . /app\n"),
		Image:          DockerImageReference{ImageName: "erun-console", Tag: "ghcr.io/sophium/erun-console:1.0.248"},
		Platforms:      []string{"linux/amd64"},
	}

	// No docker daemon on PATH: the noIncremental path must not inspect or
	// promote anything, so nothing here may shell out.
	out, err := ApplyIncrementalToDockerBuilds(Context{}, []DockerBuildSpec{gate, ordinary}, true)
	if err != nil {
		t.Fatalf("ApplyIncrementalToDockerBuilds: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected both builds to survive, got %d", len(out))
	}
	if !out[0].GateTestStage {
		t.Error("--no-incremental dropped the GateTestStage marking: the build would have no guard line and no `test stage (...): LIVE` declaration")
	}
	if out[0].Promote {
		t.Error("--no-incremental must not promote anything, least of all a gate Dockerfile")
	}
	if out[1].GateTestStage {
		t.Error("an ordinary Dockerfile must not be marked as a gate")
	}
	if out[1].Promote {
		t.Error("--no-incremental must not promote an ordinary Dockerfile either")
	}
}

// TestNoIncrementalStillDeclaresTheGateLineAndTheLiveTestStage is the
// reproduction at the surface the report actually observed: the operator-facing
// lines. It drives the same trace functions the build drives, on builds that came
// through the real --no-incremental entry point, and requires both the guard line
// and the LIVE declaration to be present. Asserting the marking alone would not
// prove the report's symptom is gone -- the lines are what the report quoted as
// missing.
func TestNoIncrementalStillDeclaresTheGateLineAndTheLiveTestStage(t *testing.T) {
	dir := t.TempDir()
	gate := DockerBuildSpec{
		ContextDir:     dir,
		DockerfilePath: writeTestDockerfile(t, filepath.Join(dir, "gate"), gateDockerfileContent),
		Image:          DockerImageReference{ImageName: "erun-devops", Tag: "ghcr.io/sophium/erun-devops:1.0.248"},
		Platforms:      []string{"linux/amd64"},
	}

	out, err := ApplyIncrementalToDockerBuilds(Context{}, []DockerBuildSpec{gate}, true)
	if err != nil {
		t.Fatalf("ApplyIncrementalToDockerBuilds: %v", err)
	}

	var buf bytes.Buffer
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, &buf, &buf)}
	traceDockerBuild(ctx, out[0])
	lines := gateTestStageProvenanceLines(out)
	rendered := buf.String() + strings.Join(lines, "\n")

	if !strings.Contains(rendered, "because its Dockerfile's test stage runs the build's own gate") {
		t.Errorf("--no-incremental must still print the gate guard line; got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "test stage (ghcr.io/sophium/erun-devops:1.0.248): LIVE") {
		t.Errorf("--no-incremental must still declare the test stage as LIVE; got:\n%s", rendered)
	}
}

func TestGateTestStageProvenanceLinesReportLiveAndCachedStatus(t *testing.T) {
	live := DockerBuildSpec{GateTestStage: true, Image: DockerImageReference{Tag: "ghcr.io/sophium/erun-devops:1.0.248"}}
	cached := DockerBuildSpec{GateTestStage: true, Promote: true, Image: DockerImageReference{Tag: "ghcr.io/sophium/erun-devops:1.0.247"}}
	ordinary := DockerBuildSpec{Image: DockerImageReference{Tag: "ghcr.io/sophium/erun-console:1.0.248"}}

	lines := gateTestStageProvenanceLines([]DockerBuildSpec{live, ordinary})
	if len(lines) != 1 {
		t.Fatalf("expected exactly one provenance line for one gate build, got %v", lines)
	}
	if !bytes.Contains([]byte(lines[0]), []byte("LIVE")) {
		t.Errorf("expected a non-promoted gate build to report LIVE, got: %s", lines[0])
	}

	cachedLines := gateTestStageProvenanceLines([]DockerBuildSpec{cached})
	if len(cachedLines) != 1 || !bytes.Contains([]byte(cachedLines[0]), []byte("CACHED")) {
		t.Errorf("expected a promoted gate build to report CACHED, got: %v", cachedLines)
	}
}
