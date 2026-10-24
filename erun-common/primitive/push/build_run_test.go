package push

import (
	"errors"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/helm"
	"github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

// The registry credential preflight covers a component chart push, not just
// an image push.
func TestPreflightRegistryPushAccessRefusesChartPushWithNoCredentialConfigured(t *testing.T) {
	useDockerConfigDir(t, t.TempDir())
	useGHToken(t, func(string) (string, bool) { return "", false })
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	execution := dockerspec.DockerPushExecutionSpec{
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
