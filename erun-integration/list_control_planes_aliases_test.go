package integration

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testListControlPlanesRealRunTwoAliasesSharingABackendCollapseIntoOnePlane(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	canonical := controlPlaneStubAt(t, "https://api.example-canonical.test", "1.0.245")
	alias := controlPlaneStubAt(t, "https://api.example-canonical.test", "1.0.245")
	registry := controlPlaneRegistryStub(t, "1.0.247")
	seedControlPlaneConfigOrdered(t, setup, []controlPlaneAliasSeed{
		{Alias: "erun+canonical@erun", APIURL: canonical.URL},
		{Alias: "erun+alias@erun", APIURL: alias.URL},
	}, registry.URL)

	result := erun.Run(t, []string{"list", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "Control planes (1 backend, 2 aliases):") {
		t.Fatalf("expected the two aliases to collapse into one reported backend:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "erun+canonical@erun") || !strings.Contains(result.Combined, "erun+alias@erun") {
		t.Fatalf("expected both configured aliases to remain visible:\n%s", result.Combined)
	}
	golden.Equal(t, "list/control_planes_real_run_two_aliases_sharing_a_backend_collapse_into_one_plane",
		normalize.Apply(result.Combined, stubServerRule(canonical, "<PLANE_API>"), stubServerRule(alias, "<ALIAS_API>"), stubServerRule(registry, "<REGISTRY_API>")))
}

func testListControlPlanesFailOnDriftTwoAliasesSharingABackendCountsOnce(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	canonical := controlPlaneStubAt(t, "https://api.example-canonical.test", "1.0.245")
	alias := controlPlaneStubAt(t, "https://api.example-canonical.test", "1.0.245")
	registry := controlPlaneRegistryStub(t, "1.0.246", "1.0.247")
	seedControlPlaneConfigOrdered(t, setup, []controlPlaneAliasSeed{
		{Alias: "erun+canonical@erun", APIURL: canonical.URL},
		{Alias: "erun+alias@erun", APIURL: alias.URL},
	}, registry.URL)

	result := erun.Run(t, []string{"list", "--control-planes", "--fail-on-drift"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a plane behind published, got 0:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "1 plane(s) behind published") {
		t.Fatalf("expected drift to count one deployment, not one per alias:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "2 plane(s) behind published") {
		t.Fatalf("two aliases of the same backend must not double-count as two planes behind published:\n%s", result.Combined)
	}
	golden.Equal(t, "list/control_planes_fail_on_drift_two_aliases_sharing_a_backend_counts_once",
		normalize.Apply(result.Combined, stubServerRule(canonical, "<PLANE_API>"), stubServerRule(alias, "<ALIAS_API>"), stubServerRule(registry, "<REGISTRY_API>")))
}

func testListControlPlanesRealRunJSONOutputTwoAliasesSharingABackendDoesNotDoubleCount(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	canonical := controlPlaneStubAt(t, "https://api.example-canonical.test", "1.0.245")
	alias := controlPlaneStubAt(t, "https://api.example-canonical.test", "1.0.245")
	registry := controlPlaneRegistryStub(t, "1.0.247")
	seedControlPlaneConfigOrdered(t, setup, []controlPlaneAliasSeed{
		{Alias: "erun+canonical@erun", APIURL: canonical.URL},
		{Alias: "erun+alias@erun", APIURL: alias.URL},
	}, registry.URL)

	result := erun.Run(t, []string{"list", "--control-planes", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, `"additionalAliases"`) {
		t.Fatalf("expected the second alias to appear as an additional alias, not a second plane:\n%s", result.Combined)
	}
	if got := strings.Count(result.Combined, `"reachable": true`); got != 1 {
		t.Fatalf("expected exactly one reachable plane entry in the structured output, got %d:\n%s", got, result.Combined)
	}
}

func testListControlPlanesRealRunTwoAliasesWithNoSelfDeclaredIdentityStaySeparate(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	first := controlPlaneStubAt(t, "", "1.0.245")
	second := controlPlaneStubAt(t, "", "1.0.245")
	registry := controlPlaneRegistryStub(t, "1.0.247")
	seedControlPlaneConfigOrdered(t, setup, []controlPlaneAliasSeed{
		{Alias: "erun+first@erun", APIURL: first.URL},
		{Alias: "erun+second@erun", APIURL: second.URL},
	}, registry.URL)

	result := erun.Run(t, []string{"list", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "Control planes (2 backends, 2 aliases):") {
		t.Fatalf("expected two distinct backends with no self-declared identity to stay separate, not collapse:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "additionalAliases") {
		t.Fatalf("neither alias should be folded into the other as an additional alias:\n%s", result.Combined)
	}
	golden.Equal(t, "list/control_planes_real_run_two_aliases_with_no_self_declared_identity_stay_separate",
		normalize.Apply(result.Combined, stubServerRule(first, "<FIRST_API>"), stubServerRule(second, "<SECOND_API>"), stubServerRule(registry, "<REGISTRY_API>")))
}

func testListControlPlanesRealRunJSONOutputTwoAliasesWithNoSelfDeclaredIdentityStaySeparate(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	first := controlPlaneStubAt(t, "", "1.0.245")
	second := controlPlaneStubAt(t, "", "1.0.245")
	registry := controlPlaneRegistryStub(t, "1.0.247")
	seedControlPlaneConfigOrdered(t, setup, []controlPlaneAliasSeed{
		{Alias: "erun+first@erun", APIURL: first.URL},
		{Alias: "erun+second@erun", APIURL: second.URL},
	}, registry.URL)

	result := erun.Run(t, []string{"list", "--control-planes", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, `"additionalAliases"`) {
		t.Fatalf("expected no additional-alias merge between two distinct, unidentified backends:\n%s", result.Combined)
	}
	if got := strings.Count(result.Combined, `"reachable": true`); got != 2 {
		t.Fatalf("expected two reachable plane entries in the structured output, got %d:\n%s", got, result.Combined)
	}
}

func testListControlPlanesRealRunFlagsAForeignAdvertisedAPIURL(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	plane := controlPlaneStubAt(t, "http://[2001:db8::1]:9999", "1.0.247")
	registry := controlPlaneRegistryStub(t, "1.0.247")
	seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

	result := erun.Run(t, []string{"list", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "advertised apiUrl mismatch") {
		t.Fatalf("expected the plane's foreign advertised apiUrl to be flagged:\n%s", result.Combined)
	}
	golden.Equal(t, "list/control_planes_real_run_flags_a_foreign_advertised_apiurl",
		normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
}

func testListControlPlanesFailOnDriftForeignAdvertisedAPIURLExitsNonZero(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	plane := controlPlaneStubAt(t, "http://[2001:db8::1]:9999", "1.0.247")
	registry := controlPlaneRegistryStub(t, "1.0.247")
	seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

	// The plane is at the published version, so nothing else in this
	// report is drift -- a foreign advertised apiUrl has to fail the run
	// on its own.
	result := erun.Run(t, []string{"list", "--control-planes", "--fail-on-drift"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for a foreign advertised apiUrl, got 0:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "1 plane(s) advertising a foreign apiUrl: erun+test@erun") {
		t.Fatalf("expected the failure to name the plane advertising a foreign apiUrl:\n%s", result.Combined)
	}
	golden.Equal(t, "list/control_planes_fail_on_drift_foreign_advertised_apiurl_exits_non_zero",
		normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
}

func testListControlPlanesRealRunJSONOutputReportsTheAdvertisedAPIURLMismatch(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	plane := controlPlaneStubAt(t, "http://[2001:db8::1]:9999", "1.0.247")
	registry := controlPlaneRegistryStub(t, "1.0.247")
	seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

	result := erun.Run(t, []string{"list", "--control-planes", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, `"advertisedApiUrlMismatch"`) {
		t.Fatalf("expected the mismatch in the structured output:\n%s", result.Combined)
	}
}

func testListControlPlanesRealRunJSONOutputOmitsTheAdvertisedAPIURLMismatchWhenThePlaneAgrees(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	// controlPlaneStub advertises its own listener address, the same one
	// erun dialed -- so the field has to be a signal, not a constant.
	plane := controlPlaneStub(t, "1.0.247")
	registry := controlPlaneRegistryStub(t, "1.0.247")
	seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

	result := erun.Run(t, []string{"list", "--control-planes", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, `"advertisedApiUrlMismatch"`) {
		t.Fatalf("expected no mismatch for a plane advertising the address erun reached:\n%s", result.Combined)
	}
}
