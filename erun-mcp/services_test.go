package erunmcp

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/expose"
	"github.com/sophium/erun/erun-common/orchestration/clusterread"
	"github.com/sophium/erun/erun-mcp/tools/services"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

// servicesKubectlStub answers only the two expected reads; any other call fails.
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

func servicesTestRuntime(t *testing.T, tenant, environment string) shared.RuntimeConfig {
	t.Helper()
	repoPath := t.TempDir()
	return shared.RuntimeConfig{
		Context: shared.RuntimeContext{Tenant: tenant, Environment: environment, RepoPath: repoPath},
		Store: listToolStore{
			tenantConfigs: map[string]config.TenantConfig{tenant: {Name: tenant}},
			envConfigs: map[string]config.EnvConfig{
				tenant + "/" + environment: {Name: environment, LocalRepoPath: repoPath, KubernetesContext: "test-context"},
			},
			envsByTenant: map[string][]config.EnvConfig{
				tenant: {{Name: environment, LocalRepoPath: repoPath, KubernetesContext: "test-context"}},
			},
		},
	}
}

// Exposures match on the Ingress backend, since a repo-native chart's Service
// need not follow <tenant>-<service>.
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
	_, output, err := services.ServicesTool(runtime)(context.Background(), nil, services.ServicesInput{})
	if err != nil {
		t.Fatalf("servicesTool returned err: %v", err)
	}
	if output.Tenant != "frs" || output.Environment != "dev" || output.Namespace != "frs-dev" {
		t.Fatalf("listing does not name the environment it read: %+v", output)
	}

	want := []expose.EnvironmentService{
		{Name: "frs-web", Type: "ClusterIP", Ports: []clusterread.ObservedServicePort{{Port: 80, Protocol: "TCP"}}},
		{
			Name: "pw-api", Type: "ClusterIP",
			Ports:    []clusterread.ObservedServicePort{{Name: "http", Port: 8080, Protocol: "TCP"}},
			Exposure: &expose.ServiceExposure{Label: "validator", Hostname: "validator.frs-dev.services.example.com", Scheme: "https"},
		},
	}
	if !reflect.DeepEqual(output.Services, want) {
		t.Fatalf("services = %+v, want %+v", output.Services, want)
	}
}

// A preview executes nothing, so Services is null rather than a fabricated list.
func TestServicesToolPreviewExecutesNothingAndReportsNoServices(t *testing.T) {
	kubectl := filepath.Join(t.TempDir(), "kubectl")
	if err := os.WriteFile(kubectl, []byte("#!/bin/sh\necho 'kubectl must not run in a preview' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("ERUN_KUBECTL_BIN", kubectl)

	runtime := servicesTestRuntime(t, "frs", "dev")
	_, output, err := services.ServicesTool(runtime)(context.Background(), nil, services.ServicesInput{Preview: true})
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
