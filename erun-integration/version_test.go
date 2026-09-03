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
	t.Run("no_registry", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"version", "--no-registry"}, erun.RunOptions{
			Cwd: setup.Cwd,
			Env: setup.Env(),
		})
		if result.ExitCode != 0 {
			t.Fatalf("erun version --no-registry exited %d:\n%s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "version/no_registry", normalize.Apply(result.Combined))
	})

	t.Run("time_flag_prints_elapsed", func(t *testing.T) {
		// --time must emit an elapsed line on stderr after a successful run.
		setup := env.New(t)
		result := erun.Run(t, []string{"version", "--no-registry", "--time"}, erun.RunOptions{
			Cwd: setup.Cwd,
			Env: setup.Env(),
		})
		if result.ExitCode != 0 {
			t.Fatalf("erun version --time exited %d:\n%s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "version/time_flag_prints_elapsed", normalize.Apply(result.Combined))
	})

	t.Run("project_version_file_does_not_shadow_the_erun_version", func(t *testing.T) {
		// A VERSION file in the working directory is the project's version, not
		// erun's: the `erun` line must stay the compiled-in build version (so it
		// stays comparable with `latest stable:`) and the project version must get
		// its own labelled line.
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
	})

	t.Run("json_output_separates_the_erun_and_project_versions", func(t *testing.T) {
		// The structured result an orchestrator reads must keep the two versions in
		// distinct fields, so neither can be mistaken for the other.
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
	})

	t.Run("registry_dockerhub_stub_returns_latest_stable_and_snapshot", func(t *testing.T) {
		// version must surface the latest stable and latest snapshot across paginated Docker Hub tags.
		page1Path := "/v2/repositories/acme/erun-devops/tags?page_size=100"
		var page2Path string
		var stableLatest, snapshotLatest string
		var server *httptest.Server
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		t.Cleanup(server.Close)
		page2Path = "/v2/repositories/acme/erun-devops/tags?page=2"

		setup := env.New(t)
		writeRuntimeRegistryConfig(t, setup, "runtimeregistry:\n"+
			"  namespace: acme\n"+
			"  repository: erun-devops\n"+
			"  baseurl: "+server.URL+"\n")
		result := erun.Run(t, []string{"version"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		// Substring-asserted, not golden-locked: normalization collapses these
		// versions to <VERSION>, which would erase the stub-driven values under test.
		if !strings.Contains(result.Stdout, "latest stable: "+stableLatest) {
			t.Errorf("expected stdout to include latest stable %q, got:\n%s", stableLatest, result.Stdout)
		}
		if !strings.Contains(result.Stdout, "latest snapshot: "+snapshotLatest) {
			t.Errorf("expected stdout to include latest snapshot %q, got:\n%s", snapshotLatest, result.Stdout)
		}
	})

	t.Run("registry_ghcr_stub_returns_latest_stable", func(t *testing.T) {
		// version must follow the GHCR rel="next" Link header across pages and surface the latest stable.
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
		// A pagination-parser regression should fail here explicitly, not only via the stdout assertion.
		expected := map[string]bool{
			"/token?service=ghcr.io&scope=repository:acme/erun-devops:pull": false,
			"/v2/acme/erun-devops/tags/list?":                               false,
			"/v2/acme/erun-devops/tags/list?last=1.4.0":                     false,
		}
		for _, req := range requested {
			expected[req] = true
		}
		for path, hit := range expected {
			if !hit {
				t.Errorf("expected stub to receive %q, got requests:\n%v", path, requested)
			}
		}
	})

	t.Run("registry_ghcr_authenticates_with_docker_config_inline_auth", func(t *testing.T) {
		// #750: registry version listing authenticates the GHCR token exchange with
		// the credential docker itself pulls with, so private tenant images list
		// their tags instead of failing anonymously. Seed an isolated docker config
		// with an inline ghcr.io auth entry and assert the token request carries HTTP
		// Basic auth — the credential-resolution logic is otherwise only unit-tested
		// via package-var seams the integration gate cannot reach.
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

		// An inline auths entry keyed by the bare host is what docker stores for
		// `docker login ghcr.io`; DOCKER_CONFIG points the resolver at this isolated
		// dir instead of the developer's ~/.docker.
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
	})

	t.Run("registry_ghcr_authenticates_via_docker_credential_helper", func(t *testing.T) {
		// #750, credential-helper arm: docker keeps most registry credentials in a
		// per-host credential helper (Docker Desktop/OrbStack keychains), not inline.
		// A credHelpers entry routes ghcr.io to `docker-credential-<helper> get`; the
		// resolver must run it and Basic-authenticate the token exchange with what it
		// returns. The helper name is deliberately absent from PATH so the resolution
		// is deterministic, and the stub is routed via ERUN_DOCKER_CREDENTIAL_ERUNSTUB_BIN
		// — which on Windows resolves to the fixture's argv-faithful .exe runner,
		// since Windows will not execute a shebang script by name.
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

		// docker-credential-erunstub get prints the credential docker would hand back
		// on stdin lookup; the resolver Basic-authenticates the token request with it.
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
	})

	t.Run("registry_defaults_namespace_and_repository", func(t *testing.T) {
		// A registry config that overrides only the URLs must default to the canonical ghcr.io/sophium/erun-devops image.
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
	})

	t.Run("registry_dockerhub_error_is_debug_logged_not_fatal", func(t *testing.T) {
		// A registry lookup failure must not fail erun version: the error is
		// debug-logged and the build-info line still prints.
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
	})

	t.Run("registry_ghcr_token_error_is_debug_logged_not_fatal", func(t *testing.T) {
		// A GHCR token-endpoint failure degrades to the same non-fatal, debug-logged behavior as any other registry failure.
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
	})

	t.Run("registry_ghcr_tags_error_is_debug_logged_not_fatal", func(t *testing.T) {
		// When the GHCR token resolves but the tags request fails, erun version still exits 0.
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
	})

	t.Run("registry_ghcr_access_token_fallback_and_link_edge_cases", func(t *testing.T) {
		// Two GHCR parsing edge cases: the token endpoint may return access_token
		// instead of token, and malformed or empty rel="next" Link segments must
		// terminate pagination rather than loop forever.
		var server *httptest.Server
		var pages []string
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	})

	t.Run("registry_dockerhub_tag_classification_edge_cases", func(t *testing.T) {
		// Tag classification ignores malformed stable tags and non-digit snapshot
		// timestamps, dedupes tags across pages, and compares minor versions correctly.
		var server *httptest.Server
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	})

	t.Run("verbose_flag_prints_audit", func(t *testing.T) {
		// The audit line appears at -vv (trace) but is suppressed at -v (debug).
		setup := env.New(t)
		result := erun.Run(t, []string{"version", "--no-registry", "-vv"}, erun.RunOptions{
			Cwd: setup.Cwd,
			Env: setup.Env(),
		})
		if result.ExitCode != 0 {
			t.Fatalf("erun version -vv exited %d:\n%s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "version/verbose_flag_prints_audit", normalize.Apply(result.Combined))
	})
}
