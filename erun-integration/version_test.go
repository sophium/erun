package integration

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func writeRuntimeRegistryConfig(t testing.TB, setup env.Setup, body string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write registry config: %v", err)
	}
}

func TestVersion(t *testing.T) {
	t.Parallel()
	t.Run("no_registry", testVersionNoRegistry)
	t.Run("time_flag_prints_elapsed", testVersionTimeFlagPrintsElapsed)
	t.Run("project_version_file_does_not_shadow_the_erun_version", testVersionProjectVersionFileDoesNotShadowTheErunVersion)
	t.Run("json_output_separates_the_erun_and_project_versions", testVersionJsonOutputSeparatesTheErunAndProjectVersions)
	t.Run("registry_dockerhub_stub_returns_latest_stable_and_snapshot", testVersionRegistryDockerhubStubReturnsLatestStableAndSnapshot)
	t.Run("registry_ghcr_stub_returns_latest_stable", testVersionRegistryGhcrStubReturnsLatestStable)
	t.Run("registry_ghcr_authenticates_with_docker_config_inline_auth", testVersionRegistryGhcrAuthenticatesWithDockerConfigInlineAuth)
	t.Run("registry_ghcr_authenticates_via_docker_credential_helper", testVersionRegistryGhcrAuthenticatesViaDockerCredentialHelper)
	t.Run("registry_defaults_namespace_and_repository", testVersionRegistryDefaultsNamespaceAndRepository)
	t.Run("registry_dockerhub_error_is_debug_logged_not_fatal", testVersionRegistryDockerhubErrorIsDebugLoggedNotFatal)
	t.Run("registry_ghcr_token_error_is_debug_logged_not_fatal", testVersionRegistryGhcrTokenErrorIsDebugLoggedNotFatal)
	t.Run("registry_ghcr_tags_error_is_debug_logged_not_fatal", testVersionRegistryGhcrTagsErrorIsDebugLoggedNotFatal)
	t.Run("registry_ghcr_access_token_fallback_and_link_edge_cases", testVersionRegistryGhcrAccessTokenFallbackAndLinkEdgeCases)
	t.Run("registry_dockerhub_tag_classification_edge_cases", testVersionRegistryDockerhubTagClassificationEdgeCases)
	t.Run("verbose_flag_prints_audit", testVersionVerboseFlagPrintsAudit)
}

func testVersionNoRegistry(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"version", "--no-registry"}, erun.RunOptions{
		Cwd: setup.Cwd,
		Env: setup.Env(),
	})
	if result.ExitCode != 0 {
		t.Fatalf("erun version --no-registry exited %d:\n%s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "version/no_registry", normalize.Apply(result.Combined))
}

func testVersionTimeFlagPrintsElapsed(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"version", "--no-registry", "--time"}, erun.RunOptions{
		Cwd: setup.Cwd,
		Env: setup.Env(),
	})
	if result.ExitCode != 0 {
		t.Fatalf("erun version --time exited %d:\n%s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "version/time_flag_prints_elapsed", normalize.Apply(result.Combined))
}

func testVersionProjectVersionFileDoesNotShadowTheErunVersion(t *testing.T) {
	// A VERSION file is the project's version; the erun line stays the build version.
	setup := env.New(t)
	if err := os.WriteFile(filepath.Join(setup.Cwd, "VERSION"), []byte("9.9.9\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	result := erun.Run(t, []string{"version", "--no-registry"}, erun.RunOptions{
		Cwd: setup.Cwd,
		Env: setup.Env(),
	})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "version/project_version_file_does_not_shadow_the_erun_version", normalize.Apply(result.Combined))
}

func testVersionJsonOutputSeparatesTheErunAndProjectVersions(t *testing.T) {
	setup := env.New(t)
	if err := os.WriteFile(filepath.Join(setup.Cwd, "VERSION"), []byte("9.9.9\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	result := erun.Run(t, []string{"version", "--no-registry", "--output", "json"}, erun.RunOptions{
		Cwd: setup.Cwd,
		Env: setup.Env(),
	})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "version/json_output_separates_the_erun_and_project_versions", normalize.Apply(result.Combined))
}

