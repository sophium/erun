package shared

import (
	"fmt"
	"os"
	"regexp"
)

var dockerfileVersionedFromPattern = regexp.MustCompile(`(?im)^\s*FROM(?:\s+--platform=\S+)?\s+[^\s]*\$\{?ERUN_VERSION\}?`)

// dockerfileHasVersionedFrom identifies images that use ERUN_VERSION only to
// resolve a base, so they get the full snapshot version.
func dockerfileHasVersionedFrom(dockerfilePath string) bool {
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return false
	}
	return dockerfileVersionedFromPattern.Match(data)
}

// DockerfileTestStagePattern requires exactly `test`: `AS test-toolchain`
// must not earn the host-network entitlement.
var DockerfileTestStagePattern = regexp.MustCompile(`(?im)^\s*FROM\s+.*\bAS\s+test(?:\s|$)`)

func DockerfileDeclaresTestStage(dockerfilePath string) bool {
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return false
	}
	return DockerfileTestStagePattern.Match(data)
}

// dockerfileTestStageConsumerPattern matches FROM test, COPY --from=test, and
// RUN --mount from=test; stage reachability is not parsed further.
var dockerfileTestStageConsumerPattern = regexp.MustCompile(`(?im)^\s*(?:FROM(?:\s+--platform=\S+)?\s+test(?:\s|$)|(?:COPY|ADD)\s+(?:--\S+\s+)*--from=test(?:\s|$)|RUN\s.*--mount=\S*\bfrom=test(?:[,\s]|$))`)

func DockerfileHasConsumedTestStage(dockerfilePath string) bool {
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return false
	}
	return DockerfileTestStagePattern.Match(data) && dockerfileTestStageConsumerPattern.Match(data)
}

// CheckDockerfileTestStageConsumed refuses an unconsumed test stage, which
// BuildKit prunes, so the tests would silently never run.
func CheckDockerfileTestStageConsumed(dockerfilePath string) error {
	if !DockerfileDeclaresTestStage(dockerfilePath) || DockerfileHasConsumedTestStage(dockerfilePath) {
		return nil
	}
	return fmt.Errorf("%s declares a stage named \"test\" that no later stage consumes, so docker build would skip it and its tests would never run: take the image's artifacts from the tested stage (`COPY --from=test <artifact> <dest>` in the final stage, or build a helper stage `FROM test` and copy from that), or rename the stage if it is not the component's tests", dockerfilePath)
}
