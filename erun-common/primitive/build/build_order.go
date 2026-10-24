package build

import (
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/sophium/erun/erun-common/primitive/shared"
)

func orderedDockerBuildSpecs(builds []shared.DockerBuildSpec) []shared.DockerBuildSpec {
	if len(builds) < 2 {
		return builds
	}

	buildsByTag := make(map[string]shared.DockerBuildSpec, len(builds))
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

	ordered := make([]shared.DockerBuildSpec, 0, len(builds))
	for _, tag := range tags {
		ordered = append(ordered, buildsByTag[tag])
	}
	return ordered
}

var dockerfileFromPattern = regexp.MustCompile(`(?im)^\s*FROM(?:\s+--platform=\S+)?\s+([^\s]+)`)

var dockerfileVersionReferencePattern = regexp.MustCompile(`\$\{?ERUN_VERSION\b`)

// dockerfileConsumesVersion reports whether a Dockerfile uses ERUN_VERSION, making
// the version a build input even for a byte-identical context.
func dockerfileConsumesVersion(dockerfilePath string) bool {
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return false
	}
	return dockerfileVersionReferencePattern.Match(data)
}

func dockerfileLocalBaseImageTags(dockerfilePath string, buildsByTag map[string]shared.DockerBuildSpec) []string {
	deps := dockerfileLocalBaseImageDeps(dockerfilePath, buildsByTag)
	tags := make([]string, 0, len(deps))
	for _, dep := range deps {
		tags = append(tags, dep.tag)
	}
	return tags
}

// dockerfileBaseImageDep is one sibling build a Dockerfile FROMs. Only a versioned
// FROM can be redirected to another tag via the ERUN_VERSION build arg.
type dockerfileBaseImageDep struct {
	tag       string
	versioned bool
}

// dockerfileLocalBaseImageDeps lists sibling builds a Dockerfile FROMs, in Dockerfile order.
func dockerfileLocalBaseImageDeps(dockerfilePath string, buildsByTag map[string]shared.DockerBuildSpec) []dockerfileBaseImageDep {
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

// markLocalBaseImageBuilds marks builds whose ${ERUN_VERSION} base this run builds without
// publishing: the plain base tag exists only after push's manifest assembly.
func markLocalBaseImageBuilds(builds []shared.DockerBuildSpec) []shared.DockerBuildSpec {
	if len(builds) < 2 {
		return builds
	}
	buildsByTag := dockerBuildsByTag(builds)
	out := make([]shared.DockerBuildSpec, len(builds))
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

func dockerfileLocalBaseImageVersionedTags(imageRef string, buildsByTag map[string]shared.DockerBuildSpec) []string {
	if !strings.Contains(imageRef, "ERUN_VERSION") {
		return nil
	}

	dependencies := make([]string, 0, 1)
	for tag := range buildsByTag {
		version := shared.DockerImageTagVersion(tag)
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
