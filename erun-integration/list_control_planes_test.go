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
// for a deployed control plane. An optional consoleURL is reported as the
// response's consoleUrl field -- erun#2070's discovery mechanism: a plane and
// its console are never configured as separate aliases, so the console
// version check finds its target here, the same call that reports the
// plane's own version.
func controlPlaneStub(t testing.TB, version string, consoleURL ...string) *httptest.Server {
	t.Helper()
	console := ""
	if len(consoleURL) > 0 {
		console = consoleURL[0]
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"version":"%s","consoleUrl":"%s"}`, version, console)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// controlPlaneStubWithAPIURL serves GET /v1/platform the same way
// controlPlaneStub does, but also reports its own apiUrl field -- the
// discovery-document field this is about. controlPlaneStub leaves it
// empty because most scenarios have nothing to say about it; this one
// exists for the scenarios that do.
func controlPlaneStubWithAPIURL(t testing.TB, version, apiURL string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"version":"%s","apiUrl":"%s"}`, version, apiURL)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// consoleStub serves GET /version.json reporting version, standing in for a
// deployed console (erun-devops/docker/erun-console's own static file,
// stamped from ERUN_VERSION at image build time).
func consoleStub(t testing.TB, version string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /version.json", func(w http.ResponseWriter, _ *http.Request) {
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

	t.Run("real_run_reports_a_console_behind_published", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		console := consoleStub(t, "1.0.245")
		plane := controlPlaneStub(t, "1.0.247", console.URL)
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/control_planes_real_run_reports_a_console_behind_published",
			normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(console, "<CONSOLE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
	})

	t.Run("real_run_reports_a_console_ahead_of_published", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		console := consoleStub(t, "1.0.999")
		plane := controlPlaneStub(t, "1.0.247", console.URL)
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/control_planes_real_run_reports_a_console_ahead_of_published",
			normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(console, "<CONSOLE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
	})

	t.Run("real_run_reports_an_unreachable_console_as_not_current", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		// The plane itself is reachable and reports a consoleUrl nothing
		// listens on -- a plane and its console can go down independently,
		// and the console must never be reported current just because its
		// plane is.
		plane := controlPlaneStub(t, "1.0.247", "http://127.0.0.1:1")
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/control_planes_real_run_reports_an_unreachable_console_as_not_current",
			normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
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
		console := consoleStub(t, "1.0.245")
		plane := controlPlaneStub(t, "1.0.245", console.URL)
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		for _, want := range []string{`"behind": true`, `"reachable": true`, `"publishedVersion": "1.0.247"`, `"console"`} {
			if !strings.Contains(result.Combined, want) {
				t.Fatalf("expected %q in JSON output:\n%s", want, result.Combined)
			}
		}
	})

	// erun#2052: --fail-on-drift is the opt-in that lets this reporting
	// command's own finding be wired into a gate -- without it this whole
	// file's scenarios are right that the command always exits 0
	// (erun-cli/AGENTS.md § "Exit-Code Contract: Reporting Commands Vs
	// Gating Checks").

	t.Run("fail_on_drift_behind_published_exits_non_zero", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		plane := controlPlaneStub(t, "1.0.245")
		registry := controlPlaneRegistryStub(t, "1.0.246", "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes", "--fail-on-drift"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a plane behind published, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "list/control_planes_fail_on_drift_behind_published_exits_non_zero",
			normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
	})

	t.Run("fail_on_drift_up_to_date_exits_zero", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		plane := controlPlaneStub(t, "1.0.247")
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes", "--fail-on-drift"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("expected exit 0 for a plane already at the published version, got %d:\n%s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/control_planes_fail_on_drift_up_to_date_exits_zero",
			normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
	})

	t.Run("fail_on_drift_console_behind_published_exits_non_zero", func(t *testing.T) {
		// The plane itself is at the published version -- only its linked
		// console is behind. --fail-on-drift must catch console drift the
		// same as plane drift, since a console has no version surface of its
		// own to notice this without the check.
		t.Parallel()
		setup := env.New(t)
		console := consoleStub(t, "1.0.245")
		plane := controlPlaneStub(t, "1.0.247", console.URL)
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes", "--fail-on-drift"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a console behind published, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "list/control_planes_fail_on_drift_console_behind_published_exits_non_zero",
			normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(console, "<CONSOLE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
	})

	t.Run("fail_on_drift_unreachable_plane_exits_non_zero", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": "http://127.0.0.1:1"}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes", "--fail-on-drift"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for an unreachable plane, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "list/control_planes_fail_on_drift_unreachable_plane_exits_non_zero",
			normalize.Apply(result.Combined, stubServerRule(registry, "<REGISTRY_API>")))
	})

	t.Run("fail_on_drift_dry_run_never_fails", func(t *testing.T) {
		// Nothing was actually probed, so there is nothing to fail on --
		// --fail-on-drift must not turn a dry-run preview into a failure.
		t.Parallel()
		setup := env.New(t)
		plane := controlPlaneStub(t, "1.0.245")
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes", "--fail-on-drift", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("expected exit 0 for a dry run, got %d:\n%s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/control_planes_fail_on_drift_dry_run_never_fails",
			normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
	})

	// Two configured aliases can resolve to the very same backend (a vanity
	// hostname CNAMEing to the per-env one, as verified live against
	// `api.erunpaas.com`/`api.frs-prod.services.erunpaas.com`), and a plane's
	// own discovery document could in principle advertise an apiUrl belonging
	// to a genuinely different plane. Both are checked by resolving hostnames
	// to addresses, which these scenarios drive with real (loopback and
	// reserved-block literal) addresses -- no DNS stub needed.

	t.Run("real_run_dedups_two_aliases_pointing_at_the_same_backend", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		plane := controlPlaneStub(t, "1.0.247")
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{
			"erun+a-vanity@erun": plane.URL,
			"erun+b-real@erun":   plane.URL,
		}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "same-backend-as=erun+b-real@erun") {
			t.Fatalf("expected the duplicate alias folded into the representative's row:\n%s", result.Combined)
		}
		if strings.Count(result.Combined, "api-url=") != 1 {
			t.Fatalf("expected the two aliases reported as a single plane, got:\n%s", result.Combined)
		}
		golden.Equal(t, "list/control_planes_real_run_dedups_two_aliases_pointing_at_the_same_backend",
			normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
	})

	t.Run("real_run_flags_a_foreign_advertised_apiurl", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		// The plane's own discovery document names an apiUrl resolving to an
		// address this plane's own configured host shares nothing with --
		// the actual misconfiguration this check exists to catch.
		plane := controlPlaneStubWithAPIURL(t, "1.0.247", "http://[2001:db8::1]:9999")
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "advertised apiUrl mismatch") {
			t.Fatalf("expected a mismatch to be flagged:\n%s", result.Combined)
		}
		golden.Equal(t, "list/control_planes_real_run_flags_a_foreign_advertised_apiurl",
			normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
	})

	t.Run("fail_on_drift_foreign_advertised_apiurl_exits_non_zero", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		plane := controlPlaneStubWithAPIURL(t, "1.0.247", "http://[2001:db8::1]:9999")
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes", "--fail-on-drift"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a foreign advertised apiUrl, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "list/control_planes_fail_on_drift_foreign_advertised_apiurl_exits_non_zero",
			normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
	})

	t.Run("real_run_json_output_reports_duplicate_and_mismatch_fields", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		plane := controlPlaneStubWithAPIURL(t, "1.0.247", "http://[2001:db8::1]:9999")
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{
			"erun+a-vanity@erun": plane.URL,
			"erun+b-real@erun":   plane.URL,
		}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		for _, want := range []string{`"duplicateAliases"`, `"erun+b-real@erun"`, `"advertisedApiUrlMismatch"`} {
			if !strings.Contains(result.Combined, want) {
				t.Fatalf("expected %q in JSON output:\n%s", want, result.Combined)
			}
		}
	})
}
