package eruncommon

import (
	"bytes"
	"reflect"
	"testing"
)

func TestBuildEnvironmentServicesMarksGroundTruthExposure(t *testing.T) {
	services := []ObservedService{
		{Name: "team-api", Ports: []ObservedServicePort{{Name: "http", Port: 80, Protocol: "TCP"}}},
		{Name: "team-worker", Ports: []ObservedServicePort{{Port: 8080}}},
		{Name: "validation-agent-backend-api", Ports: []ObservedServicePort{{Port: 3000}}},
	}
	ingresses := []ObservedIngress{
		{
			Name:     "expose-api",
			Hosts:    []string{"api.team-dev.services.test"},
			Backends: []string{"team-api"},
		},
	}

	got := buildEnvironmentServices("team", services, ingresses)

	want := []EnvironmentService{
		{
			Name:     "team-api",
			Ports:    []EnvironmentServicePort{{Name: "http", Port: 80, Protocol: "TCP"}},
			Exposed:  true,
			Hostname: "api.team-dev.services.test",
			Scheme:   "http",
		},
		{
			Name:           "team-worker",
			Ports:          []EnvironmentServicePort{{Port: 8080}},
			ExposableLabel: "worker",
		},
		{
			Name:  "validation-agent-backend-api",
			Ports: []EnvironmentServicePort{{Port: 3000}},
			// No "team-" prefix, so expose has no way to route to this
			// Service correctly today -- ExposableLabel stays empty rather
			// than offering an action that would 503.
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildEnvironmentServices() = %+v, want %+v", got, want)
	}
}

func TestBuildEnvironmentServicesResolvesSchemeFromMatchingTLSHost(t *testing.T) {
	services := []ObservedService{{Name: "team-web"}}
	ingresses := []ObservedIngress{
		{
			Name:     "expose-web",
			Hosts:    []string{"web.team-dev.services.test"},
			Backends: []string{"team-web"},
			TLS:      []ObservedIngressTLS{{Hosts: []string{"web.team-dev.services.test"}, SecretName: "team-dev-wildcard-tls"}},
		},
	}

	got := buildEnvironmentServices("team", services, ingresses)

	want := []EnvironmentService{
		{Name: "team-web", Exposed: true, Hostname: "web.team-dev.services.test", Scheme: "https"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildEnvironmentServices() = %+v, want %+v", got, want)
	}
}

func TestBuildEnvironmentServicesIgnoresNonExposeIngresses(t *testing.T) {
	services := []ObservedService{{Name: "team-api"}}
	ingresses := []ObservedIngress{
		{Name: "some-other-ingress", Hosts: []string{"other.team-dev.services.test"}, Backends: []string{"team-api"}},
	}

	got := buildEnvironmentServices("team", services, ingresses)

	want := []EnvironmentService{{Name: "team-api", ExposableLabel: "api"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildEnvironmentServices() = %+v, want %+v", got, want)
	}
}

func TestListEnvironmentServicesDryRunTracesWithoutFetching(t *testing.T) {
	var trace bytes.Buffer
	ctx := Context{DryRun: true, Logger: NewLogger(VerbosityTrace).WithTraceSink(&trace)}
	req := ShellLaunchParams{Tenant: "team", Environment: "dev", Namespace: "team-dev"}

	got, err := ListEnvironmentServices(ctx, req, "team")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected no services in dry-run, got %+v", got)
	}
	if !bytes.Contains(trace.Bytes(), []byte("kubectl")) || !bytes.Contains(trace.Bytes(), []byte("get service")) || !bytes.Contains(trace.Bytes(), []byte("get ingress")) {
		t.Fatalf("expected both planned kubectl calls to be traced, got:\n%s", trace.String())
	}
}
