package fingerprint

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The first release on each Go line whose standard library carries every fix
// govulncheck reports against the code these images compile. A line absent here
// has not been checked, so pinning it fails until it is.
var goToolchainSecurityFloors = map[int]string{
	26: "1.26.10",
	27: "1.27.3",
}

var golangImageTagPattern = regexp.MustCompile(`(?im)^\s*FROM\s+(?:--platform=\S+\s+)?(?:\S+/)?golang:(\S+)`)

func checkedGoToolchainLines() string {
	floors := make([]string, 0, len(goToolchainSecurityFloors))
	for _, floor := range goToolchainSecurityFloors {
		floors = append(floors, floor)
	}
	sort.Strings(floors)
	return strings.Join(floors, ", ")
}

// Every Go binary erun ships, and the toolchain the runtime image carries, is
// built by one of these images, so its standard library is the one pinned here.
func TestPinnedGoToolchainsCarryTheStandardLibraryFixes(t *testing.T) {
	root := erunRepoRoot(t)
	checked := 0
	for _, path := range nodePinnedDockerfilePaths(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		for _, match := range golangImageTagPattern.FindAllStringSubmatch(dockerfileWithoutComments(string(data)), -1) {
			checked++
			pinned, ok := leadingVersion(match[1])
			if !ok {
				t.Errorf("%s: cannot read a Go version from golang:%s", rel, match[1])
				continue
			}
			if pinned[0] != 1 {
				t.Errorf("%s pins golang:%s, a major this test does not know", rel, match[1])
				continue
			}
			floor, known := goToolchainSecurityFloors[pinned[1]]
			if !known {
				t.Errorf("%s pins golang:%s, a Go line whose standard library fixes have not been checked: use one at or above %s, or check the line and add its floor here", rel, match[1], checkedGoToolchainLines())
				continue
			}
			if minimum, _ := leadingVersion(floor); versionBelow(pinned, minimum) {
				t.Errorf("%s pins golang:%s, below %s, the first 1.%d release whose standard library carries the fixes for the crypto/tls, crypto/x509, net/http, net/url and encoding advisories that reach erun's binaries", rel, match[1], floor, pinned[1])
			}
		}
	}
	if checked == 0 {
		t.Fatal("no golang image pin was found in any Dockerfile; the pins moved somewhere this test does not read")
	}
}
