package integration

import (
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

func testCloudInitCloudflareDryRunRedactsTokenAndTracesAliasWrite(t *testing.T) {
	setup := env.New(t)
	args := []string{
		"cloud", "init", "cloudflare",
		"--account-id", "cf-acct-123",
		"--token-name", "ci-token",
		"--api-token", "dummy-token",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	// Normalization does not mask the token, so the redaction needs a raw substring guard.
	if strings.Contains(result.Combined, "dummy-token") {
		t.Fatalf("expected the api token to be redacted, but found the literal value in output:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "api-token=<redacted>") {
		t.Fatalf("expected redacted api-token marker in output, got:\n%s", result.Combined)
	}
	golden.Equal(t, "cloud/init_cloudflare_dry_run", normalize.Apply(result.Combined))
}

func testCloudInitCloudflareDryRunAutoResolvesAccount(t *testing.T) {
	setup := env.New(t)
	args := []string{
		"cloud", "init", "cloudflare",
		"--token-name", "ci-token",
		"--api-token", "dummy-token",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "dummy-token") {
		t.Fatalf("expected the api token to be redacted, but found the literal value in output:\n%s", result.Combined)
	}
	golden.Equal(t, "cloud/init_cloudflare_dry_run_auto_account", normalize.Apply(result.Combined))
}

func testCloudInitCloudflareDryRunHonorsApiBaseUrlSeam(t *testing.T) {
	// The trailing slash is deliberate: the golden locks the base-URL trim.
	setup := env.New(t)
	args := []string{
		"cloud", "init", "cloudflare",
		"--account-id", "cf-acct-123",
		"--token-name", "ci-token",
		"--api-token", "dummy-token",
		"--dry-run",
	}
	envVars := append(setup.Env(), "ERUN_CLOUDFLARE_API_BASE_URL=https://cf-mock.example/")
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/init_cloudflare_dry_run_api_base_url_seam", normalize.Apply(result.Combined))
}

func testCloudInitCloudflareRealRunVerifiesTokenAndResolvesAccountViaAccountsApi(t *testing.T) {
	setup := env.New(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /client/v4/user/tokens/verify", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer real-cf-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"result":  map[string]string{"id": "tok-1", "status": "active"},
		})
	})
	mux.HandleFunc("GET /client/v4/accounts", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"result":  []map[string]string{{"id": "cf-acct-real", "name": "Real Account"}},
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	envVars := append(setup.Env(), "ERUN_CLOUDFLARE_API_BASE_URL="+server.URL)
	result := erun.Run(t, []string{
		"cloud", "init", "cloudflare",
		"--token-name", "ci-token",
		"--api-token", "real-cf-token",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	alias := "ci-token+cf-acct-real@cloudflare"
	if !strings.Contains(result.Combined, "cloud init cloudflare: resolved account cf-acct-real") {
		t.Fatalf("expected the resolved account to be traced, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "Saved cloud provider alias "+alias) {
		t.Fatalf("expected init to save alias %s, got:\n%s", alias, result.Combined)
	}

	raw, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
	if err != nil {
		t.Fatalf("read root config: %v", err)
	}
	if !strings.Contains(string(raw), "alias: "+alias) {
		t.Errorf("expected persisted config to contain alias %s, got:\n%s", alias, raw)
	}
	if _, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "cloud-secrets")); err == nil {
		t.Errorf("cloud-secrets should be a directory, not a file")
	}

	listResult := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if listResult.ExitCode != 0 {
		t.Fatalf("list exit %d: %s", listResult.ExitCode, listResult.Combined)
	}
	if !strings.Contains(listResult.Combined, alias+" provider=cloudflare account=cf-acct-real status=active") {
		t.Fatalf("expected erun list to report %s as active, got:\n%s", alias, listResult.Combined)
	}
}

func testCloudInitCloudflareRealRunResolvesAccountViaZonesFallback(t *testing.T) {
	// A zone-only token gets an empty /accounts result, forcing the zone fallback.
	setup := env.New(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /client/v4/user/tokens/verify", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"result":  map[string]string{"id": "tok-2", "status": "active"},
		})
	})
	mux.HandleFunc("GET /client/v4/accounts", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []map[string]string{}})
	})
	mux.HandleFunc("GET /client/v4/zones", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"result": []map[string]any{
				{"account": map[string]string{"id": "cf-acct-via-zone", "name": "Zone Account"}},
				// A second zone under the same account must not duplicate it.
				{"account": map[string]string{"id": "cf-acct-via-zone", "name": "Zone Account"}},
			},
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	envVars := append(setup.Env(), "ERUN_CLOUDFLARE_API_BASE_URL="+server.URL)
	result := erun.Run(t, []string{
		"cloud", "init", "cloudflare",
		"--token-name", "zone-token",
		"--api-token", "zone-scoped-token",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	alias := "zone-token+cf-acct-via-zone@cloudflare"
	if !strings.Contains(result.Combined, "Saved cloud provider alias "+alias) {
		t.Fatalf("expected init to save alias %s resolved via the zones fallback, got:\n%s", alias, result.Combined)
	}
}
