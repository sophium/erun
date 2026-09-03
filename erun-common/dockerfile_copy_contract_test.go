package eruncommon

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// realDockerfilePaths returns every Dockerfile erun actually builds from —
// erun-devops/docker/<component>/Dockerfile — excluding the erun-skills
// scaffolding templates, which use __PLACEHOLDER__ tokens instead of real
// paths and are never fed to computeBuildFingerprint.
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

var dockerfileAddPattern = regexp.MustCompile(`(?im)^\s*ADD\s+`)

// TestDockerfilesNeverUseAddForLocalContent locks the fact that no Dockerfile
// uses ADD today. dockerfileCopySources (build_incremental.go) parses only
// COPY instructions (parseDockerfileCopyInstructions matches literal "COPY"),
// so an ADD source's local file/dir would never become a fingerprint input —
// unlike the mirrored-.gitignore bug, this would not even self-correct once:
// the Dockerfile text change that introduces the ADD line moves the
// fingerprint once, but every later edit to the ADDed content afterward would
// not, since that content was never added to computeBuildFingerprint's source
// list. Introducing ADD is a deliberate decision that must first teach
// dockerfileCopySources to parse it, not a silent gap this test lets slide.
func TestDockerfilesNeverUseAddForLocalContent(t *testing.T) {
	for _, path := range realDockerfilePaths(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if dockerfileAddPattern.MatchString(string(data)) {
			t.Errorf("%s uses ADD: computeBuildFingerprint only parses COPY instructions, so an ADD source's content would never enter the build fingerprint even though the real docker build copies it — teach dockerfileCopySources to parse ADD before using it here", path)
		}
	}
}

// TestDockerfileCopySourcesContainNoGlobs locks the fact that no COPY source
// uses a shell-style glob today. collectFingerprintFiles (build_incremental.go)
// resolves each source with os.Lstat on the literal string; a glob like
// "*.json" does not exist as a literal path, so os.ErrNotExist is treated as
// "nothing to hash" and the source is silently dropped — while the real
// docker build expands the glob and copies every match. That is the fail-open
// shape this whole audit is about: files enter the image without ever moving
// the fingerprint that decides whether it rebuilds.
func TestDockerfileCopySourcesContainNoGlobs(t *testing.T) {
	root := repoRootForDockerignoreTest(t)
	for _, path := range realDockerfilePaths(t) {
		sources, err := dockerfileCopySources(path, root)
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

// TestDockerfileCopyFromReferencesOnlyLocalStages locks the assumption behind
// filterDockerfileCopyArgs (build_incremental.go): every "COPY --from=X" is
// treated unconditionally as a reference to a build stage in the same file
// and dropped from the fingerprint entirely, since a stage's content is
// already governed by that stage's own FROM/COPY chain. That assumption holds
// only while X is genuinely a declared stage (or a numeric stage index) — if
// X ever named an external image instead, that image's content would become
// an unfingerprinted build input with no cascade to catch it (dockerfileLocalBaseImageDeps
// only walks FROM lines, never COPY --from).
func TestDockerfileCopyFromReferencesOnlyLocalStages(t *testing.T) {
	for _, path := range realDockerfilePaths(t) {
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

// TestDockerfileBaseImagesUseExplicitPinnedTags enforces the one part of the
// base-image-drift risk a fast, static test actually can. computeBuildFingerprint
// hashes the Dockerfile's own bytes, so it catches a base image reference
// changing in the Dockerfile text — but it hashes the tag string, never the
// digest that tag resolves to, so a mutable upstream tag (a registry
// republishing e.g. alpine:3.20 under the same tag) moves underneath an
// already-cached fingerprint with nothing to catch it. Because a
// fingerprint-matched build promotes a previously-built image instead of ever
// invoking docker build again, that drift ships with no rebuild at all.
// Resolving every base image's live digest on every build would close it
// fully, but costs a network round-trip this repo's caching model exists to
// avoid paying (see root AGENTS.md's ~9-minute figure) — so the accepted
// mitigation is pinned-tag discipline (root AGENTS.md "Release Rules"), and
// this test is what keeps that discipline from regressing silently.
func TestDockerfileBaseImagesUseExplicitPinnedTags(t *testing.T) {
	for _, path := range realDockerfilePaths(t) {
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

// dockerfileFromRefIsPinned reports whether a FROM reference is either not a
// real external image (scratch, or a previous stage in the same file) or
// names an explicit tag/digest rather than a floating "latest".
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
