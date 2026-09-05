package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// dockerfileBuildVersionLdflagsPattern extracts the -X target the release
// Dockerfile stamps buildVersion through, so this test tracks the actual
// production flag rather than a copy of it that can drift out of sync.
var dockerfileBuildVersionLdflagsPattern = regexp.MustCompile(`-X\s+(\S+\.buildVersion)=\$\{ERUN_VERSION\}`)

// buildEAPIWithLdflags compiles this package with an -overlay that swaps
// main.go for a trivial stub calling currentBuildVersion(), so the real
// buildinfo.go links at its real import path without pulling in the
// dbos/k8s dependencies the real entrypoint needs a live cluster and
// database for.
func buildEAPIWithLdflags(t *testing.T, ldflags string) string {
	t.Helper()

	mainAbsPath, err := filepath.Abs("main.go")
	if err != nil {
		t.Fatalf("resolve main.go path: %v", err)
	}

	stubDir := t.TempDir()
	stubMainPath := filepath.Join(stubDir, "main.go")
	stub := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(currentBuildVersion())\n}\n"
	if err := os.WriteFile(stubMainPath, []byte(stub), 0o644); err != nil {
		t.Fatalf("write stub main.go: %v", err)
	}

	overlayPath := filepath.Join(stubDir, "overlay.json")
	overlay := fmt.Sprintf(`{"Replace": {%q: %q}}`, mainAbsPath, stubMainPath)
	if err := os.WriteFile(overlayPath, []byte(overlay), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}

	binPath := filepath.Join(stubDir, "eapi-ldflags-check")
	args := []string{"build", "-overlay=" + overlayPath}
	if ldflags != "" {
		args = append(args, "-ldflags", ldflags)
	}
	args = append(args, "-o", binPath, ".")
	cmd := exec.Command("go", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build with ldflags %q: %v\n%s", ldflags, err, out)
	}
	return binPath
}

func runEAPIBinary(t *testing.T, binPath string) string {
	t.Helper()
	out, err := exec.Command(binPath).CombinedOutput()
	if err != nil {
		t.Fatalf("run built binary: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestDockerfileLdflagsActuallyStampsTheBinary rebuilds this package's real
// buildinfo.go using the exact -X target the release Dockerfile passes, and
// proves it actually reaches the compiled binary rather than silently
// leaving buildVersion at its "dev" default despite a plausible-looking
// -ldflags line -- the failure mode that shipped a "dev" version to
// production.
func TestDockerfileLdflagsActuallyStampsTheBinary(t *testing.T) {
	dockerfilePath := filepath.Join("..", "..", "..", "..", "erun-devops", "docker", "erun-backend-api", "Dockerfile")
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		t.Fatalf("read %s: %v", dockerfilePath, err)
	}

	match := dockerfileBuildVersionLdflagsPattern.FindSubmatch(data)
	if match == nil {
		t.Fatalf(`%s has no "-X <target>=${ERUN_VERSION}" flag for buildVersion`, dockerfilePath)
	}
	target := string(match[1])

	const injected = "9.9.9-dockerfile-ldflags-test"
	binPath := buildEAPIWithLdflags(t, "-X "+target+"="+injected)
	if got := runEAPIBinary(t, binPath); got != injected {
		t.Fatalf("binary built with the Dockerfile's ldflags target %q reported %q, want %q", target, got, injected)
	}
}

// TestUnstampedEAPIBuildStillReportsDev proves the fallback the test above
// depends on: a build with no -ldflags at all -- a bare `go build`, a dev
// image -- must keep reporting "dev", not a blank or zero version.
func TestUnstampedEAPIBuildStillReportsDev(t *testing.T) {
	binPath := buildEAPIWithLdflags(t, "")
	if got := runEAPIBinary(t, binPath); got != "dev" {
		t.Fatalf("unstamped build reported %q, want %q", got, "dev")
	}
}