func testVersionRegistryDockerhubStubReturnsLatestStableAndSnapshot(t *testing.T) {
	page1Path := "/v2/repositories/acme/erun-devops/tags?page_size=100"
	var page2Path string
	var stableLatest, snapshotLatest string
	var server *httptest.Server
	// Assigned before Start so the handler's reads happen-after them.
	page2Path = "/v2/repositories/acme/erun-devops/tags?page=2"
	server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path + "?" + r.URL.RawQuery {
		case page1Path:
			next := server.URL + page2Path
			stableLatest = "1.4.0"
			snapshotLatest = "1.5.0-snapshot-20260101000000"
			_, _ = fmt.Fprintf(w, `{"next": %q, "results":[{"name":"1.3.9"},{"name":"1.4.0"},{"name":"latest"}]}`, next)
		case strings.TrimPrefix(page2Path, ""):
			_, _ = fmt.Fprintf(w, `{"next":"","results":[{"name":"1.5.0-snapshot-20260101000000"},{"name":"1.5.0-snapshot-20251231000000"}]}`)
		default:
			http.Error(w, "unexpected request "+r.URL.String(), http.StatusNotFound)
		}
	}))
	server.Start()
	t.Cleanup(server.Close)

	setup := env.New(t)
	writeRuntimeRegistryConfig(t, setup, "runtimeregistry:\n"+
		"  namespace: acme\n"+
		"  repository: erun-devops\n"+
		"  baseurl: "+server.URL+"\n")
	result := erun.Run(t, []string{"version"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	// Normalization would mask these versions, so assert substrings.
	if !strings.Contains(result.Stdout, "latest stable: "+stableLatest) {
		t.Errorf("expected stdout to include latest stable %q, got:\n%s", stableLatest, result.Stdout)
	}
	if !strings.Contains(result.Stdout, "latest snapshot: "+snapshotLatest) {
		t.Errorf("expected stdout to include latest snapshot %q, got:\n%s", snapshotLatest, result.Stdout)
	}
}

func testVersionRegistryGhcrStubReturnsLatestStable(t *testing.T) {
	var server *httptest.Server
	var requested []string
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.Path+"?"+r.URL.RawQuery)
		switch {
		case r.URL.Path == "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"token":"stub-token"}`)
		case r.URL.Path == "/v2/acme/erun-devops/tags/list" && r.URL.RawQuery == "":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Link", `</v2/acme/erun-devops/tags/list?last=1.4.0>; rel="next"`)
			_, _ = fmt.Fprintf(w, `{"name":"acme/erun-devops","tags":["1.3.9","1.4.0","latest"]}`)
		case r.URL.Path == "/v2/acme/erun-devops/tags/list" && r.URL.RawQuery == "last=1.4.0":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"name":"acme/erun-devops","tags":["1.4.1"]}`)
		default:
			http.Error(w, "unexpected request "+r.URL.String(), http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	setup := env.New(t)
	writeRuntimeRegistryConfig(t, setup, "runtimeregistry:\n"+
		"  namespace: ghcr.io/acme\n"+
		"  repository: erun-devops\n"+
		"  baseurl: "+server.URL+"\n"+
		"  tokenurl: "+server.URL+"\n")
	result := erun.Run(t, []string{"version"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Stdout, "latest stable: 1.4.1") {
		t.Errorf("expected stdout to include latest stable 1.4.1 from paginated GHCR response, got:\n%s", result.Stdout)
	}
	assertStubReceived(t, requested,
		"/token?service=ghcr.io&scope=repository:acme/erun-devops:pull",
		"/v2/acme/erun-devops/tags/list?",
		"/v2/acme/erun-devops/tags/list?last=1.4.0",
	)
}

func assertStubReceived(t *testing.T, requested []string, paths ...string) {
	t.Helper()
	expected := make(map[string]bool, len(paths))
	for _, path := range paths {
		expected[path] = false
	}
	for _, req := range requested {
		expected[req] = true
	}
	for path, hit := range expected {
		if !hit {
			t.Errorf("expected stub to receive %q, got requests:\n%v", path, requested)
		}
	}
}

func testVersionRegistryGhcrAuthenticatesWithDockerConfigInlineAuth(t *testing.T) {
	// Private GHCR tags list with docker's own credential (#750).
	const dockerUser, dockerSecret = "erunbot", "s3cr3t-inline-token"
	var tokenAuthHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenAuthHeader = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"token":"stub-token"}`)
		case "/v2/acme/erun-devops/tags/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"name":"acme/erun-devops","tags":["1.3.9","1.4.0","latest"]}`)
		default:
			http.Error(w, "unexpected request "+r.URL.String(), http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	setup := env.New(t)
	writeRuntimeRegistryConfig(t, setup, "runtimeregistry:\n"+
		"  namespace: ghcr.io/acme\n"+
		"  repository: erun-devops\n"+
		"  baseurl: "+server.URL+"\n"+
		"  tokenurl: "+server.URL+"\n")

	// DOCKER_CONFIG isolates the resolver from the developer's ~/.docker.
	dockerCfgDir := filepath.Join(setup.Cwd, "docker-inline")
	if err := os.MkdirAll(dockerCfgDir, 0o755); err != nil {
		t.Fatalf("mkdir docker config dir: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(dockerUser + ":" + dockerSecret))
	dockerCfg := fmt.Sprintf(`{"auths":{"ghcr.io":{"auth":%q}}}`, encoded)
	if err := os.WriteFile(filepath.Join(dockerCfgDir, "config.json"), []byte(dockerCfg), 0o644); err != nil {
		t.Fatalf("write docker config: %v", err)
	}

	envVars := append(setup.Env(), "DOCKER_CONFIG="+dockerCfgDir)
	result := erun.Run(t, []string{"version"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Stdout, "latest stable: 1.4.0") {
		t.Errorf("expected stdout to include latest stable 1.4.0, got:\n%s", result.Stdout)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(dockerUser+":"+dockerSecret))
	if tokenAuthHeader != wantAuth {
		t.Errorf("expected GHCR token request to carry inline docker-config Basic auth %q, got %q", wantAuth, tokenAuthHeader)
	}
}

func testVersionRegistryGhcrAuthenticatesViaDockerCredentialHelper(t *testing.T) {
	// Credential-helper arm (#750). The helper is absent from PATH and routed via
	// ERUN_DOCKER_CREDENTIAL_ERUNSTUB_BIN, which on Windows resolves to an .exe runner.
	const dockerUser, dockerSecret = "helperbot", "s3cr3t-helper-token"
	var tokenAuthHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenAuthHeader = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"token":"stub-token"}`)
		case "/v2/acme/erun-devops/tags/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"name":"acme/erun-devops","tags":["1.4.0","1.4.2","latest"]}`)
		default:
			http.Error(w, "unexpected request "+r.URL.String(), http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	setup := env.New(t)
	writeRuntimeRegistryConfig(t, setup, "runtimeregistry:\n"+
		"  namespace: ghcr.io/acme\n"+
		"  repository: erun-devops\n"+
		"  baseurl: "+server.URL+"\n"+
		"  tokenurl: "+server.URL+"\n")

	dockerCfgDir := filepath.Join(setup.Cwd, "docker-helper")
	if err := os.MkdirAll(dockerCfgDir, 0o755); err != nil {
		t.Fatalf("mkdir docker config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dockerCfgDir, "config.json"), []byte(`{"credHelpers":{"ghcr.io":"erunstub"}}`), 0o644); err != nil {
		t.Fatalf("write docker config: %v", err)
	}

	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinary(t, stubs, "docker-credential-erunstub", fmt.Sprintf(`{"Username":%q,"Secret":%q}`, dockerUser, dockerSecret))
	envVars := append(setup.Env(), "DOCKER_CONFIG="+dockerCfgDir)
	envVars = append(envVars, fixture.StubEnv(stubs, "docker-credential-erunstub")...)
	result := erun.Run(t, []string{"version"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Stdout, "latest stable: 1.4.2") {
		t.Errorf("expected stdout to include latest stable 1.4.2, got:\n%s", result.Stdout)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(dockerUser+":"+dockerSecret))
	if tokenAuthHeader != wantAuth {
		t.Errorf("expected GHCR token request to carry credential-helper Basic auth %q, got %q", wantAuth, tokenAuthHeader)
	}
}

func testVersionRegistryDefaultsNamespaceAndRepository(t *testing.T) {
	var server *httptest.Server
	var requested []string
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			_, _ = fmt.Fprintf(w, `{"token":"stub-token"}`)
		case "/v2/sophium/erun-devops/tags/list":
			_, _ = fmt.Fprintf(w, `{"name":"sophium/erun-devops","tags":["1.2.3"]}`)
		default:
			http.Error(w, "unexpected request "+r.URL.String(), http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	setup := env.New(t)
	writeRuntimeRegistryConfig(t, setup, "runtimeregistry:\n"+
		"  baseurl: "+server.URL+"\n"+
		"  tokenurl: "+server.URL+"\n")
	result := erun.Run(t, []string{"version"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Stdout, "latest stable: 1.2.3") {
		t.Errorf("expected default ghcr.io/sophium/erun-devops lookup to surface 1.2.3, got:\n%s", result.Stdout)
	}
	for _, path := range requested {
		if strings.Contains(path, "sophium") {
			return
		}
	}
	t.Errorf("expected the stub to be queried for the default sophium namespace, got requests: %v", requested)
}

func testVersionRegistryDockerhubErrorIsDebugLoggedNotFatal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	setup := env.New(t)
	writeRuntimeRegistryConfig(t, setup, "runtimeregistry:\n"+
		"  namespace: acme\n"+
		"  repository: erun-devops\n"+
		"  baseurl: "+server.URL+"\n")
	result := erun.Run(t, []string{"version", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "version/registry_dockerhub_error_is_debug_logged_not_fatal", normalize.Apply(result.Combined))
}

func testVersionRegistryGhcrTokenErrorIsDebugLoggedNotFatal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	}))
	t.Cleanup(server.Close)

	setup := env.New(t)
	writeRuntimeRegistryConfig(t, setup, "runtimeregistry:\n"+
		"  namespace: ghcr.io/acme\n"+
		"  repository: erun-devops\n"+
		"  baseurl: "+server.URL+"\n"+
		"  tokenurl: "+server.URL+"\n")
	result := erun.Run(t, []string{"version", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "version/registry_ghcr_token_error_is_debug_logged_not_fatal", normalize.Apply(result.Combined))
}

func testVersionRegistryGhcrTagsErrorIsDebugLoggedNotFatal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"token":"stub-token"}`)
			return
		}
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)

	setup := env.New(t)
	writeRuntimeRegistryConfig(t, setup, "runtimeregistry:\n"+
		"  namespace: ghcr.io/acme\n"+
		"  repository: erun-devops\n"+
		"  baseurl: "+server.URL+"\n"+
		"  tokenurl: "+server.URL+"\n")
	result := erun.Run(t, []string{"version", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "version/registry_ghcr_tags_error_is_debug_logged_not_fatal", normalize.Apply(result.Combined))
}

func testVersionRegistryGhcrAccessTokenFallbackAndLinkEdgeCases(t *testing.T) {
	// Malformed or empty rel="next" segments must end pagination, not loop.
	var server *httptest.Server
	var pages []string
	// Assigned before Start so the handler's read happens-after it.
	server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/token":
			_, _ = fmt.Fprintf(w, `{"access_token":"stub-access-token"}`)
		case r.URL.Path == "/v2/acme/erun-devops/tags/list" && r.URL.RawQuery == "":
			pages = append(pages, "page1")
			w.Header().Set("Link", `</v2/acme/erun-devops/tags/list?last=0>; rel="prev", <`+server.URL+`/v2/acme/erun-devops/tags/list?last=1.4.0>; rel="next"`)
			_, _ = fmt.Fprintf(w, `{"name":"acme/erun-devops","tags":["1.4.0"]}`)
		case r.URL.Path == "/v2/acme/erun-devops/tags/list" && r.URL.RawQuery == "last=1.4.0":
			pages = append(pages, "page2")
			w.Header().Set("Link", `malformed; rel="next", <>; rel="next"`)
			_, _ = fmt.Fprintf(w, `{"name":"acme/erun-devops","tags":["1.4.1"]}`)
		default:
			http.Error(w, "unexpected request "+r.URL.String(), http.StatusNotFound)
		}
	}))
	server.Start()
	t.Cleanup(server.Close)

	setup := env.New(t)
	writeRuntimeRegistryConfig(t, setup, "runtimeregistry:\n"+
		"  namespace: ghcr.io/acme\n"+
		"  repository: erun-devops\n"+
		"  baseurl: "+server.URL+"\n"+
		"  tokenurl: "+server.URL+"\n")
	result := erun.Run(t, []string{"version"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Stdout, "latest stable: 1.4.1") {
		t.Errorf("expected both pages parsed (latest stable 1.4.1), got:\n%s", result.Stdout)
	}
	if got := strings.Join(pages, ","); got != "page1,page2" {
		t.Errorf("expected pagination to fetch page1 then stop after page2's malformed Link, got: %s", got)
	}
}

func testVersionRegistryDockerhubTagClassificationEdgeCases(t *testing.T) {
	var server *httptest.Server
	// Assigned before Start so the handler's read happens-after it.
	server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.RawQuery {
		case "page_size=100":
			_, _ = fmt.Fprintf(w, `{"next": %q, "results":[{"name":"1..0"},{"name":"1.-1.0"},{"name":"1.4.0"},{"name":"1.5.0-snapshot-2026010100000x"}]}`, server.URL+r.URL.Path+"?page=2")
		case "page=2":
			_, _ = fmt.Fprintf(w, `{"next":"","results":[{"name":"1.4.0"},{"name":"1.5.0"}]}`)
		default:
			http.Error(w, "unexpected request "+r.URL.String(), http.StatusNotFound)
		}
	}))
	server.Start()
	t.Cleanup(server.Close)

	setup := env.New(t)
	writeRuntimeRegistryConfig(t, setup, "runtimeregistry:\n"+
		"  namespace: acme\n"+
		"  repository: erun-devops\n"+
		"  baseurl: "+server.URL+"\n")
	result := erun.Run(t, []string{"version"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Stdout, "latest stable: 1.5.0") {
		t.Errorf("expected latest stable 1.5.0 (minor comparison wins, malformed tags ignored), got:\n%s", result.Stdout)
	}
	if strings.Contains(result.Stdout, "latest snapshot:") {
		t.Errorf("expected no snapshot line (non-digit snapshot timestamp must be ignored), got:\n%s", result.Stdout)
	}
}

func testVersionVerboseFlagPrintsAudit(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"version", "--no-registry", "-vv"}, erun.RunOptions{
		Cwd: setup.Cwd,
		Env: setup.Env(),
	})
	if result.ExitCode != 0 {
		t.Fatalf("erun version -vv exited %d:\n%s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "version/verbose_flag_prints_audit", normalize.Apply(result.Combined))
}
