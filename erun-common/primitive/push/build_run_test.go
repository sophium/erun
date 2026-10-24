package push

import (
	"errors"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/helm"
	"github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// A brand-new environment configured to build+push to ghcr.io, with no docker
// config, no gh session, and no GH_TOKEN: resolveGHCRBasicAuth's "no
// credential" case is not "inconclusive", so the preflight must refuse before
// any build runs.
func TestPreflightRegistryPushAccessRefusesBeforeBuildWhenNoCredentialConfigured(t *testing.T) {
	useDockerConfigDir(t, t.TempDir())
	useGHToken(t, func(string) (string, bool) { return "", false })
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	execution := shared.DockerPushExecutionSpec{
		Builds: []shared.DockerBuildSpec{{
			Push:  true,
			Image: helm.DockerImageReference{Tag: "ghcr.io/sophium/frs-app:1.0.0"},
		}},
	}

	err := PreflightRegistryPushAccess(core.Context{}, execution)
	if err == nil {
		t.Fatal("expected the preflight to refuse a push with no ghcr.io credential configured")
	}
	var missing *registry.MissingGHCRCredentialError
	if !errors.As(err, &missing) {
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

	execution := shared.DockerPushExecutionSpec{
		ComponentCharts: []helm.HelmChartPublishSpec{{
			OCIRepo:   "oci://ghcr.io/sophium/charts",
			ChartName: "erun-backend-postgres",
		}},
	}

	err := PreflightRegistryPushAccess(core.Context{}, execution)
	if err == nil {
		t.Fatal("expected the preflight to refuse a chart push with no ghcr.io credential configured")
	}
	var missing *registry.MissingGHCRCredentialError
	if !errors.As(err, &missing) {
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

	execution := shared.DockerPushExecutionSpec{
		Builds: []shared.DockerBuildSpec{{
			Push:  true,
			Image: helm.DockerImageReference{Tag: "ghcr.io/sophium/frs-app:1.0.0"},
		}},
	}

	if err := PreflightRegistryPushAccess(core.Context{DryRun: true}, execution); err != nil {
		t.Fatalf("dry-run must not fail on a missing credential, got %v", err)
	}
}
