package integration

import (
	"crypto/sha256"
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
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestCloud(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"help", testCloudHelp},
		{"list_empty", testCloudListEmpty},
		{"init_help", testCloudInitHelp},
		{"login_help", testCloudLoginHelp},
		{"oidc_help", testCloudOidcHelp},
		{"set_help", testCloudSetHelp},
		{"init_aws_help", testCloudInitAwsHelp},
		{"init_cloudflare_help", testCloudInitCloudflareHelp},
		{"init_cloudflare_dry_run_redacts_token_and_traces_alias_write", testCloudInitCloudflareDryRunRedactsTokenAndTracesAliasWrite},
		{"init_cloudflare_dry_run_auto_resolves_account", testCloudInitCloudflareDryRunAutoResolvesAccount},
		{"init_cloudflare_dry_run_honors_api_base_url_seam", testCloudInitCloudflareDryRunHonorsApiBaseUrlSeam},
		{"init_cloudflare_real_run_verifies_token_and_resolves_account_via_accounts_api", testCloudInitCloudflareRealRunVerifiesTokenAndResolvesAccountViaAccountsApi},
		{"init_cloudflare_real_run_resolves_account_via_zones_fallback", testCloudInitCloudflareRealRunResolvesAccountViaZonesFallback},
		{"init_erun_help", testCloudInitErunHelp},
		{"init_erun_dry_run_traces_discovery_plan", testCloudInitErunDryRunTracesDiscoveryPlan},
		{"init_erun_real_run_and_login_completes_device_flow", testCloudInitErunRealRunAndLoginCompletesDeviceFlow},
		{"login_erun_dry_run_traces_discovery_and_device_flow_plan", testCloudLoginErunDryRunTracesDiscoveryAndDeviceFlowPlan},
		{"oidc_erun_alias_fails", testCloudOidcErunAliasFails},
	} {
		t.Run(scenario.name, scenario.run)
	}

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
			testCloudLoginErunWhoamiVerificationMapsPlatformError(t, statusCase.status)
		})
	}

	for _, scenario := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"init_aws_dry_run_traces_sso_setup_and_oidc_persistence", testCloudInitAwsDryRunTracesSsoSetupAndOidcPersistence},
		{"init_aws_prompts_missing_region_via_stdin", testCloudInitAwsPromptsMissingRegionViaStdin},
		{"init_aws_real_run_persists_alias_and_issuer", testCloudInitAwsRealRunPersistsAliasAndIssuer},
		{"login_real_run_invokes_aws_sso_login_via_stub", testCloudLoginRealRunInvokesAwsSsoLoginViaStub},
		{"init_aws_dry_run_unaffected_by_library_execution_mode", testCloudInitAwsDryRunUnaffectedByLibraryExecutionMode},
		{"login_real_run_library_execution_mode_needs_no_aws_binary", testCloudLoginRealRunLibraryExecutionModeNeedsNoAwsBinary},
		{"login_dry_run_traces_aws_sso_login", testCloudLoginDryRunTracesAwsSsoLogin},
		{"oidc_dry_run_traces_bearer_token_command", testCloudOidcDryRunTracesBearerTokenCommand},
		{"oidc_dry_run_unaffected_by_library_execution_mode", testCloudOidcDryRunUnaffectedByLibraryExecutionMode},
		{"oidc_real_run_library_execution_mode_needs_no_aws_binary", testCloudOidcRealRunLibraryExecutionModeNeedsNoAwsBinary},
		{"login_select_prompt_resolves_active_alias", testCloudLoginSelectPromptResolvesActiveAlias},
		{"login_expired_confirms_relogin_via_stub", testCloudLoginExpiredConfirmsReloginViaStub},
		{"login_not_configured_declines_relogin", testCloudLoginNotConfiguredDeclinesRelogin},
		{"oidc_real_run_enables_federation_and_persists_issuer", testCloudOidcRealRunEnablesFederationAndPersistsIssuer},
		{"oidc_real_run_tolerates_already_enabled_federation", testCloudOidcRealRunToleratesAlreadyEnabledFederation},
		{"set_real_run_same_alias_heals_managed_cloud", testCloudSetRealRunSameAliasHealsManagedCloud},
		{"init_aws_real_run_identity_failure_fails", testCloudInitAwsRealRunIdentityFailureFails},
		{"init_aws_real_run_unresolvable_alias_fails", testCloudInitAwsRealRunUnresolvableAliasFails},
		{"init_aws_real_run_root_arn_username_fallback", testCloudInitAwsRealRunRootArnUsernameFallback},
		{"init_aws_real_run_configure_set_failure_fails", testCloudInitAwsRealRunConfigureSetFailureFails},
		{"login_real_run_unsupported_provider_fails", testCloudLoginRealRunUnsupportedProviderFails},
		{"login_real_run_sso_login_failure_fails", testCloudLoginRealRunSsoLoginFailureFails},
		{"oidc_real_run_token_access_denied_fails", testCloudOidcRealRunTokenAccessDeniedFails},
		{"oidc_real_run_expired_session_logs_in_before_token", testCloudOidcRealRunExpiredSessionLogsInBeforeToken},
		{"oidc_real_run_non_jwt_token_fails", testCloudOidcRealRunNonJwtTokenFails},
		{"oidc_real_run_enable_federation_failure_fails", testCloudOidcRealRunEnableFederationFailureFails},
		{"set_real_run_new_alias_marks_managed_cloud", testCloudSetRealRunNewAliasMarksManagedCloud},
		{"set_missing_environment_fails", testCloudSetMissingEnvironmentFails},
		{"set_empty_alias_fails", testCloudSetEmptyAliasFails},
		{"set_empty_tenant_fails", testCloudSetEmptyTenantFails},
		{"set_empty_environment_fails", testCloudSetEmptyEnvironmentFails},
		{"refresh_help", testCloudRefreshHelp},
		{"refresh_dry_run_traces_pod_write_and_resolved_region", testCloudRefreshDryRunTracesPodWriteAndResolvedRegion},
		{"refresh_dry_run_reports_unresolved_region", testCloudRefreshDryRunReportsUnresolvedRegion},
		{"refresh_cloudflare_alias_fails", testCloudRefreshCloudflareAliasFails},
		{"refresh_without_aws_alias_fails", testCloudRefreshWithoutAwsAliasFails},
		{"refresh_real_run_keeps_credentials_out_of_output", testCloudRefreshRealRunKeepsCredentialsOutOfOutput},
		{"refresh_dry_run_unaffected_by_library_execution_mode", testCloudRefreshDryRunUnaffectedByLibraryExecutionMode},
		{"refresh_real_run_library_execution_mode_needs_no_aws_binary", testCloudRefreshRealRunLibraryExecutionModeNeedsNoAwsBinary},
		{"set_dry_run_traces_env_alias_write", testCloudSetDryRunTracesEnvAliasWrite},
	} {
		t.Run(scenario.name, scenario.run)
	}
}

func testCloudHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"cloud", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/help", normalize.Apply(result.Combined))
}

func testCloudListEmpty(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"cloud", "list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "cloud/list_empty", normalize.Apply(result.Combined))
}

func testCloudInitHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"cloud", "init", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/init_help", normalize.Apply(result.Combined))
}

func testCloudLoginHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"cloud", "login", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/login_help", normalize.Apply(result.Combined))
}

func testCloudOidcHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"cloud", "oidc", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/oidc_help", normalize.Apply(result.Combined))
}

func testCloudSetHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"cloud", "set", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/set_help", normalize.Apply(result.Combined))
}

func testCloudInitAwsHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"cloud", "init", "aws", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/init_aws_help", normalize.Apply(result.Combined))
}

func testCloudInitCloudflareHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"cloud", "init", "cloudflare", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/init_cloudflare_help", normalize.Apply(result.Combined))
}

func testCloudInitErunHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"cloud", "init", "erun", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/init_erun_help", normalize.Apply(result.Combined))
}

func testCloudRefreshHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"cloud", "refresh", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/refresh_help", normalize.Apply(result.Combined))
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

// seedExecutionMode appends an execution.modes override to the root
// config.yaml, preserving whatever seedCloudProviderAlias or another fixture
// already wrote — config.yaml is plain YAML, so appending a distinct
// top-level key is enough, without needing to parse and re-merge the existing
// document. A second call (a scenario opting two operations into library mode
// at once) inserts its line into the existing "modes:" map instead of adding
// a second "execution:" top-level key, which yaml.Unmarshal rejects as a
// duplicate key.
func seedExecutionMode(t testing.TB, setup env.Setup, operation, mode string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	path := filepath.Join(root, "config.yaml")
	existing, _ := os.ReadFile(path)
	body := string(existing)
	line := "    " + operation + ": " + mode + "\n"
	const modesHeader = "execution:\n  modes:\n"
	if idx := strings.Index(body, modesHeader); idx >= 0 {
		insertAt := idx + len(modesHeader)
		body = body[:insertAt] + line + body[insertAt:]
	} else {
		body += modesHeader + line
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write erun config: %v", err)
	}
}

// seedAWSStaticCredentialProfile writes a real ~/.aws/config + credentials
// profile with static keys, standing in for a completed `aws configure sso`
// so the aws-sdk-go-v2 library path (which reads the real shared config files
// rather than trusting a stubbed CLI's exit code) has a profile to resolve.
func seedAWSStaticCredentialProfile(t testing.TB, setup env.Setup, profile string) {
	t.Helper()
	dir := filepath.Join(setup.Home, ".aws")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	config := "[profile " + profile + "]\nregion = eu-west-2\n"
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(config), 0o644); err != nil {
		t.Fatalf("write aws config: %v", err)
	}
	credentials := "[" + profile + "]\naws_access_key_id = AKIAFAKE\naws_secret_access_key = fakesecret\n"
	if err := os.WriteFile(filepath.Join(dir, "credentials"), []byte(credentials), 0o644); err != nil {
		t.Fatalf("write aws credentials: %v", err)
	}
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
	// Assign the issuer before Start: httptest.NewServer begins serving before
	// it returns, so writing server.URL afterwards has no happens-before edge
	// with the handler goroutines that read it. The listener address is fixed
	// when it is allocated, so this is the same URL NewServer would report.
	server := httptest.NewUnstartedServer(mux)
	issuer = "http://" + server.Listener.Addr().String()
	server.Start()
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

