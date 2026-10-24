package shared

import (
	"fmt"
	"os"
	"regexp"
)

var dockerfileVersionedFromPattern = regexp.MustCompile(`(?im)^\s*FROM(?:\s+--platform=\S+)?\s+[^\s]*\$\{?ERUN_VERSION\}?`)

// dockerfileHasVersionedFrom identifies images that use ERUN_VERSION only for
// base-image resolution, not to bake a version into a compiled binary, so their
// ERUN_VERSION build arg must be the full snapshot version, not the stable semver.
func dockerfileHasVersionedFrom(dockerfilePath string) bool {
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return false
	}
	return dockerfileVersionedFromPattern.Match(data)
}

// The pattern requires the stage name to be exactly `test`. A word-boundary
// match is not enough: `AS test-toolchain` satisfies one while the Dockerfile
// declares no test stage at all, which would grant it the host-network
// entitlement for nothing. Naming a stage after the toolchain it installs is
// ordinary.
var DockerfileTestStagePattern = regexp.MustCompile(`(?im)^\s*FROM\s+.*\bAS\s+test(?:\s|$)`)

// DockerfileDeclaresTestStage reports whether a Dockerfile declares a `test`
// stage at all: whether the build has a place for tests to run, which is the
// question the host-network entitlement is scoped by. Whether a later stage
// consumes it is checkDockerfileTestStageConsumed's question.
func DockerfileDeclaresTestStage(dockerfilePath string) bool {
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return false
	}
	return DockerfileTestStagePattern.Match(data)
}

// dockerfileTestStageConsumerPattern matches a later stage that depends on the
// `test` stage: a stage built `FROM test`, `COPY --from=test` (with any other
// flags), or a `RUN --mount=...,from=test`. Any of them puts the tests in the
// graph BuildKit walks for the final image; erun does not parse stage
// reachability beyond that.
var dockerfileTestStageConsumerPattern = regexp.MustCompile(`(?im)^\s*(?:FROM(?:\s+--platform=\S+)?\s+test(?:\s|$)|(?:COPY|ADD)\s+(?:--\S+\s+)*--from=test(?:\s|$)|RUN\s.*--mount=\S*\bfrom=test(?:[,\s]|$))`)

// DockerfileHasConsumedTestStage reports whether a Dockerfile declares a
// `test` stage that a later stage consumes, which is what makes BuildKit run
// the tests as part of building the image: no image is produced when they fail.
func DockerfileHasConsumedTestStage(dockerfilePath string) bool {
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return false
	}
	return DockerfileTestStagePattern.Match(data) && dockerfileTestStageConsumerPattern.Match(data)
}

// CheckDockerfileTestStageConsumed refuses a Dockerfile that declares a `test`
// stage no later stage consumes. BuildKit prunes such a stage from the final
// image's graph, so the build would succeed, write its fingerprint tag, and
// report a tested component whose tests never ran.
func CheckDockerfileTestStageConsumed(dockerfilePath string) error {
	if !DockerfileDeclaresTestStage(dockerfilePath) || DockerfileHasConsumedTestStage(dockerfilePath) {
		return nil
	}
	return fmt.Errorf("%s declares a stage named \"test\" that no later stage consumes, so docker build would skip it and its tests would never run: take the image's artifacts from the tested stage (`COPY --from=test <artifact> <dest>` in the final stage, or build a helper stage `FROM test` and copy from that), or rename the stage if it is not the component's tests", dockerfilePath)
}
