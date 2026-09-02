package eruncommon

import "testing"

func TestDesktopUICoverageChanged(t *testing.T) {
	cases := []struct {
		name  string
		paths []string
		want  bool
	}{
		{"no paths", nil, false},
		{"unrelated module", []string{"erun-cli/cmd/build.go", "erun-common/build_run.go"}, false},
		{"desktop go file", []string{"erun-ui/app.go"}, true},
		{"desktop frontend file", []string{"erun-ui/frontend/src/app/state.ts"}, true},
		{"desktop playwright spec", []string{"erun-ui/playwright/tests/sidebar.spec.ts"}, true},
		{"desktop docs only", []string{"erun-ui/AGENTS.md"}, false},
		{"mixed docs and code", []string{"erun-ui/AGENTS.md", "erun-ui/app.go"}, true},
		{"unrelated docs", []string{"AGENTS.md", "erun-docs/docs/reference/troubleshooting.md"}, false},
		{"path containing but not prefixed by erun-ui", []string{"erun-kit/src/components/erun-ui-helper.ts"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := desktopUICoverageChanged(tc.paths); got != tc.want {
				t.Errorf("desktopUICoverageChanged(%v) = %v, want %v", tc.paths, got, tc.want)
			}
		})
	}
}

func TestEnsureDesktopPlaywrightCoverage(t *testing.T) {
	cases := []struct {
		name      string
		paths     []string
		verified  bool
		wantError bool
	}{
		{"no desktop change, not verified", []string{"erun-cli/cmd/build.go"}, false, false},
		{"desktop change, verified", []string{"erun-ui/app.go"}, true, false},
		{"desktop change, not verified", []string{"erun-ui/app.go"}, false, true},
		{"no changed paths at all, verified false", nil, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ensureDesktopPlaywrightCoverage(tc.paths, tc.verified)
			if tc.wantError && err == nil {
				t.Fatalf("expected a refusal error, got nil")
			}
			if !tc.wantError && err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
		})
	}
}

func TestResolveGateSquashChangedPathsNoRoot(t *testing.T) {
	if _, err := resolveGateSquashChangedPaths(""); err == nil {
		t.Fatal("expected an error for an empty root")
	}
}
