package eruncommon

import (
	"strings"
	"testing"
)

func TestGuardRuntimeImageLineSwitchProceedsWhenLinesAgree(t *testing.T) {
	// frs/local: rides the stock erun-devops image on erun's own line, on
	// purpose. Must deploy with no new friction.
	target := OpenResult{
		Tenant: "frs",
		EnvConfig: EnvConfig{
			RuntimeRunningImage: "ghcr.io/sophium/erun-devops:1.0.203",
		},
	}
	err := guardRuntimeImageLineSwitch(Context{}, target, "ghcr.io/sophium/erun-devops:1.0.204", false)
	if err != nil {
		t.Fatalf("unexpected refusal for a consistent stock-image redeploy: %v", err)
	}
}

// TestGuardRuntimeImageLineSwitchRefusesCrossLineSwitch pins the core fix:
// this env's last confirmed deploy ran the tenant's own frs-devops image, but
// this deploy is about to resolve the stock erun-devops image instead (the
// exact silent-rollback shape erun#1754 describes: the wrong tag resolves
// fine, so only comparing release lines catches it). Refuse before any
// cluster mutation.
func TestGuardRuntimeImageLineSwitchRefusesCrossLineSwitch(t *testing.T) {
	target := OpenResult{
		Tenant:      "frs",
		Environment: "build",
		EnvConfig: EnvConfig{
			RuntimeRunningImage: "ghcr.io/sophium/frs-devops:1.0.86",
		},
	}
	err := guardRuntimeImageLineSwitch(Context{}, target, "ghcr.io/sophium/erun-devops:1.0.86", false)
	if err == nil {
		t.Fatal("expected a refusal: this deploy would move the pod off the tenant's own release line without being asked to")
	}
	if !strings.Contains(err.Error(), "erun") || !strings.Contains(err.Error(), "frs") {
		t.Fatalf("error should name both release lines, got: %v", err)
	}
}

// TestGuardRuntimeImageLineSwitchAllowsExplicitLineChange proves an operator's
// own --runtime-image/--runtime-chart (or a build --deploy of the working
// tree's own image) always proceeds: moving release lines on purpose is
// exactly what those inputs are for, and must never gain new friction.
func TestGuardRuntimeImageLineSwitchAllowsExplicitLineChange(t *testing.T) {
	target := OpenResult{
		Tenant: "frs",
		EnvConfig: EnvConfig{
			RuntimeRunningImage: "ghcr.io/sophium/frs-devops:1.0.86",
		},
	}
	err := guardRuntimeImageLineSwitch(Context{}, target, "ghcr.io/sophium/erun-devops:1.0.86", true)
	if err != nil {
		t.Fatalf("an explicit line change must never be refused: %v", err)
	}
}

// TestGuardRuntimeImageLineSwitchProceedsWithNoPriorDeploy covers a brand-new
// environment's first deploy: there is no RuntimeRunningImage yet, so there is
// nothing to disagree with.
func TestGuardRuntimeImageLineSwitchProceedsWithNoPriorDeploy(t *testing.T) {
	target := OpenResult{Tenant: "frs", EnvConfig: EnvConfig{}}
	err := guardRuntimeImageLineSwitch(Context{}, target, "ghcr.io/sophium/frs-devops:1.0.86", false)
	if err != nil {
		t.Fatalf("a first-ever deploy must not be refused: %v", err)
	}
}

// TestGuardRuntimeImageLineSwitchWarnsOnUnclassifiablePriorImage covers a
// recorded RuntimeRunningImage this guard cannot parse into a component name.
// Per root AGENTS.md, an unclassifiable pairing must not silently pass as
// fine, but it must also never block a configuration merely because this
// guard could not classify it -- it proceeds, with a trace explaining why.
func TestGuardRuntimeImageLineSwitchWarnsOnUnclassifiablePriorImage(t *testing.T) {
	var trace strings.Builder
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, &trace, &trace)}
	target := OpenResult{
		Tenant: "frs",
		EnvConfig: EnvConfig{
			// No tag/digest and no path separator: runtimeImageComponentName
			// still extracts a name from this ("garbage"), so use something
			// that yields an empty component name instead -- a value ending
			// in "/", so the last path segment is empty.
			RuntimeRunningImage: "ghcr.io/sophium/",
		},
	}
	err := guardRuntimeImageLineSwitch(ctx, target, "ghcr.io/sophium/frs-devops:1.0.86", false)
	if err != nil {
		t.Fatalf("an unclassifiable prior image must not block the deploy: %v", err)
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
			// The reported shape: an frs environment, running
			// frs's own line, whose deploy resolved erun's shared chart because
			// the requested version is an erun release and frs publishes no
			// chart at it. The image half agrees with itself (both frs-devops),
			// so only the chart's line catches it.
			name:            "refuses the shared chart on a tenant-line environment",
			runningImage:    "ghcr.io/sophium/frs-devops:1.0.138",
			resolvedChart:   "erun-devops",
			wantRefusal:     true,
			wantErrContains: []string{"frs", "erun-devops", "1.0.138", "--version", "--runtime-chart"},
		},
		{
			// The umbrella and the image are the same line: nothing to refuse.
			name:          "proceeds when the chart is on the environment's own line",
			runningImage:  "ghcr.io/sophium/frs-devops:1.0.138",
			resolvedChart: "frs-devops",
		},
		{
			// The erun product's own environments resolve the stock chart on
			// the line they run.
			name:          "proceeds for the stock chart on a stock-image environment",
			runningImage:  "ghcr.io/sophium/erun-devops:1.0.304",
			resolvedChart: "erun-devops",
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
		{
			// An observed baseline this guard cannot parse into a component
			// name is undetermined, not wrong.
			name:          "proceeds when the prior image cannot be classified",
			runningImage:  "ghcr.io/sophium/",
			resolvedChart: "erun-devops",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := OpenResult{
				Tenant:      "frs",
				Environment: "build",
				EnvConfig:   EnvConfig{RuntimeRunningImage: tc.runningImage},
			}
			err := guardRuntimeChartLineSwitch(Context{}, target, tc.resolvedChart, tc.explicitChange)
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
		if got := runtimeImageComponentName(image); got != want {
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
		line, ok := runtimeImageReleaseLine(tc.image)
		if line != tc.wantLine || ok != tc.wantOK {
			t.Errorf("runtimeImageReleaseLine(%q) = (%q, %v), want (%q, %v)", tc.image, line, ok, tc.wantLine, tc.wantOK)
		}
	}
}
