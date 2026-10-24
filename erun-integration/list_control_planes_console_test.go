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
	// The plane itself is reachable and reports a consoleUrl nothing
	// listens on -- a plane and its console can go down independently,
	// and the console must never be reported current just because its
	// plane is.
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
	// A console that answers GET /version.json with 200 text/html (the
	// SPA's own index.html, served by an nginx fallback that doesn't yet
	// exact-match the route) must be reported reachable -- it answered
	// -- with an unknown version and a reason naming the status code
	// and content type, never reachable=no.
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

// The docs site is its own surface, not a second view of the console: it
// is published by its own deploy, so a docs site left behind by that
// deploy is invisible unless it is compared on its own. Here the plane and
// its console are both current and only the docs site lags.
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
	// Read the docs entry's own line: the console's line carries the same
	// verdict text, so matching the whole document would pass on it.
	if !strings.Contains(docsLine, "[behind published -- roll it]") {
		t.Fatalf("expected the docs site to be flagged behind while the plane and console are current:\n%s", docsLine)
	}
	golden.Equal(t, "list/control_planes_real_run_reports_a_docs_site_behind_published",
		normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(console, "<CONSOLE_API>"), stubServerRule(docs, "<DOCS_API>"), stubServerRule(registry, "<REGISTRY_API>")))
}

// A plane that advertises no docsUrl has no docs site to check, and that
// has to read as absent rather than as current: reporting a surface erun
// never reached as up to date is the same fail-open shape as reporting an
// unreachable console as current.
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
	// The console was advertised as empty here, so a "console" key in this
	// document would be the same absence-reported-as-current mistake one
	// field over.
	if strings.Contains(result.Combined, `"console"`) {
		t.Fatalf("expected no console entry for a plane that advertises none:\n%s", result.Combined)
	}
	for _, want := range []string{`"docs"`, `"url": "` + docs.URL + `"`, `"behind": true`} {
		if !strings.Contains(result.Combined, want) {
			t.Fatalf("expected %q in JSON output:\n%s", want, result.Combined)
		}
	}
}
