package run

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

// build -> test -> dist -> runtime: the image is produced only after tests pass.
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

// Reads a sibling module, so it must run uncached (go test -count=1).
func TestRealDevopsDockerfileStillConsumesItsTestStage(t *testing.T) {
	path := filepath.Join(erunRepoRoot(t), "erun-devops", "docker", "erun-devops", "Dockerfile")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the erun-devops Dockerfile is not readable at %s: %v", path, err)
	}
	if !dockerspec.DockerfileHasConsumedTestStage(path) {
		t.Fatalf("%s no longer declares a stage `AS test` that a later stage consumes (`FROM test` or `COPY --from=test`), so its image would build and publish with its tests never run", path)
	}
}
