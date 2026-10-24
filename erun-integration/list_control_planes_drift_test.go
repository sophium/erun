package integration

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testListControlPlanesFailOnDriftBehindPublishedExitsNonZero(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	plane := controlPlaneStub(t, "1.0.245")
	registry := controlPlaneRegistryStub(t, "1.0.246", "1.0.247")
	seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

	result := erun.Run(t, []string{"list", "--control-planes", "--fail-on-drift"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a plane behind published, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "list/control_planes_fail_on_drift_behind_published_exits_non_zero",
		normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
}

func testListControlPlanesFailOnDriftUpToDateExitsZero(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	plane := controlPlaneStub(t, "1.0.247")
	registry := controlPlaneRegistryStub(t, "1.0.247")
	seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

	result := erun.Run(t, []string{"list", "--control-planes", "--fail-on-drift"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("expected exit 0 for a plane already at the published version, got %d:\n%s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/control_planes_fail_on_drift_up_to_date_exits_zero",
		normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
}

func testListControlPlanesFailOnDriftConsoleBehindPublishedExitsNonZero(t *testing.T) {
	// The plane itself is at the published version -- only its linked
	// console is behind. --fail-on-drift must catch console drift the
	// same as plane drift, since a console has no version surface of its
	// own to notice this without the check.
	t.Parallel()
	setup := env.New(t)
	console := versionJSONStub(t, "1.0.245")
	plane := controlPlaneStub(t, "1.0.247", console.URL)
	registry := controlPlaneRegistryStub(t, "1.0.247")
	seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

	result := erun.Run(t, []string{"list", "--control-planes", "--fail-on-drift"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a console behind published, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "list/control_planes_fail_on_drift_console_behind_published_exits_non_zero",
		normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(console, "<CONSOLE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
}

func testListControlPlanesFailOnDriftDocsSiteBehindPublishedExitsNonZero(t *testing.T) {
	// The same one surface over: the plane and its console are both at the
	// published version and the docs site a deploy left behind is the only
	// thing stale. Nothing else in erun reads the docs site's version, so
	// without this leg the drift is invisible.
	t.Parallel()
	setup := env.New(t)
	console := versionJSONStub(t, "1.0.247")
	docs := versionJSONStub(t, "1.0.245")
	plane := controlPlaneStubWithDocs(t, "1.0.247", console.URL, docs.URL)
	registry := controlPlaneRegistryStub(t, "1.0.247")
	seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

	result := erun.Run(t, []string{"list", "--control-planes", "--fail-on-drift"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a docs site behind published, got 0:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "(docs site)") {
		t.Fatalf("expected the summary to name the docs site as the drifting surface:\n%s", result.Combined)
	}
	golden.Equal(t, "list/control_planes_fail_on_drift_docs_site_behind_published_exits_non_zero",
		normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(console, "<CONSOLE_API>"), stubServerRule(docs, "<DOCS_API>"), stubServerRule(registry, "<REGISTRY_API>")))
}

func testListControlPlanesFailOnDriftUnreachablePlaneExitsNonZero(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	registry := controlPlaneRegistryStub(t, "1.0.247")
	seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": "http://127.0.0.1:1"}, registry.URL)

	result := erun.Run(t, []string{"list", "--control-planes", "--fail-on-drift"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an unreachable plane, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "list/control_planes_fail_on_drift_unreachable_plane_exits_non_zero",
		normalize.Apply(result.Combined, stubServerRule(registry, "<REGISTRY_API>")))
}

func testListControlPlanesFailOnDriftDryRunNeverFails(t *testing.T) {
	// Nothing was actually probed, so there is nothing to fail on --
	// --fail-on-drift must not turn a dry-run preview into a failure.
	t.Parallel()
	setup := env.New(t)
	plane := controlPlaneStub(t, "1.0.245")
	registry := controlPlaneRegistryStub(t, "1.0.247")
	seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

	result := erun.Run(t, []string{"list", "--control-planes", "--fail-on-drift", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("expected exit 0 for a dry run, got %d:\n%s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/control_planes_fail_on_drift_dry_run_never_fails",
		normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
}
