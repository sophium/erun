package activity

import (
	"errors"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
)

// Tested here because the Playwright harness has no cluster or Docker daemon.
func TestDeployNeedsBuildOrchestration(t *testing.T) {
	openOf := func(envType config.EnvironmentType) config.OpenResult {
		return config.OpenResult{EnvConfig: config.EnvConfig{Type: envType}}
	}
	cases := []struct {
		name    string
		result  config.OpenResult
		version string
		force   bool
		want    bool
	}{
		{"local-agent, no version: build fresh", openOf(config.EnvironmentTypeLocalAgent), "", false, true},
		{"local-agent, pinned version: install by reference", openOf(config.EnvironmentTypeLocalAgent), "1.2.3-snapshot", false, false},
		{"local-agent, forced: rebuild even with a version", openOf(config.EnvironmentTypeLocalAgent), "1.2.3-snapshot", true, true},
		{"runtime: install by reference", openOf(config.EnvironmentTypeRuntime), "", false, false},
		{"runtime, forced: still no local build", openOf(config.EnvironmentTypeRuntime), "", true, false},
		{"remote-agent: builds in its pod, not here", openOf(config.EnvironmentTypeRemoteAgent), "", false, false},
		{"unresolved type: no orchestration", openOf(""), "", false, false},
		// A host env satisfies BuildsHere() && !RemoteRepo() but has no pod.
		{"host: no pod to deploy to, regardless of version", openOf(config.EnvironmentTypeHost), "", false, false},
		{"host, forced: still no pod to deploy to", openOf(config.EnvironmentTypeHost), "", true, false},
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

// erun routes Info to stderr, so its first line is always a "==>" marker.
func TestDeployOrchestrationFailureReasonKeepsCompilerErrorNotTraceMarker(t *testing.T) {
	err := &erunCommandError{
		Command: "build",
		Err:     errors.New("exit status 1"),
		Detail:  "==> Building\n./main.go:12:2: undefined: fmt.Sprintfff",
	}
	got := deployOrchestrationFailureReason(err)
	if strings.Contains(got, "==> Building") {
		t.Fatalf("reason still carries the trace marker instead of the real error: %q", got)
	}
	if !strings.Contains(got, "undefined: fmt.Sprintfff") {
		t.Fatalf("reason dropped the compiler error entirely: %q", got)
	}
}

func TestDeployOrchestrationFailureReasonFallsBackWithNoDetail(t *testing.T) {
	err := &erunCommandError{Command: "deploy", Err: errors.New("exit status 1")}
	got := deployOrchestrationFailureReason(err)
	if !strings.Contains(got, "erun deploy") || !strings.Contains(got, "exit status 1") {
		t.Fatalf("reason = %q, want it to name the command and the exec error", got)
	}
}

func TestDeployOrchestrationFailureReasonTruncatesTail(t *testing.T) {
	long := strings.Repeat("x", 400) + " THE_REAL_ERROR_AT_THE_END"
	err := &erunCommandError{Command: "build", Err: errors.New("exit status 1"), Detail: long}
	got := deployOrchestrationFailureReason(err)
	if !strings.Contains(got, "THE_REAL_ERROR_AT_THE_END") {
		t.Fatalf("truncated reason dropped the tail content: %q", got)
	}
	if len(got) > deployOrchestrationFailureReasonMaxLen+3 {
		t.Fatalf("reason not capped: len=%d, got=%q", len(got), got)
	}
}

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
