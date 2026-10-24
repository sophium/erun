package eruncommon

import (
	"os"
	"path/filepath"
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
