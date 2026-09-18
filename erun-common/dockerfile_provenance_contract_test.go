package eruncommon

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A passthrough wrapper's FROM inherits every OCI label the upstream image set,
// including org.opencontainers.image.source. Left alone, the published package
// attributes itself to the vendor's repository and the registry links it there
// instead of to this one, which puts it outside anything repo-scoped —
// erun-devops/AGENTS.md ("Wrapping And Pinning Third-Party Service Images").
// Every wrapper must therefore reassert the label.
//
// The failure is invisible from a build: only the published package's labels
// show it, and a wrapper that keeps inheriting the vendor's value builds and
// pushes exactly like one that overrides it. That is how the rule was written
// down and three wrappers still shipped without it, so this test checks the
// rule against the tree rather than trusting it to be remembered.
//
// Wrappers are discovered rather than listed: a hard-coded list is precisely
// what goes stale, and a wrapper added after the list was written would be
// unchecked by the very test meant to catch it.
func TestPassthroughWrapperDockerfilesReclaimProvenanceLabel(t *testing.T) {
	wrappers := passthroughWrapperDockerfiles(t, repoRootForDockerignoreTest(t))

	// Canary: if the discovery shape stops matching the tree, an empty run must
	// not read as success. Two is a floor, not the expected count — it only has
	// to be low enough to survive wrappers being legitimately added or removed.
	if len(wrappers) < 2 {
		t.Fatalf("discovered only %d passthrough wrapper(s) under erun-devops/docker; the discovery rule no longer matches the tree, so this test checked nothing", len(wrappers))
	}

	for _, path := range wrappers {
		assertWrapperReclaimsProvenance(t, path)
	}
}

// passthroughWrapperDockerfiles returns every Dockerfile that passes an
// upstream image through, which is the set the label rule applies to.
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

// dockerfileIsPassthroughWrapper reports whether a Dockerfile passes an image
// through, which the wrap-and-pin contract defines as docker/<name>/VERSION
// being the upstream pin that the Dockerfile's own ARG default interpolates
// into the FROM. That excludes images that build their own content
// (erun-devops, erun-backend-api, ...) and base images pinned by literal
// (erun-ubuntu), neither of which passes an upstream through and therefore
// neither of which has provenance to reclaim.
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

	// The label has to be the effective one. A LABEL placed before the final
	// FROM, or in an earlier stage, is not what the published image carries:
	// the later FROM's inherited value is what wins.
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

// dockerfileFromInterpolatesVersion reports whether any FROM in the Dockerfile
// interpolates a build ARG — the shape docker/<name>/VERSION feeds through the
// Dockerfile's own ARG default.
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

	// Captures the label's value so it can be checked against this repository,
	// not just its presence: a copied vendor URL would otherwise satisfy a
	// presence-only check while leaving the package attributed elsewhere.
	dockerfileSourceLabelPattern = regexp.MustCompile(`(?im)^[ \t]*LABEL[ \t]+org\.opencontainers\.image\.source[ \t]*=[ \t]*("[^"]*"|'[^']*'|\S+)`)
)
