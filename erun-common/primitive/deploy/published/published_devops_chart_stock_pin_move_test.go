package published

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
)

func stockPinTarget() config.OpenResult {
	const at = "1.0.282"
	return config.OpenResult{
		Tenant: "erun",
		EnvConfig: config.EnvConfig{
			Name:                "code3",
			RuntimeVersion:      at,
			RuntimeImage:        "ghcr.io/sophium/erun-devops:" + at,
			RuntimeRunningImage: "ghcr.io/sophium/erun-devops:" + at,
			RuntimeChart:        "oci://ghcr.io/sophium/charts/erun-devops:" + at,
			RuntimeRegistry:     "ghcr.io/sophium",
		},
	}
}

func TestDeployMovesStockRuntimePinWithTheDeployVersion(t *testing.T) {
	const version = "1.0.283"
	spec, err := ResolvePublishedDevopsDeploySpecWithReason(core.Context{}, stockPinTarget(), version, "no local runtime chart", "", false)
	if err != nil {
		t.Fatalf("resolving the runtime deploy spec: %v", err)
	}
	const wantImage = "ghcr.io/sophium/erun-devops:" + version
	if got := spec.Deploy.ChartVersion; got != version {
		t.Errorf("ChartVersion = %q, want %q (the deploy must install the chart it records)", got, version)
	}
	if got := spec.Deploy.ResolvedRuntimeImage; got != wantImage {
		t.Errorf("ResolvedRuntimeImage = %q, want %q (the deploy must install the image it records)", got, wantImage)
	}
	if got := spec.Deploy.PersistRuntimeImage; got != wantImage {
		t.Errorf("PersistRuntimeImage = %q, want %q (the pin must move, or the next deploy reads the old one back)", got, wantImage)
	}
	if got, want := spec.Deploy.PersistRuntimeChart, "oci://ghcr.io/sophium/charts/erun-devops:"+version; got != want {
		t.Errorf("PersistRuntimeChart = %q, want %q", got, want)
	}
}

func TestPersistKeepsRecordedVersionAndRunningImageInStep(t *testing.T) {
	const version = "1.0.283"
	spec, err := ResolvePublishedDevopsDeploySpecWithReason(core.Context{}, stockPinTarget(), version, "no local runtime chart", "", false)
	if err != nil {
		t.Fatalf("resolving the runtime deploy spec: %v", err)
	}
	var saved *config.EnvConfig
	persistOrFatal(t, core.Context{}, []helmdeploy.DeploySpec{spec}, capturingSave(new(string), &saved), nil)
	if saved == nil {
		t.Fatalf("the deploy must persist the runtime coordinate it installed")
	}
	if saved.RuntimeVersion != version {
		t.Errorf("RuntimeVersion = %q, want %q", saved.RuntimeVersion, version)
	}
	if got, want := saved.RuntimeImage, "ghcr.io/sophium/erun-devops:"+version; got != want {
		t.Errorf("RuntimeImage = %q, want %q", got, want)
	}
	if got, want := saved.RuntimeChart, "oci://ghcr.io/sophium/charts/erun-devops:"+version; got != want {
		t.Errorf("RuntimeChart = %q, want %q", got, want)
	}
	if got, want := saved.RuntimeRunningImage, "ghcr.io/sophium/erun-devops:"+version; got != want {
		t.Errorf("RuntimeRunningImage = %q, want %q", got, want)
	}
}
