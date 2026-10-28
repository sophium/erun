package fingerprint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The first wrangler release checked to carry no known advisory in its own code or
// its dependency tree. Every 3.x release keeps undici 5, a line that never received
// the fixes, and wrangler itself is fixed only from 3.114.17 and 4.59.1.
const wranglerAdvisoryFixFloor = "4.154.0"

// The npm the node base image bundles carries advisories of its own and nothing in a
// wrangler image runs it once wrangler is installed.
const bundledNPMRemoval = "rm -rf /usr/local/lib/node_modules/npm /usr/local/bin/npm /usr/local/bin/npx"

var (
	wranglerVersionArgPattern = regexp.MustCompile(`(?im)^\s*ARG\s+WRANGLER_VERSION=(\S+)`)
	wranglerInstallPattern    = regexp.MustCompile(`(?i)\bwrangler@(\S+)`)
)

// Templates count too: the docs blueprint scaffolds its Dockerfile into tenant repositories.
func TestPinnedWranglerCarriesTheAdvisoryFixes(t *testing.T) {
	root := erunRepoRoot(t)
	minimum, _ := leadingVersion(wranglerAdvisoryFixFloor)
	checked := 0
	for _, path := range nodePinnedDockerfilePaths(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		text := dockerfileWithoutComments(string(data))
		for _, pattern := range []*regexp.Regexp{wranglerVersionArgPattern, wranglerInstallPattern} {
			for _, match := range pattern.FindAllStringSubmatch(text, -1) {
				if strings.HasPrefix(match[1], "$") {
					continue
				}
				checked++
				pinned, ok := leadingVersion(match[1])
				if !ok {
					t.Errorf("%s: cannot read a wrangler version from %q; pin %s or later", rel, match[1], wranglerAdvisoryFixFloor)
					continue
				}
				if versionBelow(pinned, minimum) {
					t.Errorf("%s pins wrangler %s, below %s, the first release whose own code and dependency tree were checked free of known advisories (3.x ships undici 5, esbuild 0.17 and zod 3 with unfixed ones)", rel, match[1], wranglerAdvisoryFixFloor)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no wrangler version pin was found in any Dockerfile; the pin moved somewhere this test does not read")
	}
}

// The base image's npm is only a lower layer once the last stage removes it.
func TestWranglerImagesShipNoBundledNPM(t *testing.T) {
	root := erunRepoRoot(t)
	checked := 0
	for _, path := range nodePinnedDockerfilePaths(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := dockerfileWithoutComments(string(data))
		if !wranglerInstallPattern.MatchString(text) {
			continue
		}
		checked++
		rel, _ := filepath.Rel(root, path)
		stages := componentStagesOf(text)
		if len(stages) == 0 {
			t.Errorf("%s installs wrangler outside any stage this test can read", rel)
			continue
		}
		last := stages[len(stages)-1]
		if !strings.Contains(strings.Join(last.lines, "\n"), bundledNPMRemoval) {
			t.Errorf("%s installs wrangler but its last stage %q no longer runs %q: the image would ship the base image's npm and the advisories in its bundled dependencies", rel, last.name, bundledNPMRemoval)
		}
	}
	if checked == 0 {
		t.Fatal("no Dockerfile installs wrangler; the install moved somewhere this test does not read")
	}
}
