package fingerprint

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The first release on each Node line whose bundled Brotli decoder rejects a malformed block
// switch instead of writing past its buffer. A line absent here never received the fix.
var nodeBrotliFixFloors = map[int]string{
	22: "22.23.4",
	24: "24.22.1",
	26: "26.12.1",
}

// Claude Code compiles in its own Bun runtime and decoder; this is the first release on a fixed Bun.
const claudeCodeBrotliFixFloor = "2.1.308"

var (
	nodeImageTagPattern    = regexp.MustCompile(`(?im)^\s*FROM\s+(?:--platform=\S+\s+)?(?:\S+/)?node:(\S+)`)
	nodeVersionArgPattern  = regexp.MustCompile(`(?im)^\s*ARG\s+NODE_VERSION=(\S+)`)
	claudeCodeArgPattern   = regexp.MustCompile(`(?im)^\s*ARG\s+CLAUDE_CODE_VERSION=(\S+)`)
	leadingVersionPattern  = regexp.MustCompile(`^(\d+)(?:\.(\d+))?(?:\.(\d+))?`)
	nodeTemplateDockerfile = filepath.Join("erun-skills", "skills", "*", "templates", "Dockerfile")
	nodeE2EDockerfile      = filepath.Join("erun-devops", "e2e", "*", "Dockerfile")
)

// Templates are scaffolded into tenant repositories, so a vulnerable pin there spreads.
func nodePinnedDockerfilePaths(t *testing.T) []string {
	t.Helper()
	root := erunRepoRoot(t)
	paths := allDockerfilePaths(t)
	for _, pattern := range []string{nodeTemplateDockerfile, nodeE2EDockerfile} {
		matches, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		paths = append(paths, matches...)
	}
	sort.Strings(paths)
	return paths
}

// A missing minor or patch reads as zero: a tag like node:22.14 resolves to the oldest it names.
func leadingVersion(value string) ([3]int, bool) {
	match := leadingVersionPattern.FindStringSubmatch(strings.TrimPrefix(value, "v"))
	if match == nil {
		return [3]int{}, false
	}
	var version [3]int
	for i, part := range match[1:] {
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return [3]int{}, false
		}
		version[i] = n
	}
	return version, true
}

func versionBelow(version, floor [3]int) bool {
	for i := range version {
		if version[i] != floor[i] {
			return version[i] < floor[i]
		}
	}
	return false
}

func supportedNodeBrotliFixFloors() string {
	floors := make([]string, 0, len(nodeBrotliFixFloors))
	for _, floor := range nodeBrotliFixFloors {
		floors = append(floors, floor)
	}
	sort.Strings(floors)
	return strings.Join(floors, ", ")
}

func TestPinnedNodeRuntimesCarryTheBrotliDecoderFix(t *testing.T) {
	root := erunRepoRoot(t)
	checked := 0
	for _, path := range nodePinnedDockerfilePaths(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		for _, pattern := range []*regexp.Regexp{nodeImageTagPattern, nodeVersionArgPattern} {
			for _, match := range pattern.FindAllStringSubmatch(string(data), -1) {
				checked++
				pinned, ok := leadingVersion(match[1])
				if !ok {
					t.Errorf("%s: cannot read a Node version from %q", rel, match[1])
					continue
				}
				floor, supported := nodeBrotliFixFloors[pinned[0]]
				if !supported {
					t.Errorf("%s pins Node %s, a line that never received the Brotli decoder fix: move to a line at or above %s", rel, match[1], supportedNodeBrotliFixFloors())
					continue
				}
				if minimum, _ := leadingVersion(floor); versionBelow(pinned, minimum) {
					t.Errorf("%s pins Node %s, below %s, the first %d.x release whose bundled Brotli decoder does not write past its buffer on a malformed stream", rel, match[1], floor, pinned[0])
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no Node pin was found in any Dockerfile; the pins moved somewhere this test does not read")
	}
}

func TestPinnedClaudeCodeCarriesTheBrotliDecoderFix(t *testing.T) {
	path := filepath.Join(erunRepoRoot(t), "erun-devops", "docker", "erun-devops", "Dockerfile")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	matches := claudeCodeArgPattern.FindAllStringSubmatch(string(data), -1)
	if len(matches) != 1 {
		t.Fatalf("%s: want exactly one ARG CLAUDE_CODE_VERSION, found %d", path, len(matches))
	}
	pinned, ok := leadingVersion(matches[0][1])
	if !ok {
		t.Fatalf("%s: cannot read a Claude Code version from %q", path, matches[0][1])
	}
	if minimum, _ := leadingVersion(claudeCodeBrotliFixFloor); versionBelow(pinned, minimum) {
		t.Errorf("the runtime image pins Claude Code %s, below %s: earlier releases embed a Bun whose Brotli decoder writes past its buffer on a malformed stream, and its web fetches decode whatever a page sends", matches[0][1], claudeCodeBrotliFixFloor)
	}
}
