package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// list_control_planes_test.go covers `erun list --control-planes` (erun#2052):
// every configured erun-hosted control plane's deployed version, compared
// against the newest version erun's own registry has actually published.
// route-check proves a route is reachable on a plane already assumed to be
// current; --tenant's version drift compares environments against each
// other with no registry baseline. Neither answers "is this plane running
// the latest published release" -- this is that check.

// controlPlaneRegistryStub serves the DockerHub tag-list shape
// resolveDockerHubRuntimeRegistryVersionsAt expects, reporting latest as the
// single highest version in tags.
func controlPlaneRegistryStub(t testing.TB, tags ...string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		results := make([]string, 0, len(tags))
		for _, tag := range tags {
			results = append(results, `{"name":"`+tag+`"}`)
		}
		_, _ = fmt.Fprintf(w, `{"next":"","results":[%s]}`, strings.Join(results, ","))
	}))
	t.Cleanup(server.Close)
	return server
}

// controlPlaneStub serves GET /v1/platform reporting version, standing in
// for a deployed control plane.
func controlPlaneStub(t testing.TB, version string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"version":"%s"}`, version)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// seedControlPlaneConfig writes one config.yaml carrying both the erun-type
// cloud provider aliases (each pointed at its own plane stub, or at an
// address nothing listens on to model an unreachable plane) and the
// registry override -- both sections live in the same file, so they must be
// written together rather than via the single-purpose helpers list_test.go
// and pin_test.go already use for each half alone.
func seedControlPlaneConfig(t testing.TB, setup env.Setup, planes map[string]string, registryURL string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	var body strings.Builder
	if len(planes) > 0 {
		body.WriteString("cloudproviders:\n")
		for alias, apiURL := range planes {
			body.WriteString("  - alias: " + alias + "\n")
			body.WriteString("    provider: erun\n")
			body.WriteString("    erun:\n")
			body.WriteString("      apiurl: " + apiURL + "\n")
			body.WriteString("      clientid: cli-test-client\n")
		}
	}
	body.WriteString("runtimeregistry:\n")
	body.WriteString("  namespace: acme\n")
	body.WriteString("  repository: erun-devops\n")
	body.WriteString("  baseurl: " + registryURL + "\n")
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(body.String()), 0o644); err != nil {
		t.Fatalf("write erun config: %v", err)
	}
}

func TestListControlPlanes(t *testing.T) {
	t.Parallel()

	t.Run("help_documents_control_planes", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		result := erun.Run(t, []string{"list", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "--control-planes") {
			t.Fatalf("expected --control-planes in help:\n%s", result.Combined)
		}
	})

	t.Run("combined_with_tenant_errors", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		result := erun.Run(t, []string{"list", "--tenant", "erun", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit combining --control-planes with --tenant, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "list/control_planes_combined_with_tenant_errors", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_traces_planes_and_registry_lookup_without_probing", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		plane := controlPlaneStub(t, "1.0.245")
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/control_planes_dry_run_traces_planes_and_registry_lookup_without_probing",
			normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
	})

	t.Run("real_run_reports_a_plane_behind_published", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		plane := controlPlaneStub(t, "1.0.245")
		registry := controlPlaneRegistryStub(t, "1.0.246", "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "[behind published -- roll it]") {
			t.Fatalf("expected a behind-published verdict:\n%s", result.Combined)
		}
		golden.Equal(t, "list/control_planes_real_run_reports_a_plane_behind_published",
			normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
	})

	t.Run("real_run_reports_a_plane_ahead_of_published", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		// The plane runs a build newer than anything the registry has ever
		// published -- a more alarming condition than routine drift, and
		// reported distinctly from "behind" rather than folded into it.
		plane := controlPlaneStub(t, "1.0.999")
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "[ahead of published -- running an unpublished version]") {
			t.Fatalf("expected an ahead-of-published verdict:\n%s", result.Combined)
		}
		golden.Equal(t, "list/control_planes_real_run_reports_a_plane_ahead_of_published",
			normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
	})

	t.Run("real_run_reports_a_plane_up_to_date", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		plane := controlPlaneStub(t, "1.0.247")
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if strings.Contains(result.Combined, "behind published") || strings.Contains(result.Combined, "ahead of published") {
			t.Fatalf("a plane at the published version must carry no verdict:\n%s", result.Combined)
		}
		golden.Equal(t, "list/control_planes_real_run_reports_a_plane_up_to_date",
			normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
	})

	t.Run("real_run_reports_an_unreachable_plane_as_not_current", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		registry := controlPlaneRegistryStub(t, "1.0.247")
		// 127.0.0.1:1 refuses the connection immediately (the same
		// unreachable-registry trick pin_test.go's
		// seedUnreachableRuntimeRegistry uses), modeling a plane that never
		// answers -- it must never be reported current.
		seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": "http://127.0.0.1:1"}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "reachable=no") {
			t.Fatalf("expected the unreachable plane to be reported unreachable:\n%s", result.Combined)
		}
		if strings.Contains(result.Combined, "behind published") || strings.Contains(result.Combined, "ahead of published") {
			t.Fatalf("an unreachable plane must never carry a behind/ahead verdict:\n%s", result.Combined)
		}
		golden.Equal(t, "list/control_planes_real_run_reports_an_unreachable_plane_as_not_current",
			normalize.Apply(result.Combined, stubServerRule(registry, "<REGISTRY_API>")))
	})

	t.Run("real_run_with_no_configured_planes", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, nil, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/control_planes_real_run_with_no_configured_planes",
			normalize.Apply(result.Combined, stubServerRule(registry, "<REGISTRY_API>")))
	})

	t.Run("real_run_json_output", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		plane := controlPlaneStub(t, "1.0.245")
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		for _, want := range []string{`"behind": true`, `"reachable": true`, `"publishedVersion": "1.0.247"`} {
			if !strings.Contains(result.Combined, want) {
				t.Fatalf("expected %q in JSON output:\n%s", want, result.Combined)
			}
		}
	})
}
