package fingerprint

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The first release on each nginx line carrying the fixes for the worker-process
// buffer overflows, overreads and use-after-free bugs nginx.org's CHANGES lists for
// that line's earlier releases. A line absent here has not been checked, so pinning
// it fails until it is.
var nginxSecurityFixFloors = map[int]string{
	30: "1.30.6",
}

var nginxImageTagPattern = regexp.MustCompile(`(?im)^\s*FROM\s+(?:--platform=\S+\s+)?(?:\S+/)?nginx:(\S+)`)

func checkedNginxLines() string {
	floors := make([]string, 0, len(nginxSecurityFixFloors))
	for _, floor := range nginxSecurityFixFloors {
		floors = append(floors, floor)
	}
	sort.Strings(floors)
	return strings.Join(floors, ", ")
}

// The console serves the public edge for both its UI and the API it proxies.
func TestPinnedNginxCarriesItsSecurityFixes(t *testing.T) {
	root := erunRepoRoot(t)
	checked := 0
	for _, path := range nodePinnedDockerfilePaths(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		for _, match := range nginxImageTagPattern.FindAllStringSubmatch(dockerfileWithoutComments(string(data)), -1) {
			checked++
			pinned, ok := leadingVersion(match[1])
			if !ok {
				t.Errorf("%s: cannot read an nginx version from nginx:%s", rel, match[1])
				continue
			}
			if pinned[0] != 1 {
				t.Errorf("%s pins nginx:%s, a major this test does not know", rel, match[1])
				continue
			}
			floor, known := nginxSecurityFixFloors[pinned[1]]
			if !known {
				t.Errorf("%s pins nginx:%s, an nginx line whose security fixes have not been checked: use one at or above %s, or check the line and add its floor here", rel, match[1], checkedNginxLines())
				continue
			}
			if minimum, _ := leadingVersion(floor); versionBelow(pinned, minimum) {
				t.Errorf("%s pins nginx:%s, below %s, the first 1.%d release whose worker process carries the fixes for the buffer overflow and use-after-free advisories against the line's earlier releases", rel, match[1], floor, pinned[1])
			}
		}
	}
	if checked == 0 {
		t.Fatal("no nginx image pin was found in any Dockerfile; the pins moved somewhere this test does not read")
	}
}
