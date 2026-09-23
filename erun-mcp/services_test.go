package erunmcp

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// servicesKubectlStub points ERUN_KUBECTL_BIN at a script answering the two
// reads the listing makes from fixed JSON, so the tool's own behaviour runs
// end to end without a cluster. Anything but the two expected reads fails the
// call, so a test cannot pass by having the tool skip a read it owes.
func servicesKubectlStub(t *testing.T, servicesJSON, ingressesJSON string) {
	t.Helper()
	if strings.ContainsAny(servicesJSON, "'") || strings.ContainsAny(ingressesJSON, "'") {
		t.Fatal("stub JSON must not contain a single quote; the stub is single-quoted shell")
	}
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  *\"get service\"*) printf '%s' '" + servicesJSON + "' ;;\n" +
		"  *\"get ingress\"*) printf '%s' '" + ingressesJSON + "' ;;\n" +
		"  *) echo \"unexpected kubectl call: $*\" >&2; exit 1 ;;\n" +
		"esac\n"
	path := filepath.Join(t.TempDir(), "kubectl")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("ERUN_KUBECTL_BIN", path)
}

func servicesTestRuntime(t *testing.T, tenant, environment string) RuntimeConfig {
	t.Helper()
	repoPath := t.TempDir()
	return RuntimeConfig{
		Context: RuntimeContext{Tenant: tenant, Environment: environment, RepoPath: repoPath},
		Store: listToolStore{
			tenantConfigs: map[string]eruncommon.TenantConfig{tenant: {Name: tenant}},
			envConfigs: map[string]eruncommon.EnvConfig{
				tenant + "/" + environment: {Name: environment, LocalRepoPath: repoPath, KubernetesContext: "test-context"},
			},
			envsByTenant: map[string][]eruncommon.EnvConfig{
				tenant: {{Name: environment, LocalRepoPath: repoPath, KubernetesContext: "test-context"}},
			},
		},
	}
}

// TestServicesToolReportsWhatTheNamespaceRunsAndWhatIsExposed is the MCP half
// of the reported gap: expose's SERVICE argument was a derived label with
// nothing on either agent-facing surface able to report a real one, so an
// agent could act without being able to orient.
//
// The listing is the answer to "what is this environment running", and the
// matching is on the Ingress's own backend -- a repo-native chart names its
// Service something the <tenant>-<service> convention would not produce.
//
// This runs the tool's real path, against the two kubectl reads it actually
// makes, so a handler that resolves its target and then discards the listing
// (returning an empty EnvironmentServiceList) cannot pass: the empty result is
// what the caller would act on.
func TestServicesToolReportsWhatTheNamespaceRunsAndWhatIsExposed(t *testing.T) {
	servicesKubectlStub(t,
		`{"items":[`+
			`{"metadata":{"name":"pw-api"},"spec":{"type":"ClusterIP","ports":[{"name":"http","port":8080,"protocol":"TCP"}]}},`+
			`{"metadata":{"name":"frs-web"},"spec":{"type":"ClusterIP","ports":[{"port":80,"protocol":"TCP"}]}}`+
			`]}`,
		`{"items":[{"metadata":{"name":"expose-validator"},`+
			`"spec":{"rules":[{"host":"validator.frs-dev.services.example.com","http":{"paths":[{"backend":{"service":{"name":"pw-api","port":{"number":8080}}}}]}}],`+
			`"tls":[{"hosts":["validator.frs-dev.services.example.com"],"secretName":"frs-dev-wildcard-tls"}]}}]}`,
	)

	runtime := servicesTestRuntime(t, "frs", "dev")
	_, output, err := servicesTool(runtime)(context.Background(), nil, ServicesInput{})
	if err != nil {
		t.Fatalf("servicesTool returned err: %v", err)
	}
	if output.Tenant != "frs" || output.Environment != "dev" || output.Namespace != "frs-dev" {
		t.Fatalf("listing does not name the environment it read: %+v", output)
	}

	// pw-api is matched to the Ingress by the backend it routes to, not by the
	// derived name -- the exposure's label is "validator", which no
	// <tenant>-<service> derivation would produce from it.
	want := []eruncommon.EnvironmentService{
		{Name: "frs-web", Type: "ClusterIP", Ports: []eruncommon.ObservedServicePort{{Port: 80, Protocol: "TCP"}}},
		{
			Name: "pw-api", Type: "ClusterIP",
			Ports:    []eruncommon.ObservedServicePort{{Name: "http", Port: 8080, Protocol: "TCP"}},
			Exposure: &eruncommon.ServiceExposure{Label: "validator", Hostname: "validator.frs-dev.services.example.com", Scheme: "https"},
		},
	}
	if !reflect.DeepEqual(output.Services, want) {
		t.Fatalf("services = %+v, want %+v", output.Services, want)
	}
}

// The preview contract the docs state: a preview resolves the calls a real run
// would make and executes none of them, so the listing comes back with the
// environment it resolved and no Services -- null, not a stale or fabricated
// list a caller might mistake for cluster state.
func TestServicesToolPreviewExecutesNothingAndReportsNoServices(t *testing.T) {
	kubectl := filepath.Join(t.TempDir(), "kubectl")
	if err := os.WriteFile(kubectl, []byte("#!/bin/sh\necho 'kubectl must not run in a preview' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("ERUN_KUBECTL_BIN", kubectl)

	runtime := servicesTestRuntime(t, "frs", "dev")
	_, output, err := servicesTool(runtime)(context.Background(), nil, ServicesInput{Preview: true})
	if err != nil {
		t.Fatalf("servicesTool returned err: %v", err)
	}
	if output.Tenant != "frs" || output.Environment != "dev" || output.Namespace != "frs-dev" {
		t.Fatalf("preview does not name the environment it would read: %+v", output)
	}
	if len(output.Services) != 0 {
		t.Fatalf("preview executed the read after all: %+v", output.Services)
	}
}
