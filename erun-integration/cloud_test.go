package integration

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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

func stubAWSCallerIdentityAndJWT(t testing.TB, setup env.Setup) (envVars []string, issuer string) {
	t.Helper()
	stubs := setup.Cwd + "/stubs"
	issuer = "https://oidc.eu-west-2.amazonaws.com/test-issuer"
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"` + issuer + `"}`))
	jwt := header + "." + payload + ".sig"
	identityJSON := `{"UserId":"AIDAEXAMPLE","Account":"123456789012","Arn":"arn:aws:iam::123456789012:user/test-user"}`
	script := strings.Join([]string{
		`case "$*" in`,
		`  *"sts get-caller-identity"*) printf '%s' '` + identityJSON + `' ;;`,
		`  *"sts get-web-identity-token"*) printf '%s' '` + jwt + `' ;;`,
		`  *) : ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	fixture.StubBinaryWithScript(t, stubs, "aws", script)
	envVars = append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
	return envVars, issuer
}

// erunPlatformStubServer runs a minimal hosted erun platform + OIDC provider
// for real-run `cloud init erun`/`cloud login` scenarios: the unauthenticated
// GET /v1/platform discovery endpoint, OIDC discovery, the device
// authorization grant, and the token endpoint's device_code grant. The
// device flow's interval is 1s so the real-run scenario stays fast.
func erunPlatformStubServer(t testing.TB) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var issuer string
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": issuer, "cliClientId": "cli-test-client"})
	})
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                        issuer,
			"authorization_endpoint":        issuer + "/authorize",
			"token_endpoint":                issuer + "/token",
			"device_authorization_endpoint": issuer + "/device",
		})
	})
	mux.HandleFunc("POST /device", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":               "test-device-code",
			"user_code":                 "TEST-CODE",
			"verification_uri":          issuer + "/device",
			"verification_uri_complete": issuer + "/device?user_code=TEST-CODE",
			"expires_in":                600,
			"interval":                  1,
		})
	})
	// The first device-token poll answers authorization_pending — the poll
	// loop's ordinary steady state — and only the second succeeds, so the
	// scenario exercises pollERunDeviceToken's retry branch for free instead
	// of only ever reaching the loop on its first iteration.
	deviceTokenPolls := 0
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("refresh_token") != "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "test-access-token",
				"expires_in":   3600,
			})
			return
		}
		deviceTokenPolls++
		if deviceTokenPolls == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "test-access-token",
			"refresh_token": "test-refresh-token",
			"expires_in":    3600,
		})
	})
	mux.HandleFunc("GET /v1/whoami", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-access-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"tenantId": "test-tenant-id",
			"userId":   "test-user-id",
			"username": "test-user",
		})
	})
	mux.HandleFunc("GET /v1/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-access-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tenant":       map[string]string{"tenantId": "test-tenant-id", "name": "test-tenant"},
			"environments": []any{map[string]string{"environmentId": "env-1", "name": "prod"}},
			"contexts":     []any{},
		})
	})
	server := httptest.NewServer(mux)
	issuer = server.URL
	t.Cleanup(server.Close)
	return server
}

func seedERunCloudProviderAlias(t testing.TB, setup env.Setup, alias, issuer, clientID string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	body := "cloudproviders:\n" +
		"  - alias: " + alias + "\n" +
		"    provider: erun\n" +
		"    oidcissuerurl: " + issuer + "\n" +
		"    erun:\n" +
		"      apiurl: " + issuer + "\n" +
		"      clientid: " + clientID + "\n"
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write erun config: %v", err)
	}
}

// eraseCachedERunAccessToken deletes the cached access token file for alias
// (ref "erun/access/<alias>", hashed the same way erun-common's file-backed
// CloudSecretStore names it) without touching the refresh token, forcing the
// next status check or bearer-token resolution through the refresh_token
// grant.
func eraseCachedERunAccessToken(t testing.TB, setup env.Setup, alias string) {
	t.Helper()
	sum := sha256.Sum256([]byte("erun/access/" + alias))
	path := filepath.Join(setup.ConfigHome, "erun", "cloud-secrets", hex.EncodeToString(sum[:])+".token")
	if err := os.Remove(path); err != nil {
		t.Fatalf("erase cached access token %s: %v", path, err)
	}
}

