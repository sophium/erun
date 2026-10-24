package e2e

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
)

func TestE2EServiceURLBuildArgFoldsTheServiceName(t *testing.T) {
	for service, want := range map[string]string{
		"api":           "ERUN_E2E_URL_API",
		"web.v2--edge-": "ERUN_E2E_URL_WEB_V2_EDGE",
	} {
		if got := e2eServiceURLBuildArg(service); got != want {
			t.Errorf("e2eServiceURLBuildArg(%q) = %q, want %q", service, got, want)
		}
	}
}

func TestRefuseLoopbackE2ETarget(t *testing.T) {
	lookup := func(answers map[string][]string) func(string) ([]string, error) {
		return func(host string) ([]string, error) {
			if addrs, ok := answers[host]; ok {
				return addrs, nil
			}
			return nil, errors.New("no such host")
		}
	}
	answers := map[string][]string{
		"console.v6.example": {"2001:db8::1", "::1"},
		"console.example":    {"203.0.113.10"},
	}
	for _, tc := range []struct {
		url     string
		refused bool
	}{
		{"https://console.v6.example", true},
		{"https://127.0.0.1:8443", true},
		{"https://[::1]", true},
		{"https://console.example", false},
	} {
		err := refuseLoopbackE2ETarget(core.Context{}, tc.url, lookup(answers))
		if tc.refused != (err != nil) {
			t.Errorf("%s: refused=%v, want %v (err %v)", tc.url, err != nil, tc.refused, err)
		}
		if err != nil && !strings.Contains(err.Error(), "--base-url") {
			t.Errorf("%s: the refusal does not name the --base-url remedy: %v", tc.url, err)
		}
	}
}

func TestE2EDockerBuildNestsUnderTheCPUCapWhenOneApplies(t *testing.T) {
	suite := E2ESuite{Component: "console", Dockerfile: "team-devops/e2e/console/Dockerfile"}
	args := strings.Join(e2eDockerBuildArgs(suite, "1.2.3", "https://console.example.test", nil, nil, "/docker/erun-build-cpu-cap-pod"), " ")
	if !strings.Contains(args, "--cgroup-parent /docker/erun-build-cpu-cap-pod -f team-devops/e2e/console/Dockerfile .") {
		t.Fatalf("an in-pod e2e run should carry --cgroup-parent, got %q", args)
	}
	if bare := strings.Join(e2eDockerBuildArgs(suite, "1.2.3", "https://console.example.test", nil, nil, ""), " "); strings.Contains(bare, "--cgroup-parent") {
		t.Fatalf("a host e2e run carries no --cgroup-parent, got %q", bare)
	}
}

func TestE2ELogPathWithoutAnEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	got, err := e2eLogPath(E2ETarget{}, "console")
	if err != nil {
		t.Fatalf("expected a log path without an environment, got %v", err)
	}
	if want := filepath.Join(home, ".erun", "e2e", "console.log"); got != want {
		t.Fatalf("e2eLogPath = %q, want %q", got, want)
	}
}
