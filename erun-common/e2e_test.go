package eruncommon

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestE2EServiceURLBuildArgFoldsTheServiceName(t *testing.T) {
	for service, want := range map[string]string{
		"erun-console":  "ERUN_E2E_URL_ERUN_CONSOLE",
		"api":           "ERUN_E2E_URL_API",
		"web.v2--edge-": "ERUN_E2E_URL_WEB_V2_EDGE",
	} {
		if got := e2eServiceURLBuildArg(service); got != want {
			t.Errorf("e2eServiceURLBuildArg(%q) = %q, want %q", service, got, want)
		}
	}
}

// A local cluster's ingress is only refused on evidence: an address that is
// loopback. A host that does not resolve here, or resolves to a routable
// address, is left to the build.
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
		"console.local.example": {"127.0.0.1"},
		"console.v6.example":    {"2001:db8::1", "::1"},
		"console.example":       {"203.0.113.10"},
	}
	for _, tc := range []struct {
		url     string
		refused bool
	}{
		{"https://console.local.example", true},
		{"https://console.v6.example", true},
		{"https://127.0.0.1:8443", true},
		{"https://[::1]", true},
		{"https://console.example", false},
		{"https://unresolvable.invalid", false},
	} {
		err := refuseLoopbackE2ETarget(Context{}, tc.url, lookup(answers))
		if tc.refused != (err != nil) {
			t.Errorf("%s: refused=%v, want %v (err %v)", tc.url, err != nil, tc.refused, err)
		}
		if err != nil && !strings.Contains(err.Error(), "--base-url") {
			t.Errorf("%s: the refusal does not name the --base-url remedy: %v", tc.url, err)
		}
	}
}

// An e2e suite run from inside a runtime pod nests under the same CPU cap.
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

// A failure against a --version names that version as the expected one, not
// as what the current scope has deployed: with --base-url the suite may have
// run against a different deployment entirely.
func TestFormatE2EFailureNamesWhereTheVersionCameFrom(t *testing.T) {
	suite := E2ESuiteResult{Component: "console", Dockerfile: "team-devops/e2e/console/Dockerfile", BaseURL: "https://console.example.test", Failure: "docker build --target e2e failed"}
	deployed := formatE2EFailure(&E2EResult{Tenant: "team", Environment: "dev", Version: "1.0.296"}, suite)
	if !strings.Contains(deployed, "version:   1.0.296 (deployed to team/dev)") {
		t.Fatalf("a resolved version should name the environment it is deployed to, got:\n%s", deployed)
	}
	overridden := formatE2EFailure(&E2EResult{Tenant: "team", Environment: "dev", Version: "1.0.305", versionOverridden: true}, suite)
	if !strings.Contains(overridden, "version:   1.0.305 (expected, from --version)") || strings.Contains(overridden, "deployed to") {
		t.Fatalf("a --version should be named as the expected version, got:\n%s", overridden)
	}
}

// With --base-url and --version no environment is read, so a failing run's
// summary and JSON result name the URL it ran against, never the tenant and
// environment it was invoked under.
func TestAFailureAgainstABaseURLAndVersionNamesTheURL(t *testing.T) {
	projectRoot := t.TempDir()
	suiteDir := filepath.Join(projectRoot, "team-devops", "e2e", "console")
	if err := os.MkdirAll(suiteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	deps := e2eDeps{
		runBuild:   func(string, []string, io.Writer) error { return errors.New("exit status 1") },
		lookupHost: func(string) ([]string, error) { return nil, errors.New("no such host") },
		logPath: func(_ E2ETarget, component string) (string, error) {
			return filepath.Join(t.TempDir(), component+".log"), nil
		},
	}
	params := E2EParams{
		Plan:        E2EPlan{ProjectRoot: projectRoot, Suites: []E2ESuite{{Component: "console", Dir: suiteDir, Dockerfile: "team-devops/e2e/console/Dockerfile"}}},
		Target:      E2ETarget{Tenant: "team", Environment: "dev", Namespace: "team-dev"},
		BaseURL:     "https://console.production.invalid",
		Version:     "1.0.305",
		TargetNamed: true,
	}
	result, err := runE2E(Context{Stderr: io.Discard}, params, deps)
	if err == nil {
		t.Fatal("expected a failing suite to fail the run")
	}
	if !strings.Contains(err.Error(), "failed against https://console.production.invalid at version 1.0.305") || strings.Contains(err.Error(), "team/dev") {
		t.Fatalf("expected the summary to name the base URL and not team/dev, got %v", err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"baseUrl":"https://console.production.invalid","version":"1.0.305"`) || strings.Contains(string(data), `"tenant"`) || strings.Contains(string(data), `"environment"`) {
		t.Fatalf("expected the JSON result to name the base URL and no tenant/environment, got %s", data)
	}
}

// A run that resolved no environment logs beside erun's own state, not under
// a tenant/environment it never read.
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
