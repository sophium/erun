package eruncommon

import "testing"

// stockPinTarget is the state the reported failure was reproduced in: an environment whose
// runtime coordinates are all on erun's own release line and all at 1.0.282,
// with a deploy asking for 1.0.283. Every field names one coordinate, so a
// deploy that installs the older artifacts while recording the newer version
// reports a roll that did not happen.
func stockPinTarget() OpenResult {
	const at = "1.0.282"
	return OpenResult{
		Tenant: "erun",
		EnvConfig: EnvConfig{
			Name:                "code3",
			RuntimeVersion:      at,
			RuntimeImage:        "ghcr.io/sophium/erun-devops:" + at,
			RuntimeRunningImage: "ghcr.io/sophium/erun-devops:" + at,
			RuntimeChart:        "oci://ghcr.io/sophium/charts/erun-devops:" + at,
			RuntimeRegistry:     "ghcr.io/sophium",
		},
	}
}

// TestDeployMovesStockRuntimePinWithTheDeployVersion is the reproduction of the
// reported failure: a deploy of 1.0.283 onto an environment pinned at 1.0.282 on
// erun's own line must install 1.0.283, not honor the older chart and image while
// recording the newer version.
func TestDeployMovesStockRuntimePinWithTheDeployVersion(t *testing.T) {
	const version = "1.0.283"
	spec, err := resolvePublishedDevopsDeploySpecWithReason(Context{}, stockPinTarget(), version, "no local runtime chart", "", false)
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

// TestStockRuntimePinMoveLeavesADeliberateCoordinateAlone is the other half of
// the same decision: a tenant that publishes a devops image of its own runs its
// components on its own version line, which is exactly why it states its runtime
// chart separately. There the stated version is the operator's coordinate and
// must not move onto the deploy version.
func TestStockRuntimePinMoveLeavesADeliberateCoordinateAlone(t *testing.T) {
	target := OpenResult{
		Tenant: "frs",
		EnvConfig: EnvConfig{
			Name:                "prod",
			RuntimeVersion:      "1.0.86",
			RuntimeImage:        "ghcr.io/sophium/frs-devops:1.0.86",
			RuntimeRunningImage: "ghcr.io/sophium/frs-devops:1.0.86",
			RuntimeChart:        "oci://ghcr.io/sophium/charts/erun-devops:1.0.30",
			RuntimeRegistry:     "ghcr.io/sophium",
		},
	}
	spec, err := resolvePublishedDevopsDeploySpecWithReason(Context{}, target, "1.0.86", "no local runtime chart", "", false)
	if err != nil {
		t.Fatalf("resolving the runtime deploy spec: %v", err)
	}
	if got := spec.Deploy.ChartVersion; got != "1.0.30" {
		t.Errorf("ChartVersion = %q, want the stated 1.0.30 (a tenant with its own line states this coordinate on purpose)", got)
	}
	if got := spec.Deploy.PersistRuntimeChart; got != "" {
		t.Errorf("PersistRuntimeChart = %q, want empty (nothing was moved)", got)
	}
}

// TestPersistKeepsRecordedVersionAndRunningImageInStep closes the reported loop:
// after the deploy persists, the recorded runtime version and the image that
// version describes are the same coordinate, and `erun list`'s runtime-version
// is the version the pods run.
func TestPersistKeepsRecordedVersionAndRunningImageInStep(t *testing.T) {
	const version = "1.0.283"
	spec, err := resolvePublishedDevopsDeploySpecWithReason(Context{}, stockPinTarget(), version, "no local runtime chart", "", false)
	if err != nil {
		t.Fatalf("resolving the runtime deploy spec: %v", err)
	}
	var saved *EnvConfig
	persistOrFatal(t, Context{}, []DeploySpec{spec}, capturingSave(new(string), &saved), nil)
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
