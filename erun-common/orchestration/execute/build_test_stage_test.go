package execute

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/helm"
	buildpkg "github.com/sophium/erun/erun-common/primitive/build"
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

func testStagePromotionBuild(t *testing.T) shared.DockerBuildSpec {
	t.Helper()
	dir := t.TempDir()
	return shared.DockerBuildSpec{
		ContextDir:     dir,
		DockerfilePath: writeTestDockerfile(t, dir, consumedTestStageDockerfileContent),
		Image:          helm.DockerImageReference{ImageName: "erun-devops", Tag: "ghcr.io/sophium/erun-devops:1.0.248"},
		Platforms:      []string{"linux/amd64"},
	}
}

// An all-promoted plan is a valid result: every component reused its last
// successful result. Driven through the umbrella a real `erun build` runs, the
// builder must be handed the promote and the run must report success.
func TestBuildSucceedsForAnAllPromotedPlanWithATestStageDockerfile(t *testing.T) {
	build := testStagePromotionBuild(t)
	build.Fingerprint = "abc123"
	build.Promote = true

	if err := RunBuildExecution(core.Context{DryRun: true}, shared.BuildExecutionSpec{DockerBuilds: []shared.DockerBuildSpec{build}}, nil, nil, nil, nil, cloud.CloudDependencies{}); err != nil {
		t.Fatalf("expected an all-promoted plan to be accepted, got %v", err)
	}

	var log bytes.Buffer
	ctx, finish := buildpkg.TraceBuildUmbrella(core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log)})
	promoted := false
	err := buildpkg.RunDockerBuild(ctx, build, func(input shared.DockerBuildSpec, _, _ io.Writer) error {
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
