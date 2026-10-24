package build

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A wrapper inherits the upstream image's org.opencontainers.image.source label, which links the
// package to the vendor's repo; only the published labels show it. Wrappers are discovered, not listed.
func TestPassthroughWrapperDockerfilesReclaimProvenanceLabel(t *testing.T) {
	wrappers := passthroughWrapperDockerfiles(t, repoRootForDockerignoreTest(t))

	// Canary: an empty discovery must not read as success.
	if len(wrappers) < 2 {
		t.Fatalf("discovered only %d passthrough wrapper(s) under erun-devops/docker; the discovery rule no longer matches the tree, so this test checked nothing", len(wrappers))
	}

	for _, path := range wrappers {
		assertWrapperReclaimsProvenance(t, path)
	}
}

func passthroughWrapperDockerfiles(t *testing.T, root string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "erun-devops", "docker", "*", "Dockerfile"))
	if err != nil {
		t.Fatalf("glob Dockerfiles: %v", err)
	}
	sort.Strings(matches)

	var wrappers []string
	for _, path := range matches {
		if dockerfileIsPassthroughWrapper(t, path) {
			wrappers = append(wrappers, path)
		}
	}
	return wrappers
}

// dockerfileIsPassthroughWrapper reports whether docker/<name>/VERSION is the upstream pin
// interpolated into the FROM via the Dockerfile's ARG default.
func dockerfileIsPassthroughWrapper(t *testing.T, path string) bool {
	t.Helper()
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "VERSION")); err != nil {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return dockerfileFromInterpolatesVersion(string(data))
}

func assertWrapperReclaimsProvenance(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(data)

	labels := dockerfileSourceLabelPattern.FindAllStringSubmatchIndex(text, -1)
	if len(labels) == 0 {
		t.Errorf("%s: passthrough wrapper never sets org.opencontainers.image.source, so the published package attributes itself to the upstream's repository instead of this one — add `LABEL org.opencontainers.image.source=https://github.com/sophium/erun` after the FROM", path)
		return
	}

	// Only a LABEL after the final FROM is what the published image carries.
	last := labels[len(labels)-1]
	if fromAt := lastMatchIndex(dockerfileFromLinePattern, text); last[0] < fromAt {
		t.Errorf("%s: org.opencontainers.image.source is set before the final FROM, so the upstream's inherited value is what ships — move the LABEL after the FROM", path)
		return
	}

	value := strings.Trim(strings.TrimSpace(text[last[2]:last[3]]), `"'`)
	if !strings.Contains(value, "github.com/sophium/erun") {
		t.Errorf("%s: org.opencontainers.image.source is %q, which does not name this repository, so the published package is attributed elsewhere", path, value)
	}
}

func dockerfileFromInterpolatesVersion(text string) bool {
	for _, line := range dockerfileFromLinePattern.FindAllString(text, -1) {
		if strings.Contains(line, "${") {
			return true
		}
	}
	return false
}

func lastMatchIndex(re *regexp.Regexp, text string) int {
	last := -1
	for _, loc := range re.FindAllStringIndex(text, -1) {
		last = loc[0]
	}
	return last
}

var (
	dockerfileFromLinePattern = regexp.MustCompile(`(?im)^[ \t]*FROM[ \t]+\S[^\n]*`)

	// Captures the value: a copied vendor URL would pass a presence-only check.
	dockerfileSourceLabelPattern = regexp.MustCompile(`(?im)^[ \t]*LABEL[ \t]+org\.opencontainers\.image\.source[ \t]*=[ \t]*("[^"]*"|'[^']*'|\S+)`)
)
