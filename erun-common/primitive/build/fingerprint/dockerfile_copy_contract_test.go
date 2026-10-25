package fingerprint

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// realDockerfilePaths excludes erun-skills templates, which use __PLACEHOLDER__ paths.
func realDockerfilePaths(t *testing.T) []string {
	t.Helper()
	root := repoRootForDockerignoreTest(t)
	matches, err := filepath.Glob(filepath.Join(root, "erun-devops", "docker", "*", "Dockerfile"))
	if err != nil {
		t.Fatalf("glob Dockerfiles: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no Dockerfiles found under erun-devops/docker")
	}
	sort.Strings(matches)
	return matches
}

func linuxPackageDockerfilePaths(t *testing.T) []string {
	t.Helper()
	root := repoRootForDockerignoreTest(t)
	matches, err := filepath.Glob(filepath.Join(root, "erun-devops", "linux", "*", "Dockerfile"))
	if err != nil {
		t.Fatalf("glob package Dockerfiles: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no Dockerfiles found under erun-devops/linux")
	}
	sort.Strings(matches)
	return matches
}

func allDockerfilePaths(t *testing.T) []string {
	t.Helper()
	return append(realDockerfilePaths(t), linuxPackageDockerfilePaths(t)...)
}

var dockerfileAddPattern = regexp.MustCompile(`(?im)^\s*ADD\s+`)

// dockerfileCopySources parses only COPY, so ADD content would never reach the fingerprint.
func TestDockerfilesNeverUseAddForLocalContent(t *testing.T) {
	for _, path := range allDockerfilePaths(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if dockerfileAddPattern.Match(data) {
			t.Errorf("%s uses ADD: computeBuildFingerprint only parses COPY instructions, so an ADD source's content would never enter the build fingerprint even though the real docker build copies it — teach dockerfileCopySources to parse ADD before using it here", path)
		}
	}
}

// collectFingerprintFiles Lstats sources literally, so a glob is silently dropped from the fingerprint.
func TestDockerfileCopySourcesContainNoGlobs(t *testing.T) {
	root := repoRootForDockerignoreTest(t)
	for _, path := range allDockerfilePaths(t) {
		sources, err := DockerfileCopySources(path, root)
		if err != nil {
			t.Fatalf("parse COPY sources for %s: %v", path, err)
		}
		for _, src := range sources {
			if strings.ContainsAny(src, "*?[") {
				t.Errorf("%s: COPY source %q contains a glob character — computeBuildFingerprint would silently skip it as a nonexistent literal path while the real docker build expands and copies the matches", path, src)
			}
		}
	}
}

var (
	dockerfileStageNamePattern = regexp.MustCompile(`(?im)^\s*FROM\s+.*\bAS\s+(\S+)`)
	dockerfileCopyFromPattern  = regexp.MustCompile(`(?im)^\s*COPY\s+.*?--from=(\S+)`)
)

// filterDockerfileCopyArgs drops every COPY --from, which is safe only for local stages.
func TestDockerfileCopyFromReferencesOnlyLocalStages(t *testing.T) {
	for _, path := range allDockerfilePaths(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		stages := make(map[string]struct{})
		for _, m := range dockerfileStageNamePattern.FindAllStringSubmatch(text, -1) {
			stages[strings.ToLower(m[1])] = struct{}{}
		}
		for _, m := range dockerfileCopyFromPattern.FindAllStringSubmatch(text, -1) {
			ref := strings.ToLower(m[1])
			if _, ok := stages[ref]; ok {
				continue
			}
			if isDigits(ref) {
				continue // numeric stage index (COPY --from=0 ...)
			}
			t.Errorf("%s: COPY --from=%s does not name a stage declared in this file (FROM ... AS %s) — filterDockerfileCopyArgs drops every --from as a stage reference unconditionally, so if this is really an external image its content is invisible to computeBuildFingerprint even though the real build copies from it", path, m[1], m[1])
		}
	}
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

var dockerfileFromRefPattern = regexp.MustCompile(`(?im)^\s*FROM\s+(?:--platform=\S+\s+)?(\S+)`)

// The fingerprint hashes tag strings, not digests, so a floating tag drifts under a cached image.
// Pinned tags are the accepted mitigation (root AGENTS.md "Release Rules").
func TestDockerfileBaseImagesUseExplicitPinnedTags(t *testing.T) {
	for _, path := range allDockerfilePaths(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		stages := make(map[string]struct{})
		for _, m := range dockerfileStageNamePattern.FindAllStringSubmatch(text, -1) {
			stages[strings.ToLower(m[1])] = struct{}{}
		}
		for _, m := range dockerfileFromRefPattern.FindAllStringSubmatch(text, -1) {
			ref := m[1]
			if dockerfileFromRefIsPinned(ref, stages) {
				continue
			}
			t.Errorf("%s: FROM %s has no explicit pinned tag — an untagged or \"latest\" base image can mutate upstream with nothing in the repo moving the fingerprint that gates rebuilds", path, ref)
		}
	}
}

func dockerfileFromRefIsPinned(ref string, stages map[string]struct{}) bool {
	lower := strings.ToLower(ref)
	if lower == "scratch" {
		return true
	}
	if _, ok := stages[lower]; ok {
		return true // FROM of a previous stage in this file, not an image
	}
	if strings.Contains(ref, "@sha256:") {
		return true // digest-pinned, strictly stronger than a tag
	}
	nameAndTag := ref
	if lastSlash := strings.LastIndex(ref, "/"); lastSlash >= 0 {
		nameAndTag = ref[lastSlash+1:]
	}
	tag := ""
	if colon := strings.LastIndex(nameAndTag, ":"); colon >= 0 {
		tag = nameAndTag[colon+1:]
	}
	return tag != "" && tag != "latest"
}
