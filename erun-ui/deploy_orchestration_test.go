package main

import (
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// TestDeployNeedsBuildOrchestration locks the desktop's per-env-type deploy
// policy here because the Playwright harness has no cluster or Docker daemon to
// drive the real build -> push -> deploy end-to-end.
func TestDeployNeedsBuildOrchestration(t *testing.T) {
	openOf := func(envType eruncommon.EnvironmentType) eruncommon.OpenResult {
		return eruncommon.OpenResult{EnvConfig: eruncommon.EnvConfig{Type: envType}}
	}
	cases := []struct {
		name    string
		result  eruncommon.OpenResult
		version string
		force   bool
		want    bool
	}{
		{"local-agent, no version: build fresh", openOf(eruncommon.EnvironmentTypeLocalAgent), "", false, true},
		{"local-agent, pinned version: install by reference", openOf(eruncommon.EnvironmentTypeLocalAgent), "1.2.3-snapshot", false, false},
		{"local-agent, forced: rebuild even with a version", openOf(eruncommon.EnvironmentTypeLocalAgent), "1.2.3-snapshot", true, true},
		{"runtime: install by reference", openOf(eruncommon.EnvironmentTypeRuntime), "", false, false},
		{"runtime, forced: still no local build", openOf(eruncommon.EnvironmentTypeRuntime), "", true, false},
		{"remote-agent: builds in its pod, not here", openOf(eruncommon.EnvironmentTypeRemoteAgent), "", false, false},
		{"unresolved type: no orchestration", openOf(""), "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deployNeedsBuildOrchestration(tc.result, tc.version, tc.force); got != tc.want {
				t.Fatalf("deployNeedsBuildOrchestration(type=%q, version=%q, force=%v) = %v, want %v",
					tc.result.EnvConfig.Type, tc.version, tc.force, got, tc.want)
			}
		})
	}
}

// TestParseBuildResultVersion covers pulling the minted version out of
// `erun build --output json` stdout even when it carries surrounding noise.
func TestParseBuildResultVersion(t *testing.T) {
	cases := []struct {
		name   string
		stdout string
		want   string
	}{
		{"clean json", `{"version":"1.0.0-snapshot","images":[]}`, "1.0.0-snapshot"},
		{"indented json with surrounding noise", "warming up\n{\n  \"version\": \"2.0.0\"\n}\ndone\n", "2.0.0"},
		{"no json", "nothing structured here", ""},
		{"empty", "", ""},
		{"json without version", `{"images":[]}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseBuildResultVersion(tc.stdout); got != tc.want {
				t.Fatalf("parseBuildResultVersion(%q) = %q, want %q", tc.stdout, got, tc.want)
			}
		})
	}
}
