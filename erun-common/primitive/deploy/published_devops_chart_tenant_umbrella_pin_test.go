package deploy

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// The moving case is covered by erun-integration goldens; these are the no-ops a
// golden cannot distinguish.
func tenantUmbrellaTarget() config.OpenResult {
	return config.OpenResult{
		Tenant: "frs",
		EnvConfig: config.EnvConfig{
			Name:            "prod",
			RuntimeVersion:  "1.0.142",
			RuntimeChart:    "oci://ghcr.io/sophium/charts/frs-devops:1.0.142",
			RuntimeRegistry: "ghcr.io/sophium",
		},
	}
}

// Every probe is refused, so any registry read would surface as a hold.
func TestTenantUmbrellaAtTheDeployVersionIsNotProbed(t *testing.T) {
	const version = "1.0.143"
	t.Setenv(PublishedChartProbeOverrideEnv, "")
	var trace bytes.Buffer
	ctx := core.Context{Logger: core.NewLogger(core.VerbosityInfo).WithTraceSink(&trace)}
	target := tenantUmbrellaTarget()
	target.EnvConfig.RuntimeChart = "oci://ghcr.io/sophium/charts/frs-devops:" + version
	spec, err := ResolvePublishedDevopsDeploySpecWithReason(ctx, target, version, "no local runtime chart", "", false)
	if err != nil {
		t.Fatalf("resolving the runtime deploy spec: %v", err)
	}
	if got := spec.Deploy.ChartVersion; got != version {
		t.Errorf("ChartVersion = %q, want the stated %q", got, version)
	}
	if got := trace.String(); strings.Contains(got, "holding") {
		t.Errorf("trace reports a hold for an umbrella already at the deploy version:\n%s", got)
	}
}

func TestTenantUmbrellaHoldIsNotReportedWhenTheOperatorOverridesTheChart(t *testing.T) {
	t.Setenv(PublishedChartProbeOverrideEnv, "")
	var trace bytes.Buffer
	ctx := core.Context{Logger: core.NewLogger(core.VerbosityInfo).WithTraceSink(&trace)}
	override := "oci://ghcr.io/sophium/charts/frs-devops:1.0.143"
	if _, err := ResolvePublishedDevopsDeploySpecWithReason(ctx, tenantUmbrellaTarget(), "1.0.143", "no local runtime chart", override, false); err != nil {
		t.Fatalf("resolving the runtime deploy spec: %v", err)
	}
	if got := trace.String(); strings.Contains(got, "holding") {
		t.Errorf("trace reports a hold the operator's --runtime-chart replaces anyway:\n%s", got)
	}
}
