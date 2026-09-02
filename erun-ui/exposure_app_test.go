package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
// script that writes stdout/stderr and exits with the given code.
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
	stubKubectlOutput(t, `{"items":[{"metadata":{"name":"expose-api"},"spec":{"rules":[{"host":"api.frs-prod.services.test"}]}}]}`, "", 0)

	result, err := app.ListEnvironmentExposures(uiSelection{Tenant: "team", Environment: "dev"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Configured || result.Restricted || result.Error != "" {
		t.Fatalf("unexpected result shape: %+v", result)
	}
	if len(result.Services) != 1 || result.Services[0].Hostname != "api.frs-prod.services.test" {
		t.Fatalf("expected one exposed service, got %+v", result.Services)
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

// stubKubectlByResource replaces kubectl with a script that answers per
// resource, which the service listing needs: it reads Services and Ingresses
// in one call, and a single canned stdout would make one of the two parse as
// an empty list and hide exactly what this test is about.
func stubKubectlByResource(t *testing.T, serviceJSON, ingressJSON string) {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\ncase \"$*\" in\n  *ingress*) printf %s ;;\n  *service*) printf %s ;;\n  *) printf '{\"items\":[]}' ;;\nesac\n",
		shellQuote(ingressJSON), shellQuote(serviceJSON))
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatalf("write stub kubectl: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestListEnvironmentServicesReportsPortsAndExistingExposure is the picker's
// contract: every Service the environment runs, its ports, and -- for the ones
// already published -- the hostname they answer at. The exposure is matched
// through the Ingress backend, so a Service whose name does not follow the
// <tenant>-<service> convention is still recognised as exposed rather than
// offered for exposing a second time.
func TestListEnvironmentServicesReportsPortsAndExistingExposure(t *testing.T) {
	app := exposureTestApp(t, exposableProjectRoot(t))
	stubKubectlByResource(t,
		`{"items":[
			{"metadata":{"name":"validation-agent-backend-api"},"spec":{"type":"ClusterIP","ports":[{"name":"http","port":8000,"protocol":"TCP"}]}},
			{"metadata":{"name":"team-mcp"},"spec":{"type":"ClusterIP","ports":[{"port":80}]}}
		]}`,
		`{"items":[{"metadata":{"name":"expose-validator"},"spec":{
			"rules":[{"host":"validator.team-dev.services.test","http":{"paths":[{"backend":{"service":{"name":"validation-agent-backend-api","port":{"number":8000}}}}]}}],
			"tls":[{"hosts":["validator.team-dev.services.test"],"secretName":"team-dev-wildcard-tls"}]
		}}]}`,
	)

	result, err := app.ListEnvironmentServices(uiSelection{Tenant: "team", Environment: "dev"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Configured || result.Restricted {
		t.Fatalf("unexpected result shape: %+v", result)
	}
	// Compared whole rather than field by field: the exposure has to be
	// matched through the Ingress backend, so a Service whose name does not
	// follow the <tenant>-<service> convention is still recognised as exposed
	// rather than offered for exposing a second time -- and team-mcp, which
	// nothing routes to, must come back with no exposure at all.
	want := []uiEnvironmentService{
		{Name: "team-mcp", Type: "ClusterIP", Ports: []uiEnvironmentServicePort{{Port: 80}}},
		{
			Name:      "validation-agent-backend-api",
			Type:      "ClusterIP",
			Ports:     []uiEnvironmentServicePort{{Name: "http", Port: 8000}},
			Hostname:  "validator.team-dev.services.test",
			Scheme:    "https",
			ExposedAs: "validator",
		},
	}
	if !reflect.DeepEqual(result.Services, want) {
		t.Fatalf("services = %+v, want %+v", result.Services, want)
	}
}

func TestListEnvironmentServicesRestrictedOnForbidden(t *testing.T) {
	app := exposureTestApp(t, exposableProjectRoot(t))
	stubKubectlOutput(t, "", `Error from server (Forbidden): services is forbidden: User "x" cannot list resource "services"`, 1)

	result, err := app.ListEnvironmentServices(uiSelection{Tenant: "team", Environment: "dev"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Configured || !result.Restricted {
		t.Fatalf("expected a restricted listing, got %+v", result)
	}
	if len(result.Services) != 0 {
		t.Fatalf("a restricted listing must not also claim services: %+v", result.Services)
	}
}

// A host environment has no cluster, so the picker reports the same
// not-applicable reason the exposure list does rather than an empty namespace.
func TestListEnvironmentServicesNotConfiguredForHostEnvironment(t *testing.T) {
	app := exposureTestApp(t, exposableProjectRoot(t))
	result, err := app.ListEnvironmentServices(uiSelection{Tenant: "team", Environment: "host"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Configured || result.NotConfiguredReason != uiExposureNotConfiguredHostEnvironment {
		t.Fatalf("expected the host-environment reason, got %+v", result)
	}
}
