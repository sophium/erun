package integration

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testListControlPlanesRealRunReportsAConsoleBehindPublished(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	console := versionJSONStub(t, "1.0.245")
	plane := controlPlaneStub(t, "1.0.247", console.URL)
	registry := controlPlaneRegistryStub(t, "1.0.247")
	seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

	result := erun.Run(t, []string{"list", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/control_planes_real_run_reports_a_console_behind_published",
		normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(console, "<CONSOLE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
}

func testListControlPlanesRealRunReportsAConsoleAheadOfPublished(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	console := versionJSONStub(t, "1.0.999")
	plane := controlPlaneStub(t, "1.0.247", console.URL)
	registry := controlPlaneRegistryStub(t, "1.0.247")
	seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

	result := erun.Run(t, []string{"list", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/control_planes_real_run_reports_a_console_ahead_of_published",
		normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(console, "<CONSOLE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
}

func testListControlPlanesRealRunReportsAnUnreachableConsoleAsNotCurrent(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	// The plane is reachable but its console is not; the console must not
	// inherit the plane's verdict.
	plane := controlPlaneStub(t, "1.0.247", "http://127.0.0.1:1")
	registry := controlPlaneRegistryStub(t, "1.0.247")
	seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

	result := erun.Run(t, []string{"list", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/control_planes_real_run_reports_an_unreachable_console_as_not_current",
		normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
}

func testListControlPlanesRealRunReportsAConsoleServingHTMLAsReachableWithUnknownVersion(t *testing.T) {
	// An SPA fallback (200 text/html) answered, so it is reachable with an
	// unknown version, never reachable=no.
	t.Parallel()
	setup := env.New(t)
	console := versionJSONHTMLFallbackStub(t)
	plane := controlPlaneStub(t, "1.0.247", console.URL)
	registry := controlPlaneRegistryStub(t, "1.0.247")
	seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

	result := erun.Run(t, []string{"list", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	consoleLineIdx := strings.Index(result.Combined, "console: url=")
	if consoleLineIdx == -1 {
		t.Fatalf("expected a console entry:\n%s", result.Combined)
	}
	consoleLine := result.Combined[consoleLineIdx:]
	if nl := strings.IndexByte(consoleLine, '\n'); nl != -1 {
		consoleLine = consoleLine[:nl]
	}
	if !strings.Contains(consoleLine, "reachable=yes") {
		t.Fatalf("expected the console to be reported reachable despite the wrong content type:\n%s", consoleLine)
	}
	if !strings.Contains(consoleLine, "version=unknown") {
		t.Fatalf("expected the console's version to be reported unknown:\n%s", consoleLine)
	}
	if !strings.Contains(consoleLine, "/version.json returned 200 text/html (expected application/json)") {
		t.Fatalf("expected the reason to name the status code and content type:\n%s", consoleLine)
	}
	golden.Equal(t, "list/control_planes_real_run_reports_a_console_serving_html_as_reachable_with_unknown_version",
		normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(console, "<CONSOLE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
}

func testListControlPlanesRealRunReportsADocsSiteBehindPublished(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	console := versionJSONStub(t, "1.0.247")
	docs := versionJSONStub(t, "1.0.245")
	plane := controlPlaneStubWithDocs(t, "1.0.247", console.URL, docs.URL)
	registry := controlPlaneRegistryStub(t, "1.0.247")
	seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

	result := erun.Run(t, []string{"list", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	docsLine := lineContaining(result.Combined, "docs site: url=")
	if docsLine == "" {
		t.Fatalf("expected a docs-site entry:\n%s", result.Combined)
	}
	// The console's line carries the same verdict text, so match the docs line only.
	if !strings.Contains(docsLine, "[behind published -- roll it]") {
		t.Fatalf("expected the docs site to be flagged behind while the plane and console are current:\n%s", docsLine)
	}
	golden.Equal(t, "list/control_planes_real_run_reports_a_docs_site_behind_published",
		normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(console, "<CONSOLE_API>"), stubServerRule(docs, "<DOCS_API>"), stubServerRule(registry, "<REGISTRY_API>")))
}

// No advertised docsUrl must read as absent, not current.
func testListControlPlanesRealRunReportsNoDocsSiteWhenThePlaneHasNone(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	plane := controlPlaneStub(t, "1.0.247")
	registry := controlPlaneRegistryStub(t, "1.0.247")
	seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

	result := erun.Run(t, []string{"list", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "docs site") {
		t.Fatalf("expected no docs entry for a plane that advertises none, not a current-looking one:\n%s", result.Combined)
	}
	golden.Equal(t, "list/control_planes_real_run_reports_no_docs_site_when_the_plane_has_none",
		normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
}

func testListControlPlanesRealRunJSONOutputReportsTheDocsSite(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	docs := versionJSONStub(t, "1.0.245")
	plane := controlPlaneStubWithDocs(t, "1.0.247", "", docs.URL)
	registry := controlPlaneRegistryStub(t, "1.0.247")
	seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

	result := erun.Run(t, []string{"list", "--control-planes", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, `"console"`) {
		t.Fatalf("expected no console entry for a plane that advertises none:\n%s", result.Combined)
	}
	for _, want := range []string{`"docs"`, `"url": "` + docs.URL + `"`, `"behind": true`} {
		if !strings.Contains(result.Combined, want) {
			t.Fatalf("expected %q in JSON output:\n%s", want, result.Combined)
		}
	}
}
