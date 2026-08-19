package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// seedCachedERunAccessToken writes a cached, unexpired access token directly
// (bypassing the OIDC device flow cloud_test.go's real-run scenarios already
// cover) so a platform scenario can hit the real erun-backend-api stub
// authenticated from its first request.
func seedCachedERunAccessToken(t testing.TB, setup env.Setup, alias, token string) {
	t.Helper()
	dir := filepath.Join(setup.ConfigHome, "erun", "cloud-secrets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	sum := sha256.Sum256([]byte("erun/access/" + alias))
	path := filepath.Join(dir, hex.EncodeToString(sum[:])+".token")
	body := `{"accessToken":"` + token + `","expiresAt":"2999-01-01T00:00:00Z"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write cached access token: %v", err)
	}
}

// platformAlias seeds a minimal erun-type cloud alias pointed at server, with
// a cached access token already in place — the alias `erun platform`
// commands resolve by default when exactly one erun-type alias is configured.
func platformAlias(t testing.TB, setup env.Setup, server *httptest.Server) string {
	t.Helper()
	alias := "erun+test@erun"
	seedERunCloudProviderAlias(t, setup, alias, server.URL, "cli-test-client")
	seedCachedERunAccessToken(t, setup, alias, "test-access-token")
	return alias
}

// requireBearer answers 401 unless the request carries the seeded test token,
// so a scenario proves the CLI actually attached the bearer it minted.
func requireBearer(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") != "Bearer test-access-token" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

// platformAPIStubServer runs a minimal erun-backend-api double covering every
// route `erun platform` drives, so real-run scenarios exercise
// erun-common/platform_client.go's request/response handling end to end
// rather than only its --dry-run trace branch.
func platformAPIStubServer(t testing.TB) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/whoami", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tenantId": "tenant-1", "userId": "user-1", "username": "test-user", "issuer": "https://idp.example", "subject": "sub-1",
		})
	})
	mux.HandleFunc("POST /v1/tenants", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tenantId": "tenant-2", "name": body["name"], "type": body["type"], "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
		})
	})
	mux.HandleFunc("GET /v1/tenants", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"tenantId": "tenant-1", "name": "acme", "type": "COMPANY", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z"},
		})
	})
	mux.HandleFunc("POST /v1/users", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"userId": "user-2", "tenantId": "tenant-1", "username": body["username"], "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
		})
	})
	mux.HandleFunc("GET /v1/users", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"userId": "user-1", "tenantId": "tenant-1", "username": "test-user", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z"},
		})
	})
	mux.HandleFunc("GET /v1/environments", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"environmentId": "env-1", "tenantId": "tenant-1", "name": "prod", "type": "runtime", "status": "running", "runtimeVersion": "1.2.3", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z"},
		})
	})
	mux.HandleFunc("POST /v1/environments", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"environmentId": "env-2", "tenantId": "tenant-1", "name": body["name"], "type": body["type"], "status": "registered", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
		})
	})
	mux.HandleFunc("GET /v1/environments/{environment_id}", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		if r.PathValue("environment_id") == "missing" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"environmentId": r.PathValue("environment_id"), "tenantId": "tenant-1", "name": "prod", "type": "runtime", "status": "running", "runtimeVersion": "1.2.3", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
		})
	})
	mux.HandleFunc("POST /v1/environments/{environment_id}/deploy", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"environmentId": r.PathValue("environment_id"), "tenantId": "tenant-1", "name": "prod", "type": "runtime", "status": "provisioning", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
		})
	})
	mux.HandleFunc("POST /v1/environments/{environment_id}/stop", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"environmentId": r.PathValue("environment_id"), "tenantId": "tenant-1", "name": "prod", "type": "runtime", "status": "running", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
		})
	})
	mux.HandleFunc("DELETE /v1/environments/{environment_id}", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/contexts", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"contextId": "ctx-1", "tenantId": "tenant-1", "name": "prod-cluster", "provider": "aws", "status": "running", "region": "eu-west-2", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z"},
		})
	})
	mux.HandleFunc("GET /v1/contexts/{context_id}", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"contextId": r.PathValue("context_id"), "tenantId": "tenant-1", "name": "prod-cluster", "provider": "aws", "status": "running", "region": "eu-west-2", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
		})
	})
	mux.HandleFunc("POST /v1/contexts", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if preview, _ := body["preview"].(bool); preview {
			_ = json.NewEncoder(w).Encode(map[string]any{"plan": []string{"context: bootstrap cluster prod via alias aws-main in eu-west-2"}})
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"plan": []string{"context: bootstrap cluster prod via alias aws-main in eu-west-2"},
			"context": map[string]any{
				"contextId": "ctx-2", "tenantId": "tenant-1", "name": body["name"], "provider": "aws", "status": "provisioning", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
			},
		})
	})
	mux.HandleFunc("POST /v1/provision", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"plan": []string{
				"provision: tenant acme (resolved from token)",
				"quota: tenant has 1 of 10 environments — within quota",
				"namespace: would create acme-prod",
			},
			"quotaOk": true,
		})
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestPlatform(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"platform", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "platform/help", normalize.Apply(result.Combined))
	})

	t.Run("whoami_no_alias_configured", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"platform", "whoami"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit with no erun alias configured, got:\n%s", result.Combined)
		}
		golden.Equal(t, "platform/whoami_no_alias_configured", normalize.Apply(result.Combined))
	})

	t.Run("whoami_dry_run_traces_resolved_call", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{"platform", "whoami", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "platform/whoami_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("whoami_real_run", func(t *testing.T) {
		setup := env.New(t)
		server := platformAPIStubServer(t)
		platformAlias(t, setup, server)
		result := erun.Run(t, []string{"platform", "whoami"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "test-user") || !strings.Contains(result.Combined, "tenant-1") {
			t.Fatalf("expected whoami output to name the resolved identity, got:\n%s", result.Combined)
		}
	})

	t.Run("tenant_create_dry_run", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		args := []string{"platform", "tenant", "create", "--name", "acme", "--issuer", "https://idp.example", "--dry-run"}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "platform/tenant_create_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("tenant_create_and_list_real_run", func(t *testing.T) {
		setup := env.New(t)
		server := platformAPIStubServer(t)
		platformAlias(t, setup, server)
		createResult := erun.Run(t, []string{"platform", "tenant", "create", "--name", "acme", "--issuer", "https://idp.example"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if createResult.ExitCode != 0 {
			t.Fatalf("create exit %d: %s", createResult.ExitCode, createResult.Combined)
		}
		if !strings.Contains(createResult.Combined, "acme") {
			t.Fatalf("expected created tenant to be named, got:\n%s", createResult.Combined)
		}
		listResult := erun.Run(t, []string{"platform", "tenant", "list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if listResult.ExitCode != 0 {
			t.Fatalf("list exit %d: %s", listResult.ExitCode, listResult.Combined)
		}
		if !strings.Contains(listResult.Combined, "acme") {
			t.Fatalf("expected tenant list to include acme, got:\n%s", listResult.Combined)
		}
	})

	t.Run("user_enroll_dry_run", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		args := []string{"platform", "user", "enroll", "--username", "jane", "--dry-run"}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "platform/user_enroll_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("user_enroll_and_list_real_run", func(t *testing.T) {
		setup := env.New(t)
		server := platformAPIStubServer(t)
		platformAlias(t, setup, server)
		enrollResult := erun.Run(t, []string{"platform", "user", "enroll", "--username", "jane"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if enrollResult.ExitCode != 0 {
			t.Fatalf("enroll exit %d: %s", enrollResult.ExitCode, enrollResult.Combined)
		}
		listResult := erun.Run(t, []string{"platform", "user", "list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if listResult.ExitCode != 0 {
			t.Fatalf("list exit %d: %s", listResult.ExitCode, listResult.Combined)
		}
		if !strings.Contains(listResult.Combined, "test-user") {
			t.Fatalf("expected user list to include test-user, got:\n%s", listResult.Combined)
		}
	})

	t.Run("env_register_dry_run", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		args := []string{"platform", "env", "register", "--name", "prod", "--type", "runtime", "--runtime-version", "1.2.3", "--dry-run"}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "platform/env_register_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("env_lifecycle_real_run", func(t *testing.T) {
		// One scenario drives register -> list -> get -> deploy -> stop ->
		// delete against the real stub server, covering every environment
		// PlatformClient method's request/response handling in one pass.
		setup := env.New(t)
		server := platformAPIStubServer(t)
		platformAlias(t, setup, server)

		register := erun.Run(t, []string{"platform", "env", "register", "--name", "prod", "--type", "runtime"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if register.ExitCode != 0 {
			t.Fatalf("register exit %d: %s", register.ExitCode, register.Combined)
		}

		list := erun.Run(t, []string{"platform", "env", "list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if list.ExitCode != 0 || !strings.Contains(list.Combined, "prod") {
			t.Fatalf("list exit %d: %s", list.ExitCode, list.Combined)
		}

		get := erun.Run(t, []string{"platform", "env", "get", "env-1"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if get.ExitCode != 0 || !strings.Contains(get.Combined, "env-1") {
			t.Fatalf("get exit %d: %s", get.ExitCode, get.Combined)
		}

		deploy := erun.Run(t, []string{"platform", "env", "deploy", "env-1", "--version", "1.3.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if deploy.ExitCode != 0 || !strings.Contains(deploy.Combined, "provisioning") {
			t.Fatalf("deploy exit %d: %s", deploy.ExitCode, deploy.Combined)
		}

		stop := erun.Run(t, []string{"platform", "env", "stop", "env-1"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if stop.ExitCode != 0 {
			t.Fatalf("stop exit %d: %s", stop.ExitCode, stop.Combined)
		}

		deleteResult := erun.Run(t, []string{"platform", "env", "delete", "env-1", "-y"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if deleteResult.ExitCode != 0 || !strings.Contains(deleteResult.Combined, "deleted environment env-1") {
			t.Fatalf("delete exit %d: %s", deleteResult.ExitCode, deleteResult.Combined)
		}
	})

	t.Run("env_get_not_found", func(t *testing.T) {
		setup := env.New(t)
		server := platformAPIStubServer(t)
		platformAlias(t, setup, server)
		result := erun.Run(t, []string{"platform", "env", "get", "missing"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for a missing environment, got:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "not found") {
			t.Fatalf("expected a not-found error, got:\n%s", result.Combined)
		}
	})

	t.Run("env_delete_dry_run_skips_confirmation", func(t *testing.T) {
		// --dry-run must never block on the interactive confirm prompt: the
		// harness has no TTY to answer it.
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{"platform", "env", "delete", "env-1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "platform/env_delete_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("context_create_preview_real_run", func(t *testing.T) {
		setup := env.New(t)
		server := platformAPIStubServer(t)
		platformAlias(t, setup, server)
		args := []string{"platform", "context", "create", "--name", "prod", "--alias", "aws-main", "--region", "eu-west-2", "--preview"}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "bootstrap cluster prod") {
			t.Fatalf("expected the preview plan in output, got:\n%s", result.Combined)
		}
	})

	t.Run("context_list_and_get_real_run", func(t *testing.T) {
		setup := env.New(t)
		server := platformAPIStubServer(t)
		platformAlias(t, setup, server)
		list := erun.Run(t, []string{"platform", "context", "list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if list.ExitCode != 0 || !strings.Contains(list.Combined, "prod-cluster") {
			t.Fatalf("list exit %d: %s", list.ExitCode, list.Combined)
		}
		get := erun.Run(t, []string{"platform", "context", "get", "ctx-1"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if get.ExitCode != 0 || !strings.Contains(get.Combined, "ctx-1") {
			t.Fatalf("get exit %d: %s", get.ExitCode, get.Combined)
		}
	})

	t.Run("provision_dry_run", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		args := []string{"platform", "provision", "--env-name", "prod", "--env-type", "runtime", "--kubernetes-context", "prod-cluster", "--dry-run"}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "platform/provision_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("provision_with_context_bootstrap_real_run", func(t *testing.T) {
		setup := env.New(t)
		server := platformAPIStubServer(t)
		platformAlias(t, setup, server)
		args := []string{
			"platform", "provision", "--env-name", "prod", "--env-type", "runtime",
			"--context-name", "prod", "--context-alias", "aws-main", "--context-region", "eu-west-2",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "quota ok: true") {
			t.Fatalf("expected the resolved plan and quota line, got:\n%s", result.Combined)
		}
	})

	t.Run("output_json", func(t *testing.T) {
		setup := env.New(t)
		server := platformAPIStubServer(t)
		platformAlias(t, setup, server)
		result := erun.Run(t, []string{"platform", "whoami", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, `"tenantId": "tenant-1"`) {
			t.Fatalf("expected structured JSON result on stdout, got:\n%s", result.Combined)
		}
	})
}
