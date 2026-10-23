package eruncommon

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

func orderedDockerBuildSpecs(builds []DockerBuildSpec) []DockerBuildSpec {
	if len(builds) < 2 {
		return builds
	}

	buildsByTag := make(map[string]DockerBuildSpec, len(builds))
	orderIndex := make(map[string]int, len(builds))
	for i, build := range builds {
		tag := strings.TrimSpace(build.Image.Tag)
		buildsByTag[tag] = build
		orderIndex[tag] = i
	}

	tags := make([]string, 0, len(builds))
	seen := make(map[string]bool, len(builds))
	var visit func(string)
	visit = func(tag string) {
		if seen[tag] {
			return
		}
		seen[tag] = true
		build, ok := buildsByTag[tag]
		if ok {
			for _, dependencyTag := range dockerfileLocalBaseImageTags(build.DockerfilePath, buildsByTag) {
				visit(dependencyTag)
			}
		}
		tags = append(tags, tag)
	}

	inputTags := make([]string, 0, len(builds))
	for _, build := range builds {
		inputTags = append(inputTags, strings.TrimSpace(build.Image.Tag))
	}
	slices.SortStableFunc(inputTags, func(a, b string) int {
		return orderIndex[a] - orderIndex[b]
	})
	for _, tag := range inputTags {
		visit(tag)
	}

	ordered := make([]DockerBuildSpec, 0, len(builds))
	for _, tag := range tags {
		ordered = append(ordered, buildsByTag[tag])
	}
	return ordered
}

var dockerfileFromPattern = regexp.MustCompile(`(?im)^\s*FROM(?:\s+--platform=\S+)?\s+([^\s]+)`)

var dockerfileVersionedFromPattern = regexp.MustCompile(`(?im)^\s*FROM(?:\s+--platform=\S+)?\s+[^\s]*\$\{?ERUN_VERSION\}?`)

var dockerfileVersionReferencePattern = regexp.MustCompile(`\$\{?ERUN_VERSION\b`)

// dockerfileConsumesVersion reports whether a Dockerfile resolves any of its own
// content through ERUN_VERSION — baking it into a compiled binary, or selecting
// the base tag it builds on. For such an image the version is a build input, so
// two versions are two artifacts even when every file in the context is byte
// identical; images that never reference it keep a version-independent identity.
func dockerfileConsumesVersion(dockerfilePath string) bool {
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return false
	}
	return dockerfileVersionReferencePattern.Match(data)
}

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
var dockerfileTestStagePattern = regexp.MustCompile(`(?im)^\s*FROM\s+.*\bAS\s+test(?:\s|$)`)

// dockerfileDeclaresTestStage reports whether a Dockerfile declares a `test`
// stage at all: whether the build has a place for tests to run, which is the
// question the host-network entitlement is scoped by. Whether a later stage
// consumes it is checkDockerfileTestStageConsumed's question.
func dockerfileDeclaresTestStage(dockerfilePath string) bool {
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return false
	}
	return dockerfileTestStagePattern.Match(data)
}

// dockerfileTestStageConsumerPattern matches a later stage that depends on the
// `test` stage: a stage built `FROM test`, `COPY --from=test` (with any other
// flags), or a `RUN --mount=...,from=test`. Any of them puts the tests in the
// graph BuildKit walks for the final image; erun does not parse stage
// reachability beyond that.
var dockerfileTestStageConsumerPattern = regexp.MustCompile(`(?im)^\s*(?:FROM(?:\s+--platform=\S+)?\s+test(?:\s|$)|(?:COPY|ADD)\s+(?:--\S+\s+)*--from=test(?:\s|$)|RUN\s.*--mount=\S*\bfrom=test(?:[,\s]|$))`)

// dockerfileHasConsumedTestStage reports whether a Dockerfile declares a
// `test` stage that a later stage consumes, which is what makes BuildKit run
// the tests as part of building the image: no image is produced when they fail.
func dockerfileHasConsumedTestStage(dockerfilePath string) bool {
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return false
	}
	return dockerfileTestStagePattern.Match(data) && dockerfileTestStageConsumerPattern.Match(data)
}

