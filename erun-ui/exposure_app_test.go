package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

func exposureTestApp(t *testing.T, projectRoot string) *App {
	t.Helper()
	return &App{
		deps: erunUIDeps{
			store: stubUIStore{
				envs: map[string]eruncommon.EnvConfig{
					"team/dev":  {KubernetesContext: "team-context"},
					"team/host": {Type: eruncommon.EnvironmentTypeHost},
				},
			},
			findProjectRoot: func() (string, string, error) {
				if projectRoot == "" {
					return "", "", fmt.Errorf("no project found")
				}
				return filepath.Base(projectRoot), projectRoot, nil
			},
		},
	}
}

func exposableProjectRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := eruncommon.SaveProjectConfig(root, eruncommon.ProjectConfig{
		Platform: eruncommon.PlatformConfig{
			BaseDomain:   "erunpaas.test",
			Env:          "frs-prod",
			ServicesZone: "services.erunpaas.test",
		},
	}); err != nil {
		t.Fatalf("write project config: %v", err)
	}
	return root
}

// stubKubectlOutput replaces kubectl on PATH for this test's duration with a
// script that writes stdout/stderr and exits with the given code for every
// call -- for scenarios where ListEnvironmentServices' two kubectl calls
// (get service, get ingress) can share one response, e.g. a failure that
// happens on the first call either way.
func stubKubectlOutput(t *testing.T, stdout, stderr string, exitCode int) {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\nprintf %s\nprintf %s 1>&2\nexit %d\n",
		shellQuote(stdout), shellQuote(stderr), exitCode)
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatalf("write stub kubectl: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// stubKubectlGetJSON replaces kubectl on PATH with a script that dispatches
// on the resource named right after "get" (e.g. "service", "ingress") to
// return canned JSON, mirroring erun-integration's StubKubectlGetJSON --
// ListEnvironmentServices issues one `get service` and one `get ingress`
// call per run, and a scenario needs to answer them differently.
func stubKubectlGetJSON(t *testing.T, responses map[string]string) {
	t.Helper()
	dir := t.TempDir()
	var body strings.Builder
	body.WriteString("#!/bin/sh\nargs=\"$*\"\ncase \"$args\" in\n")
	for resource, stdout := range responses {
		fmt.Fprintf(&body, "  *%s*)\n    cat <<'ERUN_STUB_KUBECTL_JSON'\n%s\nERUN_STUB_KUBECTL_JSON\n    ;;\n",
			shellQuote("get "+resource), strings.TrimRight(stdout, "\n"))
	}
	body.WriteString("  *) echo \"unstubbed kubectl call: $args\" 1>&2; exit 1 ;;\nesac\n")
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(body.String()), 0o755); err != nil {
		t.Fatalf("write stub kubectl: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestListEnvironmentExposuresNotConfiguredWithoutPlatformBlock(t *testing.T) {
	app := exposureTestApp(t, "")
	result, err := app.ListEnvironmentExposures(uiSelection{Tenant: "team", Environment: "dev"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Configured {
		t.Fatal("expected Configured=false when the project has no platform block")
	}
	if result.Restricted {
		t.Fatal("an unconfigured project must not report Restricted")
	}
	if len(result.Services) != 0 {
		t.Fatalf("expected no services, got %+v", result.Services)
	}
	if result.NotConfiguredReason != uiExposureNotConfiguredNoPlatformBlock {
		t.Fatalf("expected NotConfiguredReason %q, got %q", uiExposureNotConfiguredNoPlatformBlock, result.NotConfiguredReason)
	}
}

// TestListEnvironmentExposuresNotConfiguredForHostEnvironment locks down that
// a host environment (no pod, no cluster at all) reports the host-specific
// reason even when the open project does have a platform block -- exposure
// can never apply to a host env regardless of the project's configuration.
func TestListEnvironmentExposuresNotConfiguredForHostEnvironment(t *testing.T) {
	app := exposureTestApp(t, exposableProjectRoot(t))
	result, err := app.ListEnvironmentExposures(uiSelection{Tenant: "team", Environment: "host"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Configured {
		t.Fatal("expected Configured=false for a host environment")
	}
	if result.NotConfiguredReason != uiExposureNotConfiguredHostEnvironment {
		t.Fatalf("expected NotConfiguredReason %q, got %q", uiExposureNotConfiguredHostEnvironment, result.NotConfiguredReason)
	}
}

// TestListEnvironmentExposuresRestrictedOnForbidden locks down that a
// Kubernetes permission denial renders as Restricted, not as a genuinely empty
// list an operator would read as "this environment has nothing exposed".
func TestListEnvironmentExposuresRestrictedOnForbidden(t *testing.T) {
	app := exposureTestApp(t, exposableProjectRoot(t))
	stubKubectlOutput(t, "", `Error from server (Forbidden): ingresses.networking.k8s.io is forbidden: User "x" cannot list resource "ingresses"`, 1)

	result, err := app.ListEnvironmentExposures(uiSelection{Tenant: "team", Environment: "dev"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Configured {
		t.Fatal("expected Configured=true for a project with a platform block")
	}
	if !result.Restricted {
		t.Fatal("expected Restricted=true on a Forbidden listing failure")
	}
	if result.Error != "" {
		t.Fatalf("a restricted result should not also carry a generic error, got %q", result.Error)
	}
}

// TestListEnvironmentExposuresErrorOnOtherFailure locks down that a
// non-permission listing failure surfaces as a distinct Error state, not
// silently collapsed into Restricted or an empty list.
func TestListEnvironmentExposuresErrorOnOtherFailure(t *testing.T) {
	app := exposureTestApp(t, exposableProjectRoot(t))
	stubKubectlOutput(t, "", "Unable to connect to the server: dial tcp: i/o timeout", 1)

	result, err := app.ListEnvironmentExposures(uiSelection{Tenant: "team", Environment: "dev"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Configured {
		t.Fatal("expected Configured=true for a project with a platform block")
	}
	if result.Restricted {
		t.Fatal("a generic listing failure must not report Restricted")
	}
	if result.Error == "" {
		t.Fatal("expected a non-empty Error for a generic listing failure")
	}
}

func TestListEnvironmentExposuresReturnsServices(t *testing.T) {
	app := exposureTestApp(t, exposableProjectRoot(t))
	stubKubectlGetJSON(t, map[string]string{
		"service": `{"items":[{"metadata":{"name":"team-api"},"spec":{"ports":[{"port":80}]}}]}`,
		"ingress": `{"items":[{"metadata":{"name":"expose-api"},"spec":{"rules":[{"host":"api.frs-prod.services.test","http":{"paths":[{"backend":{"service":{"name":"team-api"}}}]}}]}}]}`,
	})

	result, err := app.ListEnvironmentExposures(uiSelection{Tenant: "team", Environment: "dev"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Configured || result.Restricted || result.Error != "" {
		t.Fatalf("unexpected result shape: %+v", result)
	}
	if len(result.Services) != 1 || !result.Services[0].Exposed || result.Services[0].Hostname != "api.frs-prod.services.test" {
		t.Fatalf("expected one exposed service, got %+v", result.Services)
	}
}

// TestListEnvironmentExposuresReturnsExposableAndBlockedServices locks the
// three-way state a picker needs to distinguish (issue #1906): a Service
// following the tenant naming convention but not yet exposed reports the
// label erun expose would need, and one that doesn't follow it reports
// neither Exposed nor an ExposableLabel -- never a guessed one.
func TestListEnvironmentExposuresReturnsExposableAndBlockedServices(t *testing.T) {
	app := exposureTestApp(t, exposableProjectRoot(t))
	stubKubectlGetJSON(t, map[string]string{
		"service": `{"items":[
			{"metadata":{"name":"team-worker"},"spec":{"ports":[{"port":8080}]}},
			{"metadata":{"name":"validation-agent-backend-api"},"spec":{"ports":[{"port":3000}]}}
		]}`,
		"ingress": `{"items":[]}`,
	})

	result, err := app.ListEnvironmentExposures(uiSelection{Tenant: "team", Environment: "dev"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Services) != 2 {
		t.Fatalf("expected two services, got %+v", result.Services)
	}
	worker, other := result.Services[0], result.Services[1]
	if worker.Exposed || worker.ExposableLabel != "worker" {
		t.Fatalf("expected team-worker exposable as %q, got %+v", "worker", worker)
	}
	if other.Exposed || other.ExposableLabel != "" {
		t.Fatalf("expected validation-agent-backend-api to be neither exposed nor exposable, got %+v", other)
	}
}

func TestExposeEnvironmentServiceRequiresServiceAndTargetIP(t *testing.T) {
	app := exposureTestApp(t, exposableProjectRoot(t))
	if _, err := app.ExposeEnvironmentService(uiSelection{Tenant: "team", Environment: "dev"}, uiExposeServiceInput{}); err == nil {
		t.Fatal("expected an error when service and target IP are both missing")
	}
}

// TestExposeEnvironmentServiceFailsWhenNotConfigured is the red/green subject
// for this file: it fails clearly, before ever shelling out, when the open
// project has no platform block to resolve a hostname from.
func TestExposeEnvironmentServiceFailsWhenNotConfigured(t *testing.T) {
	app := exposureTestApp(t, "")
	_, err := app.ExposeEnvironmentService(uiSelection{Tenant: "team", Environment: "dev"}, uiExposeServiceInput{
		Service: "api", TargetIP: "127.0.0.1",
	})
	if err == nil {
		t.Fatal("expected an error exposing a service with no platform block configured")
	}
}

// TestPreviewExposeEnvironmentServiceResolvesWithoutApplying locks that the
// preview resolves the same hostname/scheme a real expose would, without
// ever shelling out to kubectl -- if it applied anything, the unstubbed
// kubectl on PATH would fail the test.
func TestPreviewExposeEnvironmentServiceResolvesWithoutApplying(t *testing.T) {
	app := exposureTestApp(t, exposableProjectRoot(t))
	preview, err := app.PreviewExposeEnvironmentService(uiSelection{Tenant: "team", Environment: "dev"}, uiExposeServiceInput{
		Service: "api", TargetIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if preview.Hostname != "api.team-dev.services.erunpaas.test" {
		t.Fatalf("expected the resolved hostname, got %+v", preview)
	}
	if preview.TLSEnabled {
		t.Fatalf("expected TLS disabled with no DNS-01 broker configured, got %+v", preview)
	}
	if preview.TLSDisabledReason == "" {
		t.Fatal("expected a TLSDisabledReason when TLS is disabled")
	}
}

// TestExposeDefaultTargetIPLocalVsCloud locks the two states the Ports tab's
// Target IP field needs: 127.0.0.1 for a kubernetes context no registered
// cloud context claims (issue #1906's local-cluster default), empty for one
// a registered cloud context does claim, where guessing would be wrong.
func TestExposeDefaultTargetIPLocalVsCloud(t *testing.T) {
	app := &App{deps: erunUIDeps{store: stubUIStore{
		config: &eruncommon.ERunConfig{
			CloudContexts: []eruncommon.CloudContextConfig{
				{Name: "cloud-ctx", KubernetesContext: "cloud-ctx"},
			},
		},
	}}}
	if got := app.exposeDefaultTargetIP("local-context"); got != "127.0.0.1" {
		t.Fatalf("expected 127.0.0.1 for a local cluster, got %q", got)
	}
	if got := app.exposeDefaultTargetIP("cloud-ctx"); got != "" {
		t.Fatalf("expected no default for a registered cloud context, got %q", got)
	}
	if got := app.exposeDefaultTargetIP(""); got != "" {
		t.Fatalf("expected no default with no kubernetes context, got %q", got)
	}
}
