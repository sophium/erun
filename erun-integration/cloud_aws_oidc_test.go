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

func testCloudOidcDryRunTracesBearerTokenCommand(t *testing.T) {
	setup := env.New(t)
	seedCloudProviderAlias(t, setup, "rihards+123456789012@aws", "test-profile")
	args := []string{"cloud", "oidc", "--alias", "rihards+123456789012@aws", "--audience", "https://api.example", "--dry-run"}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/oidc_dry_run_traces_bearer_token_command", normalize.Apply(result.Combined))
}

func testCloudOidcDryRunUnaffectedByLibraryExecutionMode(t *testing.T) {
	// Library mode must not change the dry-run trace: both paths return before calling AWS.
	setup := env.New(t)
	seedCloudProviderAlias(t, setup, "rihards+123456789012@aws", "test-profile")
	seedExecutionMode(t, setup, "aws-sts-web-identity-token", "library")
	args := []string{"cloud", "oidc", "--alias", "rihards+123456789012@aws", "--audience", "https://api.example", "--dry-run"}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/oidc_dry_run_traces_bearer_token_command", normalize.Apply(result.Combined))
}

func testCloudOidcRealRunLibraryExecutionModeNeedsNoAwsBinary(t *testing.T) {
	// No "aws" stub is declared: a subprocess fallback would fail on the PATH scrub.
	setup := env.New(t)
	seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
	seedExecutionMode(t, setup, "aws-sts", "library")
	seedExecutionMode(t, setup, "aws-sts-web-identity-token", "library")
	seedAWSStaticCredentialProfile(t, setup, "test-profile")
	issuer := "https://oidc.eu-west-2.amazonaws.com/test-issuer"
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"` + issuer + `"}`))
	jwt := header + "." + payload + ".sig"
	envVars := append(setup.Env(), "AWS_ENDPOINT_URL="+awsSTSActionStubServer(t, jwt).URL)
	result := erun.Run(t, []string{"cloud", "oidc", "--alias", "test-user@aws"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/oidc_real_run_library_execution_mode_needs_no_aws_binary", normalize.Apply(result.Combined))
	raw, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
	if err != nil {
		t.Fatalf("read root config: %v", err)
	}
	if !strings.Contains(string(raw), "oidcissuerurl: "+issuer) {
		t.Errorf("expected persisted OIDC issuer %q, got:\n%s", issuer, raw)
	}
}

func testCloudOidcRealRunEnablesFederationAndPersistsIssuer(t *testing.T) {
	// A marker file makes the stub fail the first token call and succeed the retry.
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
}

func testCloudOidcRealRunToleratesAlreadyEnabledFederation(t *testing.T) {
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
}

func testCloudOidcRealRunTokenAccessDeniedFails(t *testing.T) {
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
}

func testCloudOidcRealRunExpiredSessionLogsInBeforeToken(t *testing.T) {
	// A marker file records the login so ordering can be asserted.
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
}

func testCloudOidcRealRunNonJwtTokenFails(t *testing.T) {
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
}

func testCloudOidcRealRunEnableFederationFailureFails(t *testing.T) {
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
}

// awsSTSActionStubServer dispatches on the Action form field to serve both
// GetCallerIdentity and GetWebIdentityToken from one endpoint.
func awsSTSActionStubServer(t testing.TB, webIdentityToken string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		switch r.PostForm.Get("Action") {
		case "GetCallerIdentity":
			_, _ = fmt.Fprint(w, `<?xml version="1.0"?>
<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <GetCallerIdentityResult>
    <Arn>arn:aws:iam::123456789012:user/test-user</Arn>
    <UserId>AIDAEXAMPLE</UserId>
    <Account>123456789012</Account>
  </GetCallerIdentityResult>
  <ResponseMetadata><RequestId>test-request-id</RequestId></ResponseMetadata>
</GetCallerIdentityResponse>`)
		case "GetWebIdentityToken":
			_, _ = fmt.Fprintf(w, `<?xml version="1.0"?>
<GetWebIdentityTokenResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <GetWebIdentityTokenResult>
    <WebIdentityToken>%s</WebIdentityToken>
    <Expiration>2026-01-01T00:00:00Z</Expiration>
  </GetWebIdentityTokenResult>
  <ResponseMetadata><RequestId>test-request-id</RequestId></ResponseMetadata>
</GetWebIdentityTokenResponse>`, webIdentityToken)
		default:
			http.Error(w, "unexpected STS action "+r.PostForm.Get("Action"), http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	return server
}
