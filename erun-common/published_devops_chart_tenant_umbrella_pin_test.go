package eruncommon

import (
	"bytes"
	"strings"
	"testing"
)

// tenantUmbrellaTarget is the shape the reported failure was reproduced in: a
// tenant that publishes its own <tenant>-devops umbrella states it at 1.0.142,
// and a deploy asks for 1.0.143. The image the same deploy derives is the deploy
// version, so honoring the stated chart installs a 1.0.143 runtime image under a
// 1.0.142 umbrella. The umbrella is where the wrapped erun version lives, so the
// two halves of this one coordinate must move together -- or the deploy must say
// they did not. The move itself is covered end-to-end by
// TestDeploy/dry_run_moves_the_tenant_umbrella_pin_with_the_deploy_version and
// TestDeploy/dry_run_says_when_it_holds_the_tenant_umbrella_back in
// erun-integration; what is left here are the two no-ops a golden cannot
// distinguish, where the deploy must do nothing at all.
func tenantUmbrellaTarget() OpenResult {
	return OpenResult{
		Tenant: "frs",
		EnvConfig: EnvConfig{
			Name:            "prod",
			RuntimeVersion:  "1.0.142",
			RuntimeChart:    "oci://ghcr.io/sophium/charts/frs-devops:1.0.142",
			RuntimeRegistry: "ghcr.io/sophium",
		},
	}
}

// TestTenantUmbrellaAtTheDeployVersionIsNotProbed pins the no-op half: a stated
// umbrella already at the deploy version is the coordinate the deploy wants, so
// it costs no registry read and prints no hold. Every probe is refused here, so
// a resolution that reads the registry at all reports a hold instead.
func TestTenantUmbrellaAtTheDeployVersionIsNotProbed(t *testing.T) {
	const version = "1.0.143"
	t.Setenv(publishedChartProbeOverrideEnv, "")
	var trace bytes.Buffer
	ctx := Context{Logger: NewLogger(VerbosityInfo).WithTraceSink(&trace)}
	target := tenantUmbrellaTarget()
	target.EnvConfig.RuntimeChart = "oci://ghcr.io/sophium/charts/frs-devops:" + version
	spec, err := resolvePublishedDevopsDeploySpecWithReason(ctx, target, version, "no local runtime chart", "", false)
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

// TestTenantUmbrellaHoldIsNotReportedWhenTheOperatorOverridesTheChart pins the
// override half: --runtime-chart replaces the coordinate wholesale, so a hold
// reported against the coordinate about to be replaced would be noise.
func TestTenantUmbrellaHoldIsNotReportedWhenTheOperatorOverridesTheChart(t *testing.T) {
	t.Setenv(publishedChartProbeOverrideEnv, "")
	var trace bytes.Buffer
	ctx := Context{Logger: NewLogger(VerbosityInfo).WithTraceSink(&trace)}
	override := "oci://ghcr.io/sophium/charts/frs-devops:1.0.143"
	if _, err := resolvePublishedDevopsDeploySpecWithReason(ctx, tenantUmbrellaTarget(), "1.0.143", "no local runtime chart", override, false); err != nil {
		t.Fatalf("resolving the runtime deploy spec: %v", err)
	}
	if got := trace.String(); strings.Contains(got, "holding") {
		t.Errorf("trace reports a hold the operator's --runtime-chart replaces anyway:\n%s", got)
	}
}
