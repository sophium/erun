package fingerprint

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The oldest Alpine branch whose main repository still receives security fixes:
// 3.20 reached end of life on 2026-04-01 and 3.21 does on 2026-11-01
// (alpinelinux.org/releases.json). An end-of-life branch gets no further package
// fixes, so an image built on one keeps every advisory published after that date.
const alpineSupportedBranchFloor = "3.22"

var (
	alpineImageTagPattern = regexp.MustCompile(`(?im)^\s*FROM\s+(?:--platform=\S+\s+)?(?:\S+/)?alpine:(\S+)`)
	// An upstream image whose tag names the Alpine branch it is built on, such as nginx:1.30.6-alpine3.24-slim.
	alpineBranchInTagPattern = regexp.MustCompile(`(?im)^\s*FROM\s+(?:--platform=\S+\s+)?\S+:\S*?-alpine(\d+\.\d+)`)
)

// Build and fetch stages count too: they install packages from the same branch.
func TestPinnedAlpineBranchesStillReceiveSecurityFixes(t *testing.T) {
	root := erunRepoRoot(t)
	minimum, _ := leadingVersion(alpineSupportedBranchFloor)
	checked := 0
	for _, path := range nodePinnedDockerfilePaths(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		text := dockerfileWithoutComments(string(data))
		for _, pattern := range []*regexp.Regexp{alpineImageTagPattern, alpineBranchInTagPattern} {
			for _, match := range pattern.FindAllStringSubmatch(text, -1) {
				checked++
				pinned, ok := leadingVersion(match[1])
				if !ok {
					t.Errorf("%s: cannot read an Alpine branch from %q; pin a supported branch, at or above %s", rel, match[1], alpineSupportedBranchFloor)
					continue
				}
				if pinned[0] != 3 {
					t.Errorf("%s pins Alpine %s, a major this test does not know: check its support status and update the floor", rel, match[1])
					continue
				}
				if branch := [3]int{pinned[0], pinned[1], 0}; versionBelow(branch, minimum) {
					t.Errorf("%s pins Alpine %d.%d, an end-of-life branch below %s: its packages get no more security fixes", rel, pinned[0], pinned[1], alpineSupportedBranchFloor)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no Alpine image pin was found in any Dockerfile; the pins moved somewhere this test does not read")
	}
}
