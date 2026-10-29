package fingerprint

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Helper images erun starts itself rather than build: the build-daemon forwarder
// and the privileged binfmt installer the dind pod runs. Each floor is the first
// release that scans clean of the advisories its predecessor carried.
var helperImageFloors = []struct {
	file    string
	pattern *regexp.Regexp
	floor   string
	why     string
}{
	{
		filepath.Join("erun-common", "primitive", "build", "run", "build_daemon_endpoint.go"),
		regexp.MustCompile(`"alpine/socat:([0-9][^"]*)"`),
		"1.8.1",
		"earlier tags ship socat and Alpine packages with fixed advisories",
	},
	{
		filepath.Join("erun-devops", "k8s", "erun-devops", "templates", "service.yaml"),
		regexp.MustCompile(`tonistiigi/binfmt:qemu-v(\S+)`),
		"10.2.3",
		"earlier installers carry a CRITICAL Go stdlib advisory and BuildKit's CVE-2026-33747/33748, and run privileged",
	},
}

func TestHelperImagesPinPastTheirAdvisories(t *testing.T) {
	root := erunRepoRoot(t)
	for _, entry := range helperImageFloors {
		data, err := os.ReadFile(filepath.Join(root, entry.file))
		if err != nil {
			t.Fatalf("read %s: %v", entry.file, err)
		}
		matches := entry.pattern.FindAllStringSubmatch(string(data), -1)
		if len(matches) == 0 {
			t.Errorf("%s: no image matching %s; the pin moved somewhere this test does not read", entry.file, entry.pattern)
			continue
		}
		minimum, _ := leadingVersion(entry.floor)
		for _, match := range matches {
			pinned, ok := leadingVersion(match[1])
			if !ok {
				t.Errorf("%s: cannot read a version from %q", entry.file, match[0])
				continue
			}
			if versionBelow(pinned, minimum) {
				t.Errorf("%s pins %s, below %s: %s", entry.file, match[0], entry.floor, entry.why)
			}
		}
	}
}
