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
// plane's own version. Reports its own listener address as apiUrl, the same
// as a real erun-backend-api reporting its own configured PlatformAPIURL --
// this is what lets the duplicate-alias collapsing key on it instead
// of falling back to DNS in the common single-alias case.
func controlPlaneStub(t testing.TB, version string, consoleURL ...string) *httptest.Server {
	t.Helper()
	console := ""
	if len(consoleURL) > 0 {
		console = consoleURL[0]
	}
	var server *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		apiURL := ""
		if server != nil {
			apiURL = server.URL
		}
		_, _ = fmt.Fprintf(w, `{"version":"%s","apiUrl":"%s","consoleUrl":"%s"}`, version, apiURL, console)
	})
	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// controlPlaneStubAt is like controlPlaneStub but reports identityAPIURL as
// its own self-declared /v1/platform apiUrl instead of its own listener
// address -- modeling a backend reachable through more than one
// hostname/alias that always reports the same canonical apiUrl regardless of
// which alias a client dialed. Passing another stub's own URL as
// identityAPIURL simulates two configured aliases resolving to one backend.
func controlPlaneStubAt(t testing.TB, identityAPIURL, version string, consoleURL ...string) *httptest.Server {
	t.Helper()
	console := ""
	if len(consoleURL) > 0 {
		console = consoleURL[0]
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"version":"%s","apiUrl":"%s","consoleUrl":"%s"}`, version, identityAPIURL, console)
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

// consoleHTMLFallbackStub serves GET /version.json with a 200 text/html SPA
// index page instead of the expected JSON document -- the deployed nginx
// `try_files $uri $uri/ /index.html` fallback for a route it doesn't yet
// exact-match, reproduced here rather than assumed from reading the code.
func consoleHTMLFallbackStub(t testing.TB) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /version.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "<!doctype html><html><body>console spa</body></html>")
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
// and pin_test.go already use for each half alone. A plain map is fine here
// because no scenario using it configures more than one alias; a scenario
// that needs an ordered, multi-alias list (the duplicate-alias
// collapsing, where which alias is configured first decides which one
// becomes the collapsed plane's primary entry) uses
// seedControlPlaneConfigOrdered instead, since Go map iteration order is
// randomized.
func seedControlPlaneConfig(t testing.TB, setup env.Setup, planes map[string]string, registryURL string) {
	t.Helper()
	ordered := make([]controlPlaneAliasSeed, 0, len(planes))
	for alias, apiURL := range planes {
		ordered = append(ordered, controlPlaneAliasSeed{Alias: alias, APIURL: apiURL})
	}
	seedControlPlaneConfigOrdered(t, setup, ordered, registryURL)
}

// controlPlaneAliasSeed is one configured erun-type cloud provider alias for
// seedControlPlaneConfigOrdered.
type controlPlaneAliasSeed struct {
	Alias  string
	APIURL string
}

func seedControlPlaneConfigOrdered(t testing.TB, setup env.Setup, planes []controlPlaneAliasSeed, registryURL string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	var body strings.Builder
	if len(planes) > 0 {
		body.WriteString("cloudproviders:\n")
		for _, plane := range planes {
			body.WriteString("  - alias: " + plane.Alias + "\n")
			body.WriteString("    provider: erun\n")
			body.WriteString("    erun:\n")
			body.WriteString("      apiurl: " + plane.APIURL + "\n")
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

	// Every other platform-touching command (`gate list`, `platform whoami`,
	// ...) accepts --erun-alias to narrow its work to one configured alias,
	// but --control-planes rejected it outright and unconditionally probed
	// every configured erun-hosted alias instead.

	t.Run("erun_alias_requires_control_planes", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		result := erun.Run(t, []string{"list", "--erun-alias", "erun+test@erun"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for --erun-alias without --control-planes, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "list/control_planes_erun_alias_requires_control_planes", normalize.Apply(result.Combined))
	})

	t.Run("erun_alias_unconfigured_errors", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		plane := controlPlaneStub(t, "1.0.247")
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes", "--erun-alias", "erun+nonexistent@erun"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for an unconfigured --erun-alias, got 0:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, `cloud provider alias "erun+nonexistent@erun" is not configured`) {
			t.Fatalf("expected the error to name the unconfigured alias:\n%s", result.Combined)
		}
		golden.Equal(t, "list/control_planes_erun_alias_unconfigured_errors",
			normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
	})

	t.Run("erun_alias_narrows_the_check_to_one_configured_plane", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		planeA := controlPlaneStub(t, "1.0.247")
		planeB := controlPlaneStub(t, "1.0.247")
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{
			"erun+a@erun": planeA.URL,
			"erun+b@erun": planeB.URL,
		}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes", "--erun-alias", "erun+b@erun"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "erun+b@erun") {
			t.Fatalf("expected the narrowed-to alias to be reported:\n%s", result.Combined)
		}
		if strings.Contains(result.Combined, "erun+a@erun") {
			t.Fatalf("expected the other configured alias to never be probed or reported:\n%s", result.Combined)
		}
		if strings.Count(result.Combined, "GET ") != 2 {
			t.Fatalf("expected exactly one plane probed (its trace line plus its actual GET), got:\n%s", result.Combined)
		}
		golden.Equal(t, "list/control_planes_erun_alias_narrows_the_check_to_one_configured_plane",
			normalize.Apply(result.Combined, stubServerRule(planeA, "<PLANE_A_API>"), stubServerRule(planeB, "<PLANE_B_API>"), stubServerRule(registry, "<REGISTRY_API>")))
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

	t.Run("real_run_reports_a_console_serving_html_as_reachable_with_unknown_version", func(t *testing.T) {
		// A console that answers GET /version.json with 200 text/html (the
		// SPA's own index.html, served by an nginx fallback that doesn't yet
		// exact-match the route) must be reported reachable -- it answered
		// -- with an unknown version and a reason naming the status code
		// and content type, never reachable=no.
		t.Parallel()
		setup := env.New(t)
		console := consoleHTMLFallbackStub(t)
		plane := controlPlaneStub(t, "1.0.247", console.URL)
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		consoleLineIdx := strings.Index(result.Combined, "console: url=")
		if consoleLineIdx == -1 {
			t.Fatalf("expected a console entry:\n%s", result.Combined)
		}
		consoleLine := result.Combined[consoleLineIdx:]
		if nl := strings.IndexByte(consoleLine, '\n'); nl != -1 {
			consoleLine = consoleLine[:nl]
		}
		if !strings.Contains(consoleLine, "reachable=yes") {
			t.Fatalf("expected the console to be reported reachable despite the wrong content type:\n%s", consoleLine)
		}
		if !strings.Contains(consoleLine, "version=unknown") {
			t.Fatalf("expected the console's version to be reported unknown:\n%s", consoleLine)
		}
		if !strings.Contains(consoleLine, "/version.json returned 200 text/html (expected application/json)") {
			t.Fatalf("expected the reason to name the status code and content type:\n%s", consoleLine)
		}
		golden.Equal(t, "list/control_planes_real_run_reports_a_console_serving_html_as_reachable_with_unknown_version",
			normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(console, "<CONSOLE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
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

	// Two configured aliases that resolve to the same backend must
	// be reported as one plane, not two -- pointing two aliases at one plane
	// is legitimate configuration, but reporting it as two double-counts
	// drift an operator would only ever act on once. Identity is keyed on
	// each alias's own self-declared GET /v1/platform apiUrl (see
	// controlPlaneBackendIdentity in erun-common); controlPlaneStubAt models
	// two distinct listeners that both report that same canonical apiUrl,
	// the same as one real backend answering on two hostnames/ingresses.

	t.Run("real_run_two_aliases_sharing_a_backend_collapse_into_one_plane", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		canonical := controlPlaneStubAt(t, "https://api.example-canonical.test", "1.0.245")
		alias := controlPlaneStubAt(t, "https://api.example-canonical.test", "1.0.245")
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfigOrdered(t, setup, []controlPlaneAliasSeed{
			{Alias: "erun+canonical@erun", APIURL: canonical.URL},
			{Alias: "erun+alias@erun", APIURL: alias.URL},
		}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "Control planes (1 backend, 2 aliases):") {
			t.Fatalf("expected the two aliases to collapse into one reported backend:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "erun+canonical@erun") || !strings.Contains(result.Combined, "erun+alias@erun") {
			t.Fatalf("expected both configured aliases to remain visible:\n%s", result.Combined)
		}
		golden.Equal(t, "list/control_planes_real_run_two_aliases_sharing_a_backend_collapse_into_one_plane",
			normalize.Apply(result.Combined, stubServerRule(canonical, "<PLANE_API>"), stubServerRule(alias, "<ALIAS_API>"), stubServerRule(registry, "<REGISTRY_API>")))
	})

	t.Run("fail_on_drift_two_aliases_sharing_a_backend_counts_once", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		canonical := controlPlaneStubAt(t, "https://api.example-canonical.test", "1.0.245")
		alias := controlPlaneStubAt(t, "https://api.example-canonical.test", "1.0.245")
		registry := controlPlaneRegistryStub(t, "1.0.246", "1.0.247")
		seedControlPlaneConfigOrdered(t, setup, []controlPlaneAliasSeed{
			{Alias: "erun+canonical@erun", APIURL: canonical.URL},
			{Alias: "erun+alias@erun", APIURL: alias.URL},
		}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes", "--fail-on-drift"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a plane behind published, got 0:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "1 plane(s) behind published") {
			t.Fatalf("expected drift to count one deployment, not one per alias:\n%s", result.Combined)
		}
		if strings.Contains(result.Combined, "2 plane(s) behind published") {
			t.Fatalf("two aliases of the same backend must not double-count as two planes behind published:\n%s", result.Combined)
		}
		golden.Equal(t, "list/control_planes_fail_on_drift_two_aliases_sharing_a_backend_counts_once",
			normalize.Apply(result.Combined, stubServerRule(canonical, "<PLANE_API>"), stubServerRule(alias, "<ALIAS_API>"), stubServerRule(registry, "<REGISTRY_API>")))
	})

	t.Run("real_run_json_output_two_aliases_sharing_a_backend_does_not_double_count", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		canonical := controlPlaneStubAt(t, "https://api.example-canonical.test", "1.0.245")
		alias := controlPlaneStubAt(t, "https://api.example-canonical.test", "1.0.245")
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfigOrdered(t, setup, []controlPlaneAliasSeed{
			{Alias: "erun+canonical@erun", APIURL: canonical.URL},
			{Alias: "erun+alias@erun", APIURL: alias.URL},
		}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, `"additionalAliases"`) {
			t.Fatalf("expected the second alias to appear as an additional alias, not a second plane:\n%s", result.Combined)
		}
		if got := strings.Count(result.Combined, `"reachable": true`); got != 1 {
			t.Fatalf("expected exactly one reachable plane entry in the structured output, got %d:\n%s", got, result.Combined)
		}
	})

	// Follow-up: identity must never be guessed from DNS/host alone
	// when neither alias's own GET /v1/platform self-reports an apiUrl -- an
	// older platform that predates the field, modeled here via
	// controlPlaneStubAt with an empty identityAPIURL. Both stubs listen on
	// 127.0.0.1 (only their ports differ), which is exactly the shape that
	// used to collapse incorrectly: a DNS-based fallback identity resolved
	// both hosts to the same loopback address and dropped the port,
	// silently merging two genuinely distinct backends into one reported
	// plane. erun-common no longer keys identity on DNS at all when the
	// self-declared signal is missing, so these must stay two backends.

	t.Run("real_run_two_aliases_with_no_self_declared_identity_stay_separate", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		first := controlPlaneStubAt(t, "", "1.0.245")
		second := controlPlaneStubAt(t, "", "1.0.245")
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfigOrdered(t, setup, []controlPlaneAliasSeed{
			{Alias: "erun+first@erun", APIURL: first.URL},
			{Alias: "erun+second@erun", APIURL: second.URL},
		}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "Control planes (2 backends, 2 aliases):") {
			t.Fatalf("expected two distinct backends with no self-declared identity to stay separate, not collapse:\n%s", result.Combined)
		}
		if strings.Contains(result.Combined, "additionalAliases") {
			t.Fatalf("neither alias should be folded into the other as an additional alias:\n%s", result.Combined)
		}
		golden.Equal(t, "list/control_planes_real_run_two_aliases_with_no_self_declared_identity_stay_separate",
			normalize.Apply(result.Combined, stubServerRule(first, "<FIRST_API>"), stubServerRule(second, "<SECOND_API>"), stubServerRule(registry, "<REGISTRY_API>")))
	})

	t.Run("real_run_json_output_two_aliases_with_no_self_declared_identity_stay_separate", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		first := controlPlaneStubAt(t, "", "1.0.245")
		second := controlPlaneStubAt(t, "", "1.0.245")
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfigOrdered(t, setup, []controlPlaneAliasSeed{
			{Alias: "erun+first@erun", APIURL: first.URL},
			{Alias: "erun+second@erun", APIURL: second.URL},
		}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if strings.Contains(result.Combined, `"additionalAliases"`) {
			t.Fatalf("expected no additional-alias merge between two distinct, unidentified backends:\n%s", result.Combined)
		}
		if got := strings.Count(result.Combined, `"reachable": true`); got != 2 {
			t.Fatalf("expected two reachable plane entries in the structured output, got %d:\n%s", got, result.Combined)
		}
	})

	// A plane's own discovery document can name an apiUrl that is not this
	// backend. A textually different apiUrl is common and benign (the
	// collapsing scenarios above model exactly that), so the check resolves
	// both hostnames and flags only an address this plane's own host shares
	// nothing with. These scenarios drive it with literal addresses -- the
	// configured stub's loopback listener against the reserved, never-routed
	// 2001:db8::/32 documentation prefix -- so no DNS stub is needed.

	t.Run("real_run_flags_a_foreign_advertised_apiurl", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		plane := controlPlaneStubAt(t, "http://[2001:db8::1]:9999", "1.0.247")
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "advertised apiUrl mismatch") {
			t.Fatalf("expected the plane's foreign advertised apiUrl to be flagged:\n%s", result.Combined)
		}
		golden.Equal(t, "list/control_planes_real_run_flags_a_foreign_advertised_apiurl",
			normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
	})

	t.Run("fail_on_drift_foreign_advertised_apiurl_exits_non_zero", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		plane := controlPlaneStubAt(t, "http://[2001:db8::1]:9999", "1.0.247")
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

		// The plane is at the published version, so nothing else in this
		// report is drift -- a foreign advertised apiUrl has to fail the run
		// on its own.
		result := erun.Run(t, []string{"list", "--control-planes", "--fail-on-drift"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for a foreign advertised apiUrl, got 0:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "1 plane(s) advertising a foreign apiUrl: erun+test@erun") {
			t.Fatalf("expected the failure to name the plane advertising a foreign apiUrl:\n%s", result.Combined)
		}
		golden.Equal(t, "list/control_planes_fail_on_drift_foreign_advertised_apiurl_exits_non_zero",
			normalize.Apply(result.Combined, stubServerRule(plane, "<PLANE_API>"), stubServerRule(registry, "<REGISTRY_API>")))
	})

	t.Run("real_run_json_output_reports_the_advertised_apiurl_mismatch", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		plane := controlPlaneStubAt(t, "http://[2001:db8::1]:9999", "1.0.247")
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, `"advertisedApiUrlMismatch"`) {
			t.Fatalf("expected the mismatch in the structured output:\n%s", result.Combined)
		}
	})

	t.Run("real_run_json_output_omits_the_advertised_apiurl_mismatch_when_the_plane_agrees", func(t *testing.T) {
		t.Parallel()
		setup := env.New(t)
		// controlPlaneStub advertises its own listener address, the same one
		// erun dialed -- so the field has to be a signal, not a constant.
		plane := controlPlaneStub(t, "1.0.247")
		registry := controlPlaneRegistryStub(t, "1.0.247")
		seedControlPlaneConfig(t, setup, map[string]string{"erun+test@erun": plane.URL}, registry.URL)

		result := erun.Run(t, []string{"list", "--control-planes", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if strings.Contains(result.Combined, `"advertisedApiUrlMismatch"`) {
			t.Fatalf("expected no mismatch for a plane advertising the address erun reached:\n%s", result.Combined)
		}
	})
}
