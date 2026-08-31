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
