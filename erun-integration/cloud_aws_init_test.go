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

func testCloudInitAwsDryRunTracesSsoSetupAndOidcPersistence(t *testing.T) {
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
}

func testCloudInitAwsPromptsMissingRegionViaStdin(t *testing.T) {
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
}

func testCloudInitAwsRealRunPersistsAliasAndIssuer(t *testing.T) {
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
}

func testCloudInitAwsDryRunUnaffectedByLibraryExecutionMode(t *testing.T) {
	// Locks the dry-run/audit contract: the `aws sts get-caller-identity`
	// trace line the CLI hand-renders under --dry-run must stay
	// byte-identical to the subprocess-mode golden even when
	// execution.modes.aws-sts opts into the library path, because
	// ResolveAWSIdentity/CheckAWSStatus are never invoked during this
	// command's dry-run path at all (see init_aws_dry_run.txt).
	setup := env.New(t)
	seedExecutionMode(t, setup, "aws-sts", "library")
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
}

func testCloudInitAwsRealRunIdentityFailureFails(t *testing.T) {
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
}

func testCloudInitAwsRealRunUnresolvableAliasFails(t *testing.T) {
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
}

func testCloudInitAwsRealRunRootArnUsernameFallback(t *testing.T) {
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
}

func testCloudInitAwsRealRunConfigureSetFailureFails(t *testing.T) {
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
}

// stsCallerIdentityStubServer fakes the STS GetCallerIdentity endpoint the
// aws-sdk-go-v2 library path calls, honored via the SDK's own
// AWS_ENDPOINT_URL env var — no daemon, no real AWS account, no "aws" stub
// binary needed on PATH.
func stsCallerIdentityStubServer(t testing.TB) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		_, _ = fmt.Fprint(w, `<?xml version="1.0"?>
<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <GetCallerIdentityResult>
    <Arn>arn:aws:iam::123456789012:user/test-user</Arn>
    <UserId>AIDAEXAMPLE</UserId>
    <Account>123456789012</Account>
  </GetCallerIdentityResult>
  <ResponseMetadata><RequestId>test-request-id</RequestId></ResponseMetadata>
</GetCallerIdentityResponse>`)
	}))
	t.Cleanup(server.Close)
	return server
}