// checkDockerfileTestStageConsumed refuses a Dockerfile that declares a `test`
// stage no later stage consumes. BuildKit prunes such a stage from the final
// image's graph, so the build would succeed, write its fingerprint tag, and
// report a tested component whose tests never ran.
func checkDockerfileTestStageConsumed(dockerfilePath string) error {
	if !dockerfileDeclaresTestStage(dockerfilePath) || dockerfileHasConsumedTestStage(dockerfilePath) {
		return nil
	}
	return fmt.Errorf("%s declares a stage named \"test\" that no later stage consumes, so docker build would skip it and its tests would never run: take the image's artifacts from the tested stage (`COPY --from=test <artifact> <dest>` in the final stage, or build a helper stage `FROM test` and copy from that), or rename the stage if it is not the component's tests", dockerfilePath)
}

func dockerfileLocalBaseImageTags(dockerfilePath string, buildsByTag map[string]DockerBuildSpec) []string {
	deps := dockerfileLocalBaseImageDeps(dockerfilePath, buildsByTag)
	tags := make([]string, 0, len(deps))
	for _, dep := range deps {
		tags = append(tags, dep.tag)
	}
	return tags
}

// dockerfileBaseImageDep is one sibling build a Dockerfile FROMs, tagged with how
// the FROM names it. Only a versioned reference can be redirected to a different
// tag of the same base through the ERUN_VERSION build arg, so callers that must
// resolve a base the registry does not hold need the distinction; a literal FROM
// names exactly one tag and offers no such lever.
type dockerfileBaseImageDep struct {
	tag       string
	versioned bool
}

// dockerfileLocalBaseImageDeps lists the sibling builds a Dockerfile FROMs, in
// Dockerfile order — the order callers rely on to pick a deterministic build
// sequence and to name a cascade's trigger.
func dockerfileLocalBaseImageDeps(dockerfilePath string, buildsByTag map[string]DockerBuildSpec) []dockerfileBaseImageDep {
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return nil
	}

	matches := dockerfileFromPattern.FindAllStringSubmatch(string(data), -1)
	dependencies := make([]dockerfileBaseImageDep, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		imageRef := strings.TrimSpace(match[1])
		if imageRef == "" || strings.HasPrefix(imageRef, "${") {
			continue
		}
		if _, ok := buildsByTag[imageRef]; !ok {
			for _, tag := range dockerfileLocalBaseImageVersionedTags(imageRef, buildsByTag) {
				dependencies = append(dependencies, dockerfileBaseImageDep{tag: tag, versioned: true})
			}
			continue
		}
		dependencies = append(dependencies, dockerfileBaseImageDep{tag: imageRef})
	}
	return dependencies
}

// markLocalBaseImageBuilds records, on each build whose ${ERUN_VERSION} base this
// same run produces and does not publish, the base tag it must resolve locally.
// A local build only ever tags its output per-arch; the plain <registry>/<image>:<version>
// reference the wrapper's FROM asks for is minted in the registry by push's
// manifest assembly, so without this a dependent image can never build locally
// and `erun build` cannot gate a release before any git ref moves. A pushing base
// is left alone: its plain tag really does exist by the time the dependent builds,
// published by the base's own push earlier in the dependency order.
func markLocalBaseImageBuilds(builds []DockerBuildSpec) []DockerBuildSpec {
	if len(builds) < 2 {
		return builds
	}
	buildsByTag := dockerBuildsByTag(builds)
	out := make([]DockerBuildSpec, len(builds))
	copy(out, builds)
	for i := range out {
		for _, dep := range dockerfileLocalBaseImageDeps(out[i].DockerfilePath, buildsByTag) {
			if !dep.versioned {
				continue
			}
			if base, ok := buildsByTag[dep.tag]; !ok || base.Push {
				continue
			}
			out[i].LocalBaseTag = dep.tag
			break
		}
	}
	return out
}

func dockerfileLocalBaseImageVersionedTags(imageRef string, buildsByTag map[string]DockerBuildSpec) []string {
	if !strings.Contains(imageRef, "ERUN_VERSION") {
		return nil
	}

	dependencies := make([]string, 0, 1)
	for tag := range buildsByTag {
		version := dockerImageTagVersion(tag)
		if version == "" {
			continue
		}
		candidate := strings.ReplaceAll(imageRef, "${ERUN_VERSION}", version)
		candidate = strings.ReplaceAll(candidate, "$ERUN_VERSION", version)
		if candidate == tag {
			dependencies = append(dependencies, tag)
		}
	}
	return dependencies
}
