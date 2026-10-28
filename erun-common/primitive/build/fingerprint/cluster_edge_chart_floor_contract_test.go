package fingerprint

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The first chart each cluster-edge default may pin. A default below its floor brings back
// the advisories its images carried; a newer chart carries the same fixes unless a rescan
// says otherwise.
var clusterEdgeChartSecurityFloors = []struct {
	variable, floor, fixes string
}{
	{"traefik_chart_version", "42.1.1", "the Traefik v3.2.2 advisories (GHSA-cxjq-mrr5-89rv, GHSA-5423-jcjm-2gpv and GHSA-qqjf-53cj-pwvv among them) and the Alpine and Go standard library findings of every image before Traefik v3.7.16"},
	{"cert_manager_chart_version", "v1.20.5", "the Go standard library, x/net, x/crypto, x/text, grpc, OpenTelemetry, go-jose, cel-go and go-pkcs12 findings of the v1.20.3 images (the v1.20.4 controller still carries 27)"},
}

var clusterEdgeMainTF = filepath.Join("erun-devops", "terraform-erun", "modules", "terraform-erun-cluster-edge", "main.tf")

func terraformWithoutLineComments(text string) string {
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "//") {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// The module resolves a null input to its default in one local, so that literal is the pin.
func clusterEdgeDefault(t *testing.T, variable string) string {
	t.Helper()
	pattern := regexp.MustCompile(`(?m)^\s*arg_\w+\s*=\s*var\.` + regexp.QuoteMeta(variable) + `\s*==\s*null\s*\?\s*"([^"]+)"`)
	matches := pattern.FindAllStringSubmatch(terraformWithoutLineComments(readRepoFile(t, clusterEdgeMainTF)), -1)
	if len(matches) != 1 {
		t.Fatalf("%s: want exactly one default for var.%s, found %d; the pin moved somewhere this test does not read", clusterEdgeMainTF, variable, len(matches))
	}
	return matches[0][1]
}

// Reads the Terraform module: the build runs it with -count=1.
func TestClusterEdgeChartDefaultsPinPastTheirAdvisories(t *testing.T) {
	for _, entry := range clusterEdgeChartSecurityFloors {
		requirePinAtOrAbove(t, clusterEdgeMainTF+" "+entry.variable, clusterEdgeDefault(t, entry.variable), entry.floor, entry.fixes)
	}
}

// Traefik resolves a named redirect target to that entrypoint's listen port, and the chart
// listens on :8443 behind a Service that publishes 443.
func TestClusterEdgeRedirectLandsOnThePublishedHTTPSPort(t *testing.T) {
	pattern := regexp.MustCompile(`redirections\.entryPoint\.to=([^"\s]*)`)
	matches := pattern.FindAllStringSubmatch(terraformWithoutLineComments(readRepoFile(t, clusterEdgeMainTF)), -1)
	if len(matches) != 1 {
		t.Fatalf("%s: want exactly one entrypoint redirect target, found %d; the redirect moved somewhere this test does not read", clusterEdgeMainTF, len(matches))
	}
	if target := matches[0][1]; target != ":443" {
		t.Errorf("%s redirects plaintext to %q, want \":443\": a named entrypoint redirects to its listen port (:8443 in the Traefik chart), so every http visitor is sent to https://<host>:8443/, which nothing outside the pod reaches", clusterEdgeMainTF, target)
	}
}
