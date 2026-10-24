package deploy

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// TestGuardRuntimeImageLineSwitchAllowsExplicitLineChange proves an operator's
// own --runtime-image/--runtime-chart (or a build --deploy of the working
// tree's own image) always proceeds: moving release lines on purpose is
// exactly what those inputs are for, and must never gain new friction.
func TestGuardRuntimeImageLineSwitchAllowsExplicitLineChange(t *testing.T) {
	target := config.OpenResult{
		Tenant: "frs",
		EnvConfig: config.EnvConfig{
			RuntimeRunningImage: "ghcr.io/sophium/frs-devops:1.0.86",
		},
	}
	err := guardRuntimeImageLineSwitch(core.Context{}, target, "ghcr.io/sophium/erun-devops:1.0.86", true)
	if err != nil {
		t.Fatalf("an explicit line change must never be refused: %v", err)
	}
}

// TestGuardRuntimeImageLineSwitchProceedsWithNoPriorDeploy covers a brand-new
// environment's first deploy: there is no RuntimeRunningImage yet, so there is
// nothing to disagree with.
func TestGuardRuntimeImageLineSwitchProceedsWithNoPriorDeploy(t *testing.T) {
	target := config.OpenResult{Tenant: "frs", EnvConfig: config.EnvConfig{}}
	err := guardRuntimeImageLineSwitch(core.Context{}, target, "ghcr.io/sophium/frs-devops:1.0.86", false)
	if err != nil {
		t.Fatalf("a first-ever deploy must not be refused: %v", err)
	}
}

func TestGuardRuntimeChartLineSwitch(t *testing.T) {
	cases := []struct {
		name            string
		runningImage    string
		resolvedChart   string
		explicitChange  bool
		wantRefusal     bool
		wantErrContains []string
	}{
		{
			// The umbrella and the image are the same line: nothing to refuse.
			name:          "proceeds when the chart is on the environment's own line",
			runningImage:  "ghcr.io/sophium/frs-devops:1.0.138",
			resolvedChart: "frs-devops",
		},
		{
			// Moving release lines on purpose is what --runtime-chart is for.
			name:           "an explicit line change is never refused",
			runningImage:   "ghcr.io/sophium/frs-devops:1.0.138",
			resolvedChart:  "erun-devops",
			explicitChange: true,
		},
		{
			// No prior deploy to disagree with.
			name:          "proceeds with no prior deploy recorded",
			resolvedChart: "erun-devops",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := config.OpenResult{
				Tenant:      "frs",
				Environment: "build",
				EnvConfig:   config.EnvConfig{RuntimeRunningImage: tc.runningImage},
			}
			err := guardRuntimeChartLineSwitch(core.Context{}, target, tc.resolvedChart, tc.explicitChange)
			if tc.wantRefusal {
				if err == nil {
					t.Fatal("expected a refusal: the chart is on a different release line than this environment runs")
				}
				for _, want := range tc.wantErrContains {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("refusal must name %q, got: %v", want, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
		})
	}
}

func TestRuntimeImageComponentName(t *testing.T) {
	cases := map[string]string{
		"":                                    "",
		"erun-devops":                         "erun-devops",
		"ghcr.io/sophium/frs-devops":          "frs-devops",
		"ghcr.io/sophium/frs-devops:1.0.86":   "frs-devops",
		"ghcr.io/sophium/frs-devops@sha256:x": "frs-devops",
		"ghcr.io/sophium/":                    "",
	}
	for image, want := range cases {
		if got := config.RuntimeImageComponentName(image); got != want {
			t.Errorf("runtimeImageComponentName(%q) = %q, want %q", image, got, want)
		}
	}
}

func TestRuntimeImageReleaseLine(t *testing.T) {
	cases := []struct {
		image    string
		wantLine string
		wantOK   bool
	}{
		{"", "", false},
		{"erun-devops", "erun", true},
		{"ghcr.io/sophium/erun-devops:1.0.86", "erun", true},
		{"frs-devops", "frs", true},
		{"ghcr.io/sophium/frs-devops:1.0.86", "frs", true},
	}
	for _, tc := range cases {
		line, ok := config.RuntimeImageReleaseLine(tc.image)
		if line != tc.wantLine || ok != tc.wantOK {
			t.Errorf("runtimeImageReleaseLine(%q) = (%q, %v), want (%q, %v)", tc.image, line, ok, tc.wantLine, tc.wantOK)
		}
	}
}

// The stock chart runs its init containers and dind sidecar on the runtime
// image's own erun, so a stock image from an earlier release than the chart
// cannot start under it and must be refused before the rollout tears the
// running pod down. The same release in any of its tag forms, a newer image, a
// tenant's own image and an unclassifiable tag all proceed.
func TestRuntimeImageOlderThanTheStockChartIsRefused(t *testing.T) {
	err := guardRuntimeImageNotOlderThanChart("ghcr.io/sophium/erun-devops:1.0.296", config.DevopsComponentName, "1.0.307")
	if err == nil || !strings.Contains(err.Error(), "older than the erun-devops 1.0.307 chart") || !strings.Contains(err.Error(), "--runtime-chart erun-devops:1.0.296") {
		t.Fatalf("an older stock image under the stock chart should be refused with both remedies, got %v", err)
	}
	for _, ok := range []struct{ image, chart, version string }{
		{"ghcr.io/sophium/erun-devops:1.0.307", config.DevopsComponentName, "1.0.307-snapshot"},
		{"ghcr.io/sophium/erun-devops:1.0.307-snapshot-20261022181909-arm64", config.DevopsComponentName, "1.0.307"},
		{"ghcr.io/sophium/erun-devops:1.0.308", config.DevopsComponentName, "1.0.307"},
		{"ghcr.io/sophium/frs-devops:1.0.1", config.DevopsComponentName, "1.0.307"},
		{"ghcr.io/sophium/erun-devops:1.0.296", "frs-devops", "1.0.307"},
		{"ghcr.io/sophium/erun-devops:fp-c93a0b4ddb9869f3-arm64", config.DevopsComponentName, "1.0.307"},
		{"ghcr.io/sophium/erun-devops:1.0.296", config.DevopsComponentName, ""},
	} {
		if err := guardRuntimeImageNotOlderThanChart(ok.image, ok.chart, ok.version); err != nil {
			t.Fatalf("%s under %s %s should proceed, got %v", ok.image, ok.chart, ok.version, err)
		}
	}
}