// seedTwoERunCloudProviderAliases writes two erun-type aliases in one
// config.yaml, for scenarios asserting the ambiguous-alias refusal
// (ResolveERunPlatformAlias refuses to guess between them without an
// explicit --erun-alias).
func seedTwoERunCloudProviderAliases(t testing.TB, setup env.Setup) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	body := "cloudproviders:\n" +
		"  - alias: erun+one@erun\n" +
		"    provider: erun\n" +
		"    oidcissuerurl: https://api.example.test\n" +
		"    erun:\n" +
		"      apiurl: https://api.example.test\n" +
		"      clientid: cli-test-client\n" +
		"  - alias: erun+two@erun\n" +
		"    provider: erun\n" +
		"    oidcissuerurl: https://api2.example.test\n" +
		"    erun:\n" +
		"      apiurl: https://api2.example.test\n" +
		"      clientid: cli-test-client\n"
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

func testCloudInitErunDryRunTracesDiscoveryPlan(t *testing.T) {
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
}

func testCloudInitErunRealRunAndLoginCompletesDeviceFlow(t *testing.T) {
	// A real hosted-platform + OIDC provider stub, reached over real
	// loopback HTTP (erun's design takes the API URL as an explicit
	// input, so no ERUN_*_BASE_URL seam or stub binary is needed): init
	// discovers the platform and persists the alias, then login runs the
	// device authorization grant end to end and persists the resulting
	// refresh token.
	setup := env.New(t)
	server := erunPlatformStubServer(t)

	alias := initERunAliasAndLogin(t, setup, server)
	assertERunAliasPersisted(t, setup, alias)

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

	// `erun list` must agree with `cloud login` about this alias —
	// before ListCloudProviderStatuses was given a real CloudDependencies,
	// this row read "expired" with "cloud secret store is not configured"
	// even though the platform login above is demonstrably working.
	listResult := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if listResult.ExitCode != 0 {
		t.Fatalf("list exit %d: %s", listResult.ExitCode, listResult.Combined)
	}
	if !strings.Contains(listResult.Combined, alias+" provider=erun account="+strings.TrimPrefix(server.URL, "http://")+" status=active") {
		t.Fatalf("expected erun list to report %s as active, got:\n%s", alias, listResult.Combined)
	}
}

// initERunAliasAndLogin initialises the platform alias and signs in through
// the device flow, returning the alias both report on.
func initERunAliasAndLogin(t *testing.T, setup env.Setup, server *httptest.Server) string {
	t.Helper()
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
	return alias
}

func assertERunAliasPersisted(t *testing.T, setup env.Setup, alias string) {
	t.Helper()
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
}

func testCloudLoginErunDryRunTracesDiscoveryAndDeviceFlowPlan(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "test+host@erun", "https://auth.example.test", "cli-test")
	result := erun.Run(t, []string{"cloud", "login", "--alias", "test+host@erun", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/login_erun_dry_run", normalize.Apply(result.Combined))
}

func testCloudOidcErunAliasFails(t *testing.T) {
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
}

// testCloudLoginErunWhoamiVerificationMapsPlatformError completes the device
// flow against a platform whose whoami answers status, which login must surface.
func testCloudLoginErunWhoamiVerificationMapsPlatformError(t *testing.T, status int) {
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
		http.Error(w, http.StatusText(status), status)
	})
	// Assigned before Start so it happens-before every handler
	// goroutine that reads it; see erunPlatformStubServer.
	server := httptest.NewUnstartedServer(mux)
	issuer = "http://" + server.Listener.Addr().String()
	server.Start()
	t.Cleanup(server.Close)

	initResult := erun.Run(t, []string{"cloud", "init", "erun", "--api-url", server.URL}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if initResult.ExitCode != 0 {
		t.Fatalf("init exit %d: %s", initResult.ExitCode, initResult.Combined)
	}
	alias := "erun+" + strings.TrimPrefix(server.URL, "http://") + "@erun"

	loginResult := erun.Run(t, []string{"cloud", "login", "--alias", alias}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "y\n"})
	if loginResult.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit when whoami verification fails, got 0:\n%s", loginResult.Combined)
	}
	wantStatus := fmt.Sprintf("%d", status)
	if !strings.Contains(loginResult.Combined, "verify erun platform sign-in") || !strings.Contains(loginResult.Combined, wantStatus) {
		t.Fatalf("expected the platform's %s to surface, got:\n%s", wantStatus, loginResult.Combined)
	}
}