func TestCloud(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"cloud", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/help", normalize.Apply(result.Combined))
	})

	t.Run("list_empty", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"cloud", "list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "cloud/list_empty", normalize.Apply(result.Combined))
	})

	t.Run("init_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"cloud", "init", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/init_help", normalize.Apply(result.Combined))
	})

	t.Run("login_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"cloud", "login", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/login_help", normalize.Apply(result.Combined))
	})

	t.Run("oidc_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"cloud", "oidc", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/oidc_help", normalize.Apply(result.Combined))
	})

	t.Run("set_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"cloud", "set", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/set_help", normalize.Apply(result.Combined))
	})

	t.Run("init_aws_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"cloud", "init", "aws", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/init_aws_help", normalize.Apply(result.Combined))
	})

	t.Run("init_cloudflare_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"cloud", "init", "cloudflare", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/init_cloudflare_help", normalize.Apply(result.Combined))
	})

	t.Run("init_cloudflare_dry_run_redacts_token_and_traces_alias_write", func(t *testing.T) {
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
		// The literal token must never reach the trace; the command prints
		// "api-token=<redacted>" instead. Normalization does not mask this
		// value, so a substring guard on the raw capture is the only way to
		// lock the redaction contract (see erun-integration/AGENTS.md
		// § "Whole-output snapshots vs targeted substring assertions" case 1).
		if strings.Contains(result.Combined, "dummy-token") {
			t.Fatalf("expected the api token to be redacted, but found the literal value in output:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "api-token=<redacted>") {
			t.Fatalf("expected redacted api-token marker in output, got:\n%s", result.Combined)
		}
		golden.Equal(t, "cloud/init_cloudflare_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("init_cloudflare_dry_run_auto_resolves_account", func(t *testing.T) {
		// Without --account-id, the wizard's account step auto-resolves the
		// single available account non-interactively.
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
	})

	t.Run("init_cloudflare_dry_run_honors_api_base_url_seam", func(t *testing.T) {
		// The ERUN_CLOUDFLARE_API_BASE_URL seam lets an e2e run point the
		// subprocess at a mock Cloudflare API. The trailing slash on the
		// override is deliberate: the golden's single `/client/v4/...` (no
		// double slash) locks the base-URL trim.
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
	})

	t.Run("init_erun_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"cloud", "init", "erun", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/init_erun_help", normalize.Apply(result.Combined))
	})

	t.Run("init_erun_dry_run_traces_discovery_plan", func(t *testing.T) {
		// No server is started: a dry run must never depend on the platform
		// being reachable, unlike AWS/Cloudflare init whose identity/verify
		// calls always run for real regardless of --dry-run.
		setup := env.New(t)
		args := []string{"cloud", "init", "erun", "--api-url", "https://api.example.test", "--dry-run"}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/init_erun_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("init_erun_real_run_and_login_completes_device_flow", func(t *testing.T) {
		// A real hosted-platform + OIDC provider stub, reached over real
		// loopback HTTP (erun's design takes the API URL as an explicit
		// input, so no ERUN_*_BASE_URL seam or stub binary is needed): init
		// discovers the platform and persists the alias, then login runs the
		// device authorization grant end to end and persists the resulting
		// refresh token.
		setup := env.New(t)
		server := erunPlatformStubServer(t)

		initResult := erun.Run(t, []string{"cloud", "init", "erun", "--api-url", server.URL}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if initResult.ExitCode != 0 {
			t.Fatalf("init exit %d: %s", initResult.ExitCode, initResult.Combined)
		}
		alias := "erun+" + strings.TrimPrefix(server.URL, "http://") + "@erun"
		if !strings.Contains(initResult.Combined, "Saved cloud provider alias "+alias) {
			t.Fatalf("expected init to save alias %s, got:\n%s", alias, initResult.Combined)
		}

		loginResult := erun.Run(t, []string{"cloud", "login", "--alias", alias}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   setup.Env(),
			Stdin: "y\n",
		})
		if loginResult.ExitCode != 0 {
			t.Fatalf("login exit %d: %s", loginResult.ExitCode, loginResult.Combined)
		}
		if !strings.Contains(loginResult.Combined, alias+": active") {
			t.Fatalf("expected login to report active status, got:\n%s", loginResult.Combined)
		}
		// The end-to-end proof: login also calls GET /v1/whoami against the
		// real platform stub, so a token that merely decodes but doesn't
		// authenticate would fail here.
		if !strings.Contains(loginResult.Combined, "Signed in to "+alias+" as test-user (tenant test-tenant-id)") {
			t.Fatalf("expected login to confirm sign-in via whoami, got:\n%s", loginResult.Combined)
		}
		// Same proof, one step further: GET /v1/config also round-trips
		// against the real platform stub.
		if !strings.Contains(loginResult.Combined, "Tenant test-tenant: 1 environment(s), 0 cloud context(s)") {
			t.Fatalf("expected login to confirm config readback, got:\n%s", loginResult.Combined)
		}

		raw, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
		if err != nil {
			t.Fatalf("read root config: %v", err)
		}
		body := string(raw)
		for _, want := range []string{
			"alias: " + alias,
			"clientid: cli-test-client",
			"refreshtokenref: erun/refresh/" + alias,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("expected persisted config to contain %q, got:\n%s", want, body)
			}
		}

		// Erase only the cached access token (not the refresh token config
		// just persisted above) so the next `cloud login` is forced through
		// the refresh_token grant instead of finding an already-active
		// cache — exercising the token-refresh path end to end, including
		// the whoami/config re-verification with the freshly refreshed
		// token. No confirm prompt is expected: a successful refresh reports
		// "active" before the command ever reaches that prompt.
		eraseCachedERunAccessToken(t, setup, alias)
		refreshLoginResult := erun.Run(t, []string{"cloud", "login", "--alias", alias}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if refreshLoginResult.ExitCode != 0 {
			t.Fatalf("refresh login exit %d: %s", refreshLoginResult.ExitCode, refreshLoginResult.Combined)
		}
		if !strings.Contains(refreshLoginResult.Combined, alias+": active") {
			t.Fatalf("expected refreshed login to report active status, got:\n%s", refreshLoginResult.Combined)
		}
	})

	t.Run("login_erun_dry_run_traces_discovery_and_device_flow_plan", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "test+host@erun", "https://auth.example.test", "cli-test")
		result := erun.Run(t, []string{"cloud", "login", "--alias", "test+host@erun", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/login_erun_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("oidc_erun_alias_fails", func(t *testing.T) {
		// `cloud oidc` is AWS-only web-identity federation setup; an erun
		// alias's issuer is already set at `cloud init erun` and never uses
		// this path.
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "test+host@erun", "https://auth.example.test", "cli-test")
		result := erun.Run(t, []string{"cloud", "oidc", "--alias", "test+host@erun"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "cloud/oidc_erun_alias_fails", normalize.Apply(result.Combined))
	})

	// The device flow's token round-trip can succeed while the platform still
	// rejects the resulting bearer at the actual API (e.g. a backend-side
	// authorization mismatch, a not-yet-provisioned tenant, a conflicting
	// concurrent request, or an unconfigured executor) — platformStatusError's
	// mapping, distinct from the token endpoint's own error mapping
	// (oauthTokenError), which a passing token exchange never reaches here.
	for _, statusCase := range []struct {
		name   string
		status int
	}{
		{"unauthorized", http.StatusUnauthorized},
		{"forbidden", http.StatusForbidden},
		{"not_found", http.StatusNotFound},
		{"conflict", http.StatusConflict},
		{"not_implemented", http.StatusNotImplemented},
		// Unmapped status: platformStatusError's default branch, a generic
		// error carrying the server's message with no sentinel to match.
		{"internal_server_error", http.StatusInternalServerError},
	} {
		t.Run("login_erun_real_run_whoami_verification_maps_platform_error_"+statusCase.name, func(t *testing.T) {
			setup := env.New(t)
			mux := http.NewServeMux()
			var issuer string
			mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]string{"issuer": issuer, "cliClientId": "cli-test-client"})
			})
			mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]string{
					"issuer":                        issuer,
					"token_endpoint":                issuer + "/token",
					"device_authorization_endpoint": issuer + "/device",
				})
			})
			mux.HandleFunc("POST /device", func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"device_code": "dc", "user_code": "UC", "expires_in": 600, "interval": 1})
			})
			mux.HandleFunc("POST /token", func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "refresh_token": "rtok", "expires_in": 3600})
			})
			mux.HandleFunc("GET /v1/whoami", func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, http.StatusText(statusCase.status), statusCase.status)
			})
			server := httptest.NewServer(mux)
			t.Cleanup(server.Close)
			issuer = server.URL

			initResult := erun.Run(t, []string{"cloud", "init", "erun", "--api-url", server.URL}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
			if initResult.ExitCode != 0 {
				t.Fatalf("init exit %d: %s", initResult.ExitCode, initResult.Combined)
			}
			alias := "erun+" + strings.TrimPrefix(server.URL, "http://") + "@erun"

			loginResult := erun.Run(t, []string{"cloud", "login", "--alias", alias}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "y\n"})
			if loginResult.ExitCode == 0 {
				t.Fatalf("expected a non-zero exit when whoami verification fails, got 0:\n%s", loginResult.Combined)
			}
			wantStatus := fmt.Sprintf("%d", statusCase.status)
			if !strings.Contains(loginResult.Combined, "verify erun platform sign-in") || !strings.Contains(loginResult.Combined, wantStatus) {
				t.Fatalf("expected the platform's %s to surface, got:\n%s", wantStatus, loginResult.Combined)
			}
		})
	}

	t.Run("init_aws_dry_run_traces_sso_setup_and_oidc_persistence", func(t *testing.T) {
		setup := env.New(t)
		args := []string{
			"cloud", "init", "aws",
			"--account-id", "123456789012",
			"--role-name", "Admin",
			"--region", "eu-west-2",
			"--sso-start-url", "https://example.awsapps.com/start",
			"--sso-region", "eu-west-1",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "cloud/init_aws_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("init_aws_prompts_missing_region_via_stdin", func(t *testing.T) {
		// Provides every AWS init param except --region because the prompt
		// reader buffers ahead — one interactive prompt per subprocess.
		setup := env.New(t)
		args := []string{
			"cloud", "init", "aws",
			"--account-id", "123456789012",
			"--role-name", "Admin",
			"--sso-start-url", "https://example.awsapps.com/start",
			"--sso-region", "eu-west-1",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   setup.Env(),
			Stdin: "eu-west-2\n",
		})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/init_aws_prompts_missing_region_via_stdin", normalize.Apply(result.Combined))
	})

	t.Run("init_aws_real_run_persists_alias_and_issuer", func(t *testing.T) {
		setup := env.New(t)
		envVars, issuer := stubAWSCallerIdentityAndJWT(t, setup)
		args := []string{
			"cloud", "init", "aws",
			"--account-id", "123456789012",
			"--role-name", "Admin",
			"--region", "eu-west-2",
			"--sso-start-url", "https://example.awsapps.com/start",
			"--sso-region", "eu-west-1",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		raw, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
		if err != nil {
			t.Fatalf("read root config: %v", err)
		}
		body := string(raw)
		for _, want := range []string{
			"alias: test-user+123456789012@aws",
			"username: test-user",
			"accountid: \"123456789012\"",
			"ssostarturl: https://example.awsapps.com/start",
			"oidcissuerurl: " + issuer,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("expected persisted config to contain %q, got:\n%s", want, body)
			}
		}
	})

	t.Run("login_real_run_invokes_aws_sso_login_via_stub", func(t *testing.T) {
		setup := env.New(t)
		seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
		envVars, _ := stubAWSCallerIdentityAndJWT(t, setup)
		result := erun.Run(t, []string{"cloud", "login", "--alias", "test-user@aws"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/login_real_run_invokes_aws_sso_login_via_stub", normalize.Apply(result.Combined))
	})

	t.Run("login_dry_run_traces_aws_sso_login", func(t *testing.T) {
		setup := env.New(t)
		seedCloudProviderAlias(t, setup, "rihards+123456789012@aws", "test-profile")
		result := erun.Run(t, []string{"cloud", "login", "--alias", "rihards+123456789012@aws", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/login_dry_run_traces_aws_sso_login", normalize.Apply(result.Combined))
	})

	t.Run("oidc_dry_run_traces_bearer_token_command", func(t *testing.T) {
		setup := env.New(t)
		seedCloudProviderAlias(t, setup, "rihards+123456789012@aws", "test-profile")
		args := []string{"cloud", "oidc", "--alias", "rihards+123456789012@aws", "--audience", "https://api.example", "--dry-run"}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/oidc_dry_run_traces_bearer_token_command", normalize.Apply(result.Combined))
	})

	t.Run("login_select_prompt_resolves_active_alias", func(t *testing.T) {
		// Without --alias, `cloud login` shows a Select of configured aliases;
		// "\r" confirms the single highlighted entry (the run's one interactive
		// prompt — readline read-ahead). The stub's exit-0 get-caller-identity
		// classifies the token active, so no login round-trip runs.
		setup := env.New(t)
		seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
		envVars, _ := stubAWSCallerIdentityAndJWT(t, setup)
		result := erun.Run(t, []string{"cloud", "login"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   envVars,
			Stdin: "\r",
		})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/login_select_prompt_resolves_active_alias", normalize.Apply(result.Combined))
	})

	t.Run("login_expired_confirms_relogin_via_stub", func(t *testing.T) {
		// The stub's stderr message is the decision input that classifies the
		// session expired; "y" confirms the forced `aws sso login` re-login.
		setup := env.New(t)
		seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
		stubs := setup.Cwd + "/stubs"
		script := strings.Join([]string{
			`case "$*" in`,
			`  *"sts get-caller-identity"*)`,
			`    printf '%s\n' 'The SSO session associated with this profile has expired or is otherwise invalid.' >&2`,
			`    exit 255 ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
		fixture.StubBinaryWithScript(t, stubs, "aws", script)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		result := erun.Run(t, []string{"cloud", "login", "--alias", "test-user@aws"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   envVars,
			Stdin: "y\n",
		})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/login_expired_confirms_relogin_via_stub", normalize.Apply(result.Combined))
	})

	t.Run("login_not_configured_declines_relogin", func(t *testing.T) {
		// The stub's "could not be found" stderr classifies the provider
		// not_configured; answering "n" must decline without running
		// `aws sso login` (the stub's login arm fails loudly if it does).
		setup := env.New(t)
		seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
		stubs := setup.Cwd + "/stubs"
		script := strings.Join([]string{
			`case "$*" in`,
			`  *"sts get-caller-identity"*)`,
			`    printf '%s\n' 'The config profile (test-profile) could not be found' >&2`,
			`    exit 255 ;;`,
			`  *"sso login"*)`,
			`    printf '%s\n' 'sso login must not run when the user declines' >&2`,
			`    exit 254 ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
		fixture.StubBinaryWithScript(t, stubs, "aws", script)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		result := erun.Run(t, []string{"cloud", "login", "--alias", "test-user@aws"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   envVars,
			Stdin: "n\n",
		})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/login_not_configured_declines_relogin", normalize.Apply(result.Combined))
	})

	t.Run("oidc_real_run_enables_federation_and_persists_issuer", func(t *testing.T) {
		// Federation-disabled recovery: the first token call fails with
		// OutboundWebIdentityFederationDisabledException, the command enables
		// federation and retries. A marker file makes the stub stateful so it
		// fails then succeeds across the two token calls. Issuer persistence is
		// a side effect outside the captured streams, so it is read from disk.
		setup := env.New(t)
		seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
		stubs := setup.Cwd + "/stubs"
		issuer := "https://oidc.eu-west-2.amazonaws.com/test-issuer"
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"` + issuer + `"}`))
		jwt := header + "." + payload + ".sig"
		marker := filepath.Join(stubs, "federation-enabled")
		script := strings.Join([]string{
			`case "$*" in`,
			`  *"sts get-caller-identity"*) printf '%s' '{"UserId":"AIDAEXAMPLE","Account":"123456789012","Arn":"arn:aws:iam::123456789012:user/test-user"}' ;;`,
			`  *"iam enable-outbound-web-identity-federation"*)`,
			`    touch '` + marker + `'`,
			`    printf '%s\n' '` + issuer + `' ;;`,
			`  *"sts get-web-identity-token"*)`,
			`    if [ -f '` + marker + `' ]; then`,
			`      printf '%s' '` + jwt + `'`,
			`    else`,
			`      printf '%s\n' 'An error occurred (OutboundWebIdentityFederationDisabledException) when calling the GetWebIdentityToken operation' >&2`,
			`      exit 254`,
			`    fi ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
		fixture.StubBinaryWithScript(t, stubs, "aws", script)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		result := erun.Run(t, []string{"cloud", "oidc", "--alias", "test-user@aws"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/oidc_real_run_enables_federation_and_persists_issuer", normalize.Apply(result.Combined))
		raw, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
		if err != nil {
			t.Fatalf("read root config: %v", err)
		}
		if !strings.Contains(string(raw), "oidcissuerurl: "+issuer) {
			t.Errorf("expected persisted OIDC issuer %q, got:\n%s", issuer, raw)
		}
	})

	t.Run("oidc_real_run_tolerates_already_enabled_federation", func(t *testing.T) {
		// Already-enabled tolerance: if the enable call fails with
		// "FeatureEnabled / already enabled" — a race where another principal
		// enabled federation first — the command treats it as success and
		// retries the token.
		setup := env.New(t)
		seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
		stubs := setup.Cwd + "/stubs"
		issuer := "https://oidc.eu-west-2.amazonaws.com/test-issuer"
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"` + issuer + `"}`))
		jwt := header + "." + payload + ".sig"
		marker := filepath.Join(stubs, "federation-enabled")
		script := strings.Join([]string{
			`case "$*" in`,
			`  *"sts get-caller-identity"*) printf '%s' '{"UserId":"AIDAEXAMPLE","Account":"123456789012","Arn":"arn:aws:iam::123456789012:user/test-user"}' ;;`,
			`  *"iam enable-outbound-web-identity-federation"*)`,
			`    touch '` + marker + `'`,
			`    printf '%s\n' 'An error occurred (FeatureEnabled): Outbound web identity federation is already enabled' >&2`,
			`    exit 254 ;;`,
			`  *"sts get-web-identity-token"*)`,
			`    if [ -f '` + marker + `' ]; then`,
			`      printf '%s' '` + jwt + `'`,
			`    else`,
			`      printf '%s\n' 'An error occurred (OutboundWebIdentityFederationDisabledException) when calling the GetWebIdentityToken operation' >&2`,
			`      exit 254`,
			`    fi ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
		fixture.StubBinaryWithScript(t, stubs, "aws", script)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		result := erun.Run(t, []string{"cloud", "oidc", "--alias", "test-user@aws"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/oidc_real_run_tolerates_already_enabled_federation", normalize.Apply(result.Combined))
	})

	t.Run("set_real_run_same_alias_heals_managed_cloud", func(t *testing.T) {
		// `cloud set` with the alias the env already carries takes the
		// no-change path but must still backfill managedcloud=true for a
		// remote worktree that predates the flag (a side effect read from
		// disk, not the captured streams).
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		envCfgPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		body := "name: dev\n" +
			"repopath: /home/erun/git/team\n" +
			"kubernetescontext: test-context\n" +
			"containerregistry: registry.example/test\n" +
			"runtimeversion: 1.0.0\n" +
			"type: remote-agent\n" +
			"cloudprovideralias: team-cloud\n"
		if err := os.WriteFile(envCfgPath, []byte(body), 0o644); err != nil {
			t.Fatalf("rewrite env config with alias: %v", err)
		}
		result := erun.Run(t, []string{"cloud", "set", "team", "dev", "--alias", "team-cloud"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/set_real_run_same_alias_heals_managed_cloud", normalize.Apply(result.Combined))
		raw, err := os.ReadFile(envCfgPath)
		if err != nil {
			t.Fatalf("read env config: %v", err)
		}
		if !strings.Contains(string(raw), "managedcloud: true") {
			t.Errorf("expected managedcloud backfilled on the remote env, got:\n%s", raw)
		}
	})

	t.Run("init_aws_real_run_identity_failure_fails", func(t *testing.T) {
		// Dry-run cannot reach this: the failure is the aws CLI's real exit
		// status. `sts get-caller-identity` fails, so init must exit non-zero.
		setup := env.New(t)
		stubs := setup.Cwd + "/stubs"
		script := strings.Join([]string{
			`case "$*" in`,
			`  *"sts get-caller-identity"*)`,
			`    printf '%s\n' 'An error occurred (AccessDenied) when calling the GetCallerIdentity operation' >&2`,
			`    exit 254 ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
		fixture.StubBinaryWithScript(t, stubs, "aws", script)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		args := []string{
			"cloud", "init", "aws",
			"--account-id", "123456789012",
			"--role-name", "Admin",
			"--region", "eu-west-2",
			"--sso-start-url", "https://example.awsapps.com/start",
			"--sso-region", "eu-west-1",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when identity resolution fails, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "cloud/init_aws_real_run_identity_failure_fails", normalize.Apply(result.Combined))
	})

	t.Run("init_aws_real_run_unresolvable_alias_fails", func(t *testing.T) {
		// An empty identity ({}) leaves no derivable alias; the command must
		// fail with "cloud provider alias cannot be resolved" rather than
		// persist a nameless provider.
		setup := env.New(t)
		stubs := setup.Cwd + "/stubs"
		script := strings.Join([]string{
			`case "$*" in`,
			`  *"sts get-caller-identity"*) printf '%s' '{}' ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
		fixture.StubBinaryWithScript(t, stubs, "aws", script)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		args := []string{
			"cloud", "init", "aws",
			"--account-id", "123456789012",
			"--role-name", "Admin",
			"--region", "eu-west-2",
			"--sso-start-url", "https://example.awsapps.com/start",
			"--sso-region", "eu-west-1",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when no alias can be derived, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "cloud/init_aws_real_run_unresolvable_alias_fails", normalize.Apply(result.Combined))
	})

	t.Run("init_aws_real_run_root_arn_username_fallback", func(t *testing.T) {
		// A root-account ARN has no "/" segment, so the username falls back to
		// the last ":" segment ("root"); the persisted alias must reflect it.
		setup := env.New(t)
		stubs := setup.Cwd + "/stubs"
		identityJSON := `{"UserId":"123456789012","Account":"123456789012","Arn":"arn:aws:iam::123456789012:root"}`
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"https://oidc.example/issuer"}`))
		script := strings.Join([]string{
			`case "$*" in`,
			`  *"sts get-caller-identity"*) printf '%s' '` + identityJSON + `' ;;`,
			`  *"sts get-web-identity-token"*) printf '%s' '` + header + "." + payload + ".sig" + `' ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
		fixture.StubBinaryWithScript(t, stubs, "aws", script)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		args := []string{
			"cloud", "init", "aws",
			"--account-id", "123456789012",
			"--role-name", "Admin",
			"--region", "eu-west-2",
			"--sso-start-url", "https://example.awsapps.com/start",
			"--sso-region", "eu-west-1",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		raw, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
		if err != nil {
			t.Fatalf("read root config: %v", err)
		}
		if !strings.Contains(string(raw), "alias: root+123456789012@aws") {
			t.Errorf("expected alias derived from the root ARN colon segment, got:\n%s", raw)
		}
	})

	t.Run("init_aws_real_run_configure_set_failure_fails", func(t *testing.T) {
		setup := env.New(t)
		stubs := setup.Cwd + "/stubs"
		script := strings.Join([]string{
			`case "$*" in`,
			`  *"configure set"*)`,
			`    printf '%s\n' 'Permission denied writing ~/.aws/config' >&2`,
			`    exit 1 ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
		fixture.StubBinaryWithScript(t, stubs, "aws", script)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		args := []string{
			"cloud", "init", "aws",
			"--account-id", "123456789012",
			"--role-name", "Admin",
			"--region", "eu-west-2",
			"--sso-start-url", "https://example.awsapps.com/start",
			"--sso-region", "eu-west-1",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when configure set fails, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "cloud/init_aws_real_run_configure_set_failure_fails", normalize.Apply(result.Combined))
	})

	t.Run("login_real_run_unsupported_provider_fails", func(t *testing.T) {
		// A provider stored as provider=gcp reports status unknown; confirming
		// the login must fail with "unsupported cloud provider" rather than
		// shell out to a CLI that does not exist.
		setup := env.New(t)
		root := filepath.Join(setup.ConfigHome, "erun")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", root, err)
		}
		body := "cloudproviders:\n" +
			"  - alias: test-user@gcp\n" +
			"    provider: gcp\n" +
			"    profile: test-profile\n"
		if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(body), 0o644); err != nil {
			t.Fatalf("write erun config: %v", err)
		}
		result := erun.Run(t, []string{"cloud", "login", "--alias", "test-user@gcp"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   setup.Env(),
			Stdin: "y\n",
		})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for unsupported provider, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "cloud/login_real_run_unsupported_provider_fails", normalize.Apply(result.Combined))
	})

	t.Run("login_real_run_sso_login_failure_fails", func(t *testing.T) {
		setup := env.New(t)
		seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
		stubs := setup.Cwd + "/stubs"
		script := strings.Join([]string{
			`case "$*" in`,
			`  *"sts get-caller-identity"*)`,
			`    printf '%s\n' 'The SSO session associated with this profile has expired or is otherwise invalid.' >&2`,
			`    exit 255 ;;`,
			`  *"sso login"*)`,
			`    printf '%s\n' 'SSO authorization page failed to open' >&2`,
			`    exit 1 ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
		fixture.StubBinaryWithScript(t, stubs, "aws", script)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		result := erun.Run(t, []string{"cloud", "login", "--alias", "test-user@aws"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   envVars,
			Stdin: "y\n",
		})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when sso login fails, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "cloud/login_real_run_sso_login_failure_fails", normalize.Apply(result.Combined))
	})

	t.Run("oidc_real_run_token_access_denied_fails", func(t *testing.T) {
		// AccessDenied on `sts get-web-identity-token` — not the
		// federation-disabled exception — must fail without attempting the
		// enable-federation recovery (the stub's enable arm guards against it).
		setup := env.New(t)
		seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
		stubs := setup.Cwd + "/stubs"
		script := strings.Join([]string{
			`case "$*" in`,
			`  *"sts get-caller-identity"*) printf '%s' '{"UserId":"AIDAEXAMPLE","Account":"123456789012","Arn":"arn:aws:iam::123456789012:user/test-user"}' ;;`,
			`  *"iam enable-outbound-web-identity-federation"*)`,
			`    printf '%s\n' 'enable-federation must not run for a non-federation error' >&2`,
			`    exit 254 ;;`,
			`  *"sts get-web-identity-token"*)`,
			`    printf '%s\n' 'An error occurred (AccessDenied) when calling the GetWebIdentityToken operation' >&2`,
			`    exit 254 ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
		fixture.StubBinaryWithScript(t, stubs, "aws", script)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		result := erun.Run(t, []string{"cloud", "oidc", "--alias", "test-user@aws"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when the bearer token call fails, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "cloud/oidc_real_run_token_access_denied_fails", normalize.Apply(result.Combined))
	})

	t.Run("oidc_real_run_expired_session_logs_in_before_token", func(t *testing.T) {
		// When the session is expired the flow must run `aws sso login` before
		// requesting the web identity token. A marker file records the login
		// (a side effect outside the captured streams) so the ordering can be
		// asserted.
		setup := env.New(t)
		seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
		stubs := setup.Cwd + "/stubs"
		issuer := "https://oidc.eu-west-2.amazonaws.com/test-issuer"
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"` + issuer + `"}`))
		jwt := header + "." + payload + ".sig"
		marker := filepath.Join(stubs, "sso-login-ran")
		script := strings.Join([]string{
			`case "$*" in`,
			`  *"sts get-caller-identity"*)`,
			`    printf '%s\n' 'The SSO session associated with this profile has expired or is otherwise invalid.' >&2`,
			`    exit 255 ;;`,
			`  *"sso login"*)`,
			`    touch '` + marker + `' ;;`,
			`  *"sts get-web-identity-token"*) printf '%s' '` + jwt + `' ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
		fixture.StubBinaryWithScript(t, stubs, "aws", script)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		result := erun.Run(t, []string{"cloud", "oidc", "--alias", "test-user@aws"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Errorf("expected `aws sso login` to run before the token request: %v", err)
		}
		golden.Equal(t, "cloud/oidc_real_run_expired_session_logs_in_before_token", normalize.Apply(result.Combined))
	})

	t.Run("oidc_real_run_non_jwt_token_fails", func(t *testing.T) {
		// A token without the three JWT segments must fail with "bearer token
		// is not a JWT" rather than persist a bogus issuer.
		setup := env.New(t)
		seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
		stubs := setup.Cwd + "/stubs"
		script := strings.Join([]string{
			`case "$*" in`,
			`  *"sts get-caller-identity"*) printf '%s' '{"UserId":"AIDAEXAMPLE","Account":"123456789012","Arn":"arn:aws:iam::123456789012:user/test-user"}' ;;`,
			`  *"sts get-web-identity-token"*) printf '%s' 'not-a-jwt' ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
		fixture.StubBinaryWithScript(t, stubs, "aws", script)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		result := erun.Run(t, []string{"cloud", "oidc", "--alias", "test-user@aws"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a non-JWT token, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "cloud/oidc_real_run_non_jwt_token_fails", normalize.Apply(result.Combined))
	})

	t.Run("oidc_real_run_enable_federation_failure_fails", func(t *testing.T) {
		// When the enable-federation call fails with a real error (not
		// "already enabled"), that failure must surface as the command's error
		// — contrast the already-enabled tolerance path.
		setup := env.New(t)
		seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
		stubs := setup.Cwd + "/stubs"
		script := strings.Join([]string{
			`case "$*" in`,
			`  *"sts get-caller-identity"*) printf '%s' '{"UserId":"AIDAEXAMPLE","Account":"123456789012","Arn":"arn:aws:iam::123456789012:user/test-user"}' ;;`,
			`  *"iam enable-outbound-web-identity-federation"*)`,
			`    printf '%s\n' 'An error occurred (AccessDenied) when calling the EnableOutboundWebIdentityFederation operation' >&2`,
			`    exit 254 ;;`,
			`  *"sts get-web-identity-token"*)`,
			`    printf '%s\n' 'An error occurred (OutboundWebIdentityFederationDisabledException) when calling the GetWebIdentityToken operation' >&2`,
			`    exit 254 ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
		fixture.StubBinaryWithScript(t, stubs, "aws", script)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		result := erun.Run(t, []string{"cloud", "oidc", "--alias", "test-user@aws"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when enabling federation fails, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "cloud/oidc_real_run_enable_federation_failure_fails", normalize.Apply(result.Combined))
	})

	t.Run("set_real_run_new_alias_marks_managed_cloud", func(t *testing.T) {
		// Assigning a new alias to a remote env must set managedcloud=true (a
		// remote worktree implies a managed cloud runtime) and persist both
		// fields. The config deliberately omits `name:` so the
		// environment-name backfill branch also runs.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		envCfgPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		body := "repopath: " + filepath.Join(setup.Home, "git", "team") + "\n" +
			"kubernetescontext: test-context\n" +
			"containerregistry: registry.example/test\n" +
			"runtimeversion: 1.0.0\n" +
			"type: remote-agent\n"
		if err := os.WriteFile(envCfgPath, []byte(body), 0o644); err != nil {
			t.Fatalf("rewrite env config without alias: %v", err)
		}
		result := erun.Run(t, []string{"cloud", "set", "team", "dev", "--alias", "team-cloud"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/set_real_run_new_alias_marks_managed_cloud", normalize.Apply(result.Combined))
		raw, err := os.ReadFile(envCfgPath)
		if err != nil {
			t.Fatalf("read env config: %v", err)
		}
		for _, want := range []string{"cloudprovideralias: team-cloud", "managedcloud: true", "name: dev"} {
			if !strings.Contains(string(raw), want) {
				t.Errorf("expected persisted env config to contain %q, got:\n%s", want, raw)
			}
		}
	})

	t.Run("set_missing_environment_fails", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"cloud", "set", "team", "ghost", "--alias", "team-cloud"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a missing environment, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "cloud/set_missing_environment_fails", normalize.Apply(result.Combined))
	})

	t.Run("set_empty_alias_fails", func(t *testing.T) {
		// --alias is cobra-required, but an explicitly empty value still passes
		// cobra's presence check and must be rejected by the params validation.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"cloud", "set", "team", "dev", "--alias", ""}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for an empty alias, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "cloud/set_empty_alias_fails", normalize.Apply(result.Combined))
	})

	t.Run("refresh_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"cloud", "refresh", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/refresh_help", normalize.Apply(result.Combined))
	})

	t.Run("refresh_dry_run_traces_pod_write_and_resolved_region", func(t *testing.T) {
		// The plan must show the region it resolved (from the alias' SSO region,
		// this env having no cloud context), the deployment wait, and the exec
		// that rewrites the erun-host profile — with the credentials arriving on
		// the pod's stdin, so the script body in the trace carries no secret.
		setup := env.New(t)
		fixture.SeedLocalTenantEnvWithAWSAlias(t, setup, "team", "dev", "ops+123456789012@aws", "eu-west-2", "")
		result := erun.Run(t, []string{"cloud", "refresh", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/refresh_dry_run_traces_pod_write_and_resolved_region", normalize.Apply(result.Combined))
	})

	t.Run("refresh_dry_run_reports_unresolved_region", func(t *testing.T) {
		// The alias records no Identity Center region and the registry is not an
		// ECR host, so no region resolves. The plan has to say so rather than
		// quietly plan a profile with no region in it.
		setup := env.New(t)
		fixture.SeedLocalTenantEnvWithAWSAlias(t, setup, "team", "dev", "ops+123456789012@aws", "", "")
		result := erun.Run(t, []string{"cloud", "refresh", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/refresh_dry_run_reports_unresolved_region", normalize.Apply(result.Combined))
	})

	t.Run("refresh_cloudflare_alias_fails", func(t *testing.T) {
		// Host credential injection is an AWS-only mechanism; a Cloudflare alias
		// ships its token as a chart Secret at deploy time instead, so refresh
		// must reject it rather than try to export credentials from it.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		root := filepath.Join(setup.ConfigHome, "erun")
		rootBody := "defaulttenant: team\n" +
			"cloudproviders:\n" +
			"  - alias: dns-edit+0a1b2c3d@cloudflare\n" +
			"    provider: cloudflare\n" +
			"    cloudflare:\n" +
			"      accountid: 0a1b2c3d\n" +
			"      tokenname: dns-edit\n" +
			"      tokenref: erun-secret://dns-edit\n"
		if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(rootBody), 0o644); err != nil {
			t.Fatalf("write erun config: %v", err)
		}
		envCfgPath := filepath.Join(root, "team", "dev", "config.yaml")
		envBody := "name: dev\nrepopath: " + setup.Cwd + "\nkubernetescontext: test-context\n" +
			"containerregistry: registry.example/test\nruntimeversion: 1.0.0\ntype: local-agent\n" +
			"cloudprovideralias: dns-edit+0a1b2c3d@cloudflare\n"
		if err := os.WriteFile(envCfgPath, []byte(envBody), 0o644); err != nil {
			t.Fatalf("write env config: %v", err)
		}
		result := erun.Run(t, []string{"cloud", "refresh", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a Cloudflare alias, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "cloud/refresh_cloudflare_alias_fails", normalize.Apply(result.Combined))
	})

	t.Run("refresh_without_aws_alias_fails", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"cloud", "refresh", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for an env with no AWS alias, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "cloud/refresh_without_aws_alias_fails", normalize.Apply(result.Combined))
	})

	t.Run("refresh_real_run_keeps_credentials_out_of_output", func(t *testing.T) {
		// The contract this scenario exists for: nothing secret passes through
		// the caller. The aws stub hands back credential values that would be
		// unmistakable in any trace line, and the kubectl stub records the exec
		// argv it was given. Neither the captured streams nor the recorded argv
		// may contain them — the profile reaches the pod on stdin only.
		setup := env.New(t)
		fixture.SeedLocalTenantEnvWithAWSAlias(t, setup, "team", "dev", "ops+123456789012@aws", "eu-west-2", "")
		stubs := filepath.Join(setup.Cwd, "stubs")
		const secret = "SECRETVALUEMUSTNOTLEAK"
		exported := `{"Version":1,"AccessKeyId":"ASIAEXAMPLE","SecretAccessKey":"` + secret +
			`","SessionToken":"TOKENVALUEMUSTNOTLEAK","Expiration":"2126-01-02T03:04:05Z"}`
		fixture.StubBinaryWithScript(t, stubs, "aws", strings.Join([]string{
			`case "$*" in`,
			`  *"configure export-credentials"*) printf '%s' '` + exported + `' ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n"))
		argvLog := filepath.Join(stubs, "kubectl-argv.log")
		fixture.StubBinaryWithScript(t, stubs, "kubectl", strings.Join([]string{
			`printf '%s\n' "$*" >> '` + argvLog + `'`,
			`cat >/dev/null 2>&1 || true`,
			`exit 0`,
		}, "\n"))
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws", "kubectl")...)
		result := erun.Run(t, []string{"cloud", "refresh", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		// Normalization does not mask these values, so a substring guard on the
		// raw capture is the only way to lock the contract (see
		// erun-integration/AGENTS.md § "Whole-output snapshots vs targeted
		// substring assertions" cases 1 and 2).
		for _, leak := range []string{secret, "TOKENVALUEMUSTNOTLEAK", "ASIAEXAMPLE"} {
			if strings.Contains(result.Combined, leak) {
				t.Fatalf("credential value %q leaked into command output:\n%s", leak, result.Combined)
			}
		}
		recorded, err := os.ReadFile(argvLog)
		if err != nil {
			t.Fatalf("read recorded kubectl argv: %v", err)
		}
		for _, leak := range []string{secret, "TOKENVALUEMUSTNOTLEAK", "ASIAEXAMPLE"} {
			if strings.Contains(string(recorded), leak) {
				t.Fatalf("credential value %q leaked into a kubectl argument:\n%s", leak, recorded)
			}
		}
		golden.Equal(t, "cloud/refresh_real_run_keeps_credentials_out_of_output", normalize.Apply(result.Combined))
	})

	t.Run("set_dry_run_traces_env_alias_write", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"cloud", "set", "team", "dev", "--alias", "team-cloud", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/set_dry_run_traces_env_alias_write", normalize.Apply(result.Combined))
	})
}

func seedCloudProviderAlias(t testing.TB, setup env.Setup, alias, profile string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	body := "cloudproviders:\n" +
		"  - alias: " + alias + "\n" +
		"    provider: aws\n" +
		"    profile: " + profile + "\n"
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write erun config: %v", err)
	}
}
