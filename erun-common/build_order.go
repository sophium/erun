package eruncommon

import (
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

func dockerfileLocalBaseImageTags(dockerfilePath string, buildsByTag map[string]DockerBuildSpec) []string {
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return nil
	}

	matches := dockerfileFromPattern.FindAllStringSubmatch(string(data), -1)
	dependencies := make([]string, 0, len(matches))
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
				dependencies = append(dependencies, tag)
			}
			continue
		}
		dependencies = append(dependencies, imageRef)
	}
	return dependencies
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
