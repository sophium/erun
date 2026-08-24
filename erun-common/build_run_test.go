package eruncommon

import "testing"

// The scenario from #1201: a brand-new environment configured to build+push to
// ghcr.io, with no docker config, no gh session, and no GH_TOKEN. Before the
// fix, resolveGHCRBasicAuth's "no credential" case was treated the same as
// "inconclusive" and the release proceeded to spend a full multi-arch build
// before dying at the push. The preflight must refuse before any build runs.
func TestPreflightRegistryPushAccessRefusesBeforeBuildWhenNoCredentialConfigured(t *testing.T) {
	useDockerConfigDir(t, t.TempDir())
	useGHToken(t, func(string) (string, bool) { return "", false })
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	execution := DockerPushExecutionSpec{
		builds: []DockerBuildSpec{{
			Push:  true,
			Image: DockerImageReference{Tag: "ghcr.io/sophium/frs-app:1.0.0"},
		}},
	}

	err := preflightRegistryPushAccess(Context{}, execution)
	if err == nil {
		t.Fatal("expected the preflight to refuse a push with no ghcr.io credential configured")
	}
	if _, ok := err.(*MissingGHCRCredentialError); !ok {
		t.Fatalf("expected *MissingGHCRCredentialError, got %T: %v", err, err)
	}
}

// The same registry credential gap applies to a component chart push, not
// just an image push.
func TestPreflightRegistryPushAccessRefusesChartPushWithNoCredentialConfigured(t *testing.T) {
	useDockerConfigDir(t, t.TempDir())
	useGHToken(t, func(string) (string, bool) { return "", false })
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	execution := DockerPushExecutionSpec{
		componentCharts: []HelmChartPublishSpec{{
			OCIRepo:   "oci://ghcr.io/sophium/charts",
			ChartName: "erun-backend-postgres",
		}},
	}

	err := preflightRegistryPushAccess(Context{}, execution)
	if err == nil {
		t.Fatal("expected the preflight to refuse a chart push with no ghcr.io credential configured")
	}
	if _, ok := err.(*MissingGHCRCredentialError); !ok {
		t.Fatalf("expected *MissingGHCRCredentialError, got %T: %v", err, err)
	}
}

// dry-run does no work, so the preflight must not block the plan preview even
// with no credential configured -- matching how the pre-existing scope and
// create-package checks already skip in dry-run.
func TestPreflightRegistryPushAccessSkipsInDryRun(t *testing.T) {
	useDockerConfigDir(t, t.TempDir())
	useGHToken(t, func(string) (string, bool) { return "", false })
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	execution := DockerPushExecutionSpec{
		builds: []DockerBuildSpec{{
			Push:  true,
			Image: DockerImageReference{Tag: "ghcr.io/sophium/frs-app:1.0.0"},
		}},
	}

	if err := preflightRegistryPushAccess(Context{DryRun: true}, execution); err != nil {
		t.Fatalf("dry-run must not fail on a missing credential, got %v", err)
	}
}
