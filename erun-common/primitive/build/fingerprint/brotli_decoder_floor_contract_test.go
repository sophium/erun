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

// The first Playwright image built on a fixed Node (24.22.1).
const playwrightImageBrotliFixFloor = "1.64.1"

// The first Zitadel release whose login image runs a fixed Node (24.22.1); core
// moves with it on the shared pin.
const zitadelLoginBrotliFixFloor = "4.20.0"

// Where the decoder comes from a distro package resolved at build time, the image's
// own Dockerfile fails the build without the fix; each entry is that guard.
var brotliDecoderPackageGuards = []struct {
	dockerfile string
	guard      string
}{
	{filepath.Join("erun-devops", "docker", "erun-ubuntu", "Dockerfile"), `dpkg --compare-versions "$(dpkg-query -W -f='${Version}' libbrotli1)" ge 1.1.0-2ubuntu0.1`},
	{filepath.Join("erun-devops", "docker", "erun-powerdns", "Dockerfile"), `dpkg --compare-versions "$(dpkg-query -W -f='${Version}' libbrotli1)" ge 1.1.0-2+deb13u1`},
	{filepath.Join("erun-devops", "docker", "erun-dind", "Dockerfile"), `'brotli-libs>=1.1.1-r0'`},
	{filepath.Join("erun-devops", "docker", "erun-console", "Dockerfile"), `RUN apk info -e musl >/dev/null && ! apk info -e brotli-libs >/dev/null`},
}

var (
	nodeImageTagPattern      = regexp.MustCompile(`(?im)^\s*FROM\s+(?:--platform=\S+\s+)?(?:\S+/)?node:(\S+)`)
	nodeVersionArgPattern    = regexp.MustCompile(`(?im)^\s*ARG\s+NODE_VERSION=(\S+)`)
	claudeCodeArgPattern     = regexp.MustCompile(`(?im)^\s*ARG\s+CLAUDE_CODE_VERSION=(\S+)`)
	playwrightImagePattern   = regexp.MustCompile(`(?im)^\s*FROM\s+(?:--platform=\S+\s+)?mcr\.microsoft\.com/playwright:(\S+)`)
	zitadelVersionArgPattern = regexp.MustCompile(`(?im)^\s*ARG\s+ZITADEL_VERSION=(\S+)`)
	leadingVersionPattern    = regexp.MustCompile(`^(\d+)(?:\.(\d+))?(?:\.(\d+))?`)
	nodeTemplateDockerfile   = filepath.Join("erun-skills", "skills", "*", "templates", "Dockerfile")
	nodeE2EDockerfile        = filepath.Join("erun-devops", "e2e", "*", "Dockerfile")
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

// The e2e suites never ship, but their runners decode the deployed services' responses.
func TestE2EPlaywrightImagesCarryTheBrotliDecoderFix(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(erunRepoRoot(t), nodeE2EDockerfile))
	if err != nil {
		t.Fatalf("glob %s: %v", nodeE2EDockerfile, err)
	}
	if len(paths) == 0 {
		t.Fatalf("no %s found; the erun-devops cli-test stage must copy every e2e suite's Dockerfile", nodeE2EDockerfile)
	}
	minimum, _ := leadingVersion(playwrightImageBrotliFixFloor)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, match := range playwrightImagePattern.FindAllStringSubmatch(string(data), -1) {
			pinned, ok := leadingVersion(match[1])
			if !ok {
				t.Errorf("%s: cannot read a Playwright version from %q", path, match[1])
				continue
			}
			if versionBelow(pinned, minimum) {
				t.Errorf("%s runs on the Playwright image %s, below v%s: earlier images bundle a Node whose Brotli decoder writes past its buffer on a malformed stream", path, match[1], playwrightImageBrotliFixFloor)
			}
		}
	}
}

// The floor tests read e2e Dockerfiles, so a suite cli-test does not copy would be
// checked in a checkout and silently skipped by the build gate.
func TestCLITestStageCopiesEveryE2EDockerfile(t *testing.T) {
	root := erunRepoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "erun-devops", "docker", "erun-devops", "Dockerfile"))
	if err != nil {
		t.Fatalf("read the erun-devops Dockerfile: %v", err)
	}
	paths, err := filepath.Glob(filepath.Join(root, nodeE2EDockerfile))
	if err != nil {
		t.Fatalf("glob %s: %v", nodeE2EDockerfile, err)
	}
	for _, path := range paths {
		suite := filepath.Base(filepath.Dir(path))
		copyLine := "COPY erun-devops/e2e/" + suite + "/Dockerfile /src/erun-devops/e2e/" + suite + "/Dockerfile\n"
		if !strings.Contains(string(data), copyLine) {
			t.Errorf("erun-devops/docker/erun-devops/Dockerfile: the cli-test stage lacks %q, so the build gate never checks that suite's pins", strings.TrimSpace(copyLine))
		}
	}
}

// Docker drops a comment line even inside a continued RUN, so a commented-out
// guard must not count as present.
func dockerfileWithoutComments(text string) string {
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

func TestShippedImagesKeepTheirBrotliDecoderGuards(t *testing.T) {
	root := erunRepoRoot(t)
	for _, entry := range brotliDecoderPackageGuards {
		data, err := os.ReadFile(filepath.Join(root, entry.dockerfile))
		if err != nil {
			t.Fatalf("read %s: %v", entry.dockerfile, err)
		}
		if !strings.Contains(dockerfileWithoutComments(string(data)), entry.guard) {
			t.Errorf("%s no longer carries its Brotli decoder guard %q: a rebuild could ship the vulnerable decoder with nothing failing", entry.dockerfile, entry.guard)
		}
	}
}

func TestZitadelLoginImageCarriesTheBrotliDecoderFix(t *testing.T) {
	path := filepath.Join(erunRepoRoot(t), "erun-devops", "docker", "erun-zitadel-login", "Dockerfile")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	matches := zitadelVersionArgPattern.FindAllStringSubmatch(string(data), -1)
	if len(matches) != 1 {
		t.Fatalf("%s: want exactly one ARG ZITADEL_VERSION, found %d", path, len(matches))
	}
	pinned, ok := leadingVersion(matches[0][1])
	if !ok {
		t.Fatalf("%s: cannot read a Zitadel version from %q", path, matches[0][1])
	}
	if minimum, _ := leadingVersion(zitadelLoginBrotliFixFloor); versionBelow(pinned, minimum) {
		t.Errorf("the login image pins Zitadel %s, below v%s: earlier login images run a Node whose Brotli decoder writes past its buffer on a malformed stream", matches[0][1], zitadelLoginBrotliFixFloor)
	}
}
