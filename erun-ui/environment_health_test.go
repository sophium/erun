package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// TestRuntimeDeployedHealthCheckPropagatesProbeError locks down that a real
// probe failure (VPN down, rotated token, unreachable API server) must land
// on the honest "could not check" branch, not the "not deployed" negative
// that hands the operator a Deploy button which would fail identically.
func TestRuntimeDeployedHealthCheckPropagatesProbeError(t *testing.T) {
	app := &App{
		deps: erunUIDeps{
			checkRuntimeDeployed: func(context.Context, string, string, string) (bool, error) {
				return false, errors.New("Unable to connect to the server: dial tcp: i/o timeout")
			},
		},
	}
	config := eruncommon.EnvConfig{KubernetesContext: "team-context"}
	check := app.runtimeDeployedHealthCheck("team", "dev", config)
	if check.Status != healthCheckStatusUnknown {
		t.Fatalf("status = %q, want %q (unknown/could-not-check)", check.Status, healthCheckStatusUnknown)
	}
	if check.Fix == healthCheckFixDeploy {
		t.Fatal("a probe failure must not offer a Deploy fix — the deploy would fail identically")
	}
	if !strings.Contains(check.Detail, "Could not check") {
		t.Fatalf("detail should say the check could not run, got %q", check.Detail)
	}
}

// TestRuntimeDeployedHealthCheckReportsNotDeployedOnGenuineNotFound covers the
// legitimate negative: a real NotFound (no such namespace) still reports "not
// deployed" with the Deploy fix offered.
func TestRuntimeDeployedHealthCheckReportsNotDeployedOnGenuineNotFound(t *testing.T) {
	app := &App{
		deps: erunUIDeps{
			checkRuntimeDeployed: func(context.Context, string, string, string) (bool, error) {
				return false, nil
			},
		},
	}
	config := eruncommon.EnvConfig{KubernetesContext: "team-context"}
	check := app.runtimeDeployedHealthCheck("team", "dev", config)
	if check.Status != healthCheckStatusError {
		t.Fatalf("status = %q, want %q", check.Status, healthCheckStatusError)
	}
	if check.Fix != healthCheckFixDeploy {
		t.Fatalf("expected the Deploy fix for a genuine not-deployed state, got %q", check.Fix)
	}
}

// TestCheckRuntimeDeployedDistinguishesNotFoundFromRealErrors exercises the
// package-level probe function directly against kubectl's actual message
// shapes, via the ERUN_KUBECTL_BIN seam.
func TestCheckRuntimeDeployedDistinguishesNotFoundFromRealErrors(t *testing.T) {
	cases := []struct {
		name       string
		stubStderr string
		stubExit   int
		wantErr    bool
	}{
		{"namespace not found is a negative, not an error", `Error from server (NotFound): namespaces "team-dev" not found`, 1, false},
		{"server unreachable propagates as an error", "Unable to connect to the server: dial tcp: i/o timeout", 1, true},
		{"forbidden propagates as an error", `Error from server (Forbidden): pods is forbidden: User "x" cannot list resource "pods"`, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubKubectlForTest(t, tc.stubStderr, tc.stubExit)
			deployed, err := checkRuntimeDeployed(context.Background(), "some-context", "team", "dev")
			if tc.wantErr && err == nil {
				t.Fatalf("expected an error, got deployed=%v", deployed)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error (a negative, not a failure), got %v", err)
			}
			if !tc.wantErr && deployed {
				t.Fatal("expected not-deployed (false), got true")
			}
		})
	}
}

// stubKubectlForTest replaces the kubectl binary on PATH for the duration of
// the test with a script that writes stderrMsg and exits exitCode.
// environment_health.go's kubectlJSON shells out to "kubectl" directly (not
// through an ERUN_KUBECTL_BIN seam), so the stub must sit on PATH.
func stubKubectlForTest(t *testing.T, stderrMsg string, exitCode int) {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\necho %s 1>&2\nexit %d\n", shellQuote(stderrMsg), exitCode)
	path := dir + "/kubectl"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub kubectl: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
