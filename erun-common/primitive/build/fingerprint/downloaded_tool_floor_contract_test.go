package fingerprint

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The release each downloaded tool's pin moved to because a scan showed it drops
// advisories the previous pin carried. A pin below its floor brings them back; a
// newer release carries the same fixes unless a rescan says otherwise.
var downloadedToolSecurityFloors = []struct {
	arg, floor, fixes string
}{
	{"KUBECTL_VERSION", "1.36.6", "the Go 1.24 standard library, x/net, x/text, spdystream and Kubernetes advisories of the end-of-life 1.33 line"},
	{"HELM_VERSION", "4.3.1", "the Go 1.26.4 standard library, x/crypto, x/net, x/text and oras-go advisories"},
	{"TERRAFORM_VERSION", "1.16.7", "the Go 1.24 standard library, x/crypto, x/net, grpc, go-getter and OpenTelemetry advisories"},
	{"K9S_VERSION", "0.51.0", "the Go 1.24 standard library, x/crypto, x/net, grpc, go-git, containerd and docker module advisories"},
	{"DIFFT_VERSION", "0.70.0", "RUSTSEC-2026-0204 in its locked crossbeam-epoch"},
	{"GH_VERSION", "2.102.1", "the Go 1.24 standard library, x/crypto, x/net, grpc, sigstore and gh's own advisories"},
	{"RIPGREP_VERSION", "15.2.0", "RUSTSEC-2026-0186, -0190 and -0204 in its locked memmap2, anyhow and crossbeam-epoch"},
	{"AWS_CLI_VERSION", "2.38.5", "its bundled OpenSSL 1.1.1zd, wheel 0.45.1 and urllib3 1.26.20 advisories"},
	{"ATLAS_VERSION", "1.3.5", "the Go 1.26.1 standard library, x/crypto, x/net, x/text, x/mod and grpc advisories"},
	{"CODEX_VERSION", "0.165.0", "the rustls, rmcp, quinn-proto, gix, h2 and zip advisories in its locked crates"},
	{"NPM_VERSION", "11.21.1", "the brace-expansion 5.0.9, ip-address 10.5.0, postcss-selector-parser 7.1.4 and undici 6.28.0 advisories Node 24.22.1's bundled npm carries"},
}

func readDockerfileWithoutComments(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return dockerfileWithoutComments(string(data))
}

// dockerfileArgDefaults returns every default given to ARG name, keyed by the Dockerfile that gives it.
func dockerfileArgDefaults(t *testing.T, name string) map[string][]string {
	t.Helper()
	root := erunRepoRoot(t)
	pattern := regexp.MustCompile(`(?m)^\s*ARG\s+` + regexp.QuoteMeta(name) + `=(\S+)`)
	defaults := map[string][]string{}
	for _, path := range allDockerfilePaths(t) {
		rel, _ := filepath.Rel(root, path)
		for _, match := range pattern.FindAllStringSubmatch(readDockerfileWithoutComments(t, path), -1) {
			defaults[rel] = append(defaults[rel], match[1])
		}
	}
	return defaults
}

func TestDownloadedToolPinsStayAtOrAboveTheirSecurityFloors(t *testing.T) {
	for _, tool := range downloadedToolSecurityFloors {
		minimum, _ := leadingVersion(tool.floor)
		defaults := dockerfileArgDefaults(t, tool.arg)
		if len(defaults) == 0 {
			t.Errorf("no Dockerfile pins ARG %s=<version>; the pin moved somewhere this test does not read", tool.arg)
		}
		files := make([]string, 0, len(defaults))
		for rel := range defaults {
			files = append(files, rel)
		}
		sort.Strings(files)
		for _, rel := range files {
			for _, value := range defaults[rel] {
				pinned, ok := leadingVersion(value)
				if !ok {
					t.Errorf("%s: cannot read a version from ARG %s=%s", rel, tool.arg, value)
					continue
				}
				if versionBelow(pinned, minimum) {
					t.Errorf("%s pins %s=%s, below %s, the release that drops %s", rel, tool.arg, value, tool.floor, tool.fixes)
				}
			}
		}
	}
}

// The cli-test suites run the atlas the runtime image ships, and erun-backend-db migrates
// with it too, so every pin names one release and one pair of digests.
func TestAtlasPinsAgreeAcrossDockerfiles(t *testing.T) {
	for _, arg := range []string{"ATLAS_VERSION", "ATLAS_SHA256_AMD64", "ATLAS_SHA256_ARM64"} {
		pinnedBy := map[string][]string{}
		for rel, values := range dockerfileArgDefaults(t, arg) {
			for _, value := range values {
				pinnedBy[value] = append(pinnedBy[value], rel)
			}
		}
		if len(pinnedBy) == 1 {
			continue
		}
		var pins []string
		for value, files := range pinnedBy {
			sort.Strings(files)
			pins = append(pins, value+" ("+strings.Join(files, ", ")+")")
		}
		sort.Strings(pins)
		t.Errorf("ARG %s must have exactly one default across the Dockerfiles, found %d: %s", arg, len(pins), strings.Join(pins, "; "))
	}
}
