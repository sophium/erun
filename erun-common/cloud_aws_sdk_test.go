package eruncommon

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stsCallerIdentityFixture starts a fake STS endpoint that answers
// GetCallerIdentity with a fixed identity, honored via the SDK's own
// AWS_ENDPOINT_URL env var (no daemon, no real AWS account needed).
func stsCallerIdentityFixture(t *testing.T) {
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
	t.Setenv("AWS_ENDPOINT_URL", server.URL)
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIAFAKE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "fakesecret")
	t.Setenv("AWS_REGION", "us-east-1")
}

func traceCapturingContext() (Context, *bytes.Buffer) {
	var trace bytes.Buffer
	logger := NewLogger(VerbosityInfo).WithTraceSink(&trace)
	return Context{Logger: logger}, &trace
}

// stsWebIdentityTokenFixture starts a fake STS endpoint that answers
// GetWebIdentityToken with a fixed token, honored via the SDK's own
// AWS_ENDPOINT_URL env var (no daemon, no real AWS account needed).
func stsWebIdentityTokenFixture(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		_, _ = fmt.Fprint(w, `<?xml version="1.0"?>
<GetWebIdentityTokenResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <GetWebIdentityTokenResult>
    <WebIdentityToken>header.payload.sig</WebIdentityToken>
    <Expiration>2026-01-01T00:00:00Z</Expiration>
  </GetWebIdentityTokenResult>
  <ResponseMetadata><RequestId>test-request-id</RequestId></ResponseMetadata>
</GetWebIdentityTokenResponse>`)
	}))
	t.Cleanup(server.Close)
	t.Setenv("AWS_ENDPOINT_URL", server.URL)
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIAFAKE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "fakesecret")
	t.Setenv("AWS_REGION", "us-east-1")
}

func TestLibraryResolveAWSIdentityMatchesSubprocessObservableResult(t *testing.T) {
	stsCallerIdentityFixture(t)
	ctx, trace := traceCapturingContext()

	identity, err := libraryResolveAWSIdentity(ctx, "")
	if err != nil {
		t.Fatalf("libraryResolveAWSIdentity: %v", err)
	}

	// This is exactly the shape defaultResolveAWSIdentity parses out of `aws
	// sts get-caller-identity --output json` stdout — the two paths must
	// agree on the observable result for the same underlying identity.
	want := AWSIdentity{Account: "123456789012", Arn: "arn:aws:iam::123456789012:user/test-user", UserID: "AIDAEXAMPLE"}
	if identity != want {
		t.Fatalf("identity = %+v, want %+v", identity, want)
	}

	// The trace line must be byte-identical to what defaultResolveAWSIdentity
	// renders for the subprocess path (root AGENTS.md's dry-run/audit
	// contract: the equivalent CLI invocation, regardless of execution mode).
	wantTrace := formatShellCommand("", "aws", awsGetCallerIdentityArgs("")...)
	if got := strings.TrimSpace(trace.String()); got != wantTrace {
		t.Fatalf("trace = %q, want %q", got, wantTrace)
	}
}

// TestAWSGetCallerIdentityArgsSharedByBothPaths locks the one argv builder
// defaultResolveAWSIdentity (subprocess) and traceAWSGetCallerIdentity
// (library) both call, so a profile can never render differently between
// execution modes.
func TestAWSGetCallerIdentityArgsSharedByBothPaths(t *testing.T) {
	got := awsGetCallerIdentityArgs("my-profile")
	want := []string{"sts", "get-caller-identity", "--output", "json", "--profile", "my-profile"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
}

func TestLibraryCheckAWSStatusActive(t *testing.T) {
	stsCallerIdentityFixture(t)
	provider := CloudProviderConfig{Provider: CloudProviderAWS, Alias: "test@aws"}

	status := libraryCheckAWSStatus(Context{}, provider)

	if status.Status != CloudTokenStatusActive {
		t.Fatalf("status = %+v, want Active", status)
	}
}

func TestLibraryCheckAWSStatusNotConfiguredForMissingProfile(t *testing.T) {
	provider := CloudProviderConfig{Provider: CloudProviderAWS, Alias: "test@aws", Profile: "erun-cloud-aws-sdk-test-missing-profile"}

	status := libraryCheckAWSStatus(Context{}, provider)

	if status.Status != CloudTokenStatusNotConfigured {
		t.Fatalf("status = %+v, want NotConfigured", status)
	}
	if status.Message == "" {
		t.Fatal("expected a non-empty status message")
	}
}

func TestLibraryRunAWSBearerTokenMatchesSubprocessObservableResult(t *testing.T) {
	stsWebIdentityTokenFixture(t)
	ctx, trace := traceCapturingContext()

	token, err := libraryRunAWSBearerToken(ctx, "", "https://api.example")
	if err != nil {
		t.Fatalf("libraryRunAWSBearerToken: %v", err)
	}

	// This is exactly what defaultRunAWSBearerToken parses out of `aws sts
	// get-web-identity-token ... --output text` stdout — the two paths must
	// agree on the observable result for the same underlying token.
	want := "header.payload.sig"
	if token != want {
		t.Fatalf("token = %q, want %q", token, want)
	}

	// The trace line must be byte-identical to what defaultRunAWSBearerToken
	// renders for the subprocess path (root AGENTS.md's dry-run/audit
	// contract: the equivalent CLI invocation, regardless of execution mode).
	wantTrace := formatShellCommand("", "aws", awsGetWebIdentityTokenArgs("", "https://api.example")...)
	if got := strings.TrimSpace(trace.String()); got != wantTrace {
		t.Fatalf("trace = %q, want %q", got, wantTrace)
	}
}

func TestLibraryRunAWSBearerTokenDryRunTracesWithoutCallingAWS(t *testing.T) {
	// No fixture set up: a dry run must never touch the network, so an
	// unstubbed AWS_ENDPOINT_URL would fail the test if it were reached.
	ctx, trace := traceCapturingContext()
	ctx.DryRun = true

	token, err := libraryRunAWSBearerToken(ctx, "my-profile", "https://api.example")
	if err != nil {
		t.Fatalf("libraryRunAWSBearerToken: %v", err)
	}
	if token != "" {
		t.Fatalf("token = %q, want empty on dry run", token)
	}

	wantTrace := formatShellCommand("", "aws", awsGetWebIdentityTokenArgs("my-profile", "https://api.example")...)
	if got := strings.TrimSpace(trace.String()); got != wantTrace {
		t.Fatalf("trace = %q, want %q", got, wantTrace)
	}
}

// TestAWSGetWebIdentityTokenArgsSharedByBothPaths locks the one argv builder
// defaultRunAWSBearerToken (subprocess) and traceAWSGetWebIdentityToken
// (library) both call, so a profile can never render differently between
// execution modes.
func TestAWSGetWebIdentityTokenArgsSharedByBothPaths(t *testing.T) {
	got := awsGetWebIdentityTokenArgs("my-profile", "https://api.example")
	want := []string{
		"sts", "get-web-identity-token",
		"--audience", "https://api.example",
		"--signing-algorithm", "RS256",
		"--duration-seconds", "900",
		"--query", "WebIdentityToken",
		"--output", "text",
		"--profile", "my-profile",
	}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
}

func TestIsAWSProfileNotConfiguredError(t *testing.T) {
	_, err := awsSDKCallerIdentity(context.Background(), "erun-cloud-aws-sdk-test-missing-profile")
	if err == nil {
		t.Fatal("expected an error for a profile absent from any shared config file")
	}
	if !isAWSProfileNotConfiguredError(err) {
		t.Fatalf("isAWSProfileNotConfiguredError(%v) = false, want true", err)
	}
}

// staticCredentialsProfileFixture points the SDK's shared-config resolution at
// a temp credentials file holding a static keypair under profile, and clears
// AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY/AWS_PROFILE so the env-var and
// ambient-profile provider ranked above shared config in the chain can never
// shadow it — this fixture exists to prove the shared-config profile itself
// resolved, the same thing `aws configure export-credentials --profile
// <profile>` reads.
func staticCredentialsProfileFixture(t *testing.T, profile string) {
	t.Helper()
	dir := t.TempDir()
	credentialsPath := filepath.Join(dir, "credentials")
	body := fmt.Sprintf("[%s]\naws_access_key_id = AKIAFIXTURE\naws_secret_access_key = fixturesecret\n", profile)
	if err := os.WriteFile(credentialsPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture credentials file: %v", err)
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentialsPath)
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_REGION", "us-east-1")
}

func TestLibraryRunAWSExportCredentialsMatchesSubprocessObservableResult(t *testing.T) {
	staticCredentialsProfileFixture(t, "erun-test-export")
	ctx, trace := traceCapturingContext()

	creds, err := libraryRunAWSExportCredentials(ctx, "erun-test-export")
	if err != nil {
		t.Fatalf("libraryRunAWSExportCredentials: %v", err)
	}

	// This is exactly what defaultRunAWSExportCredentials parses out of `aws
	// configure export-credentials --format process` stdout — the two paths
	// must agree on the observable result for the same underlying profile.
	if creds.AccessKeyID != "AKIAFIXTURE" || creds.SecretAccessKey != "fixturesecret" {
		t.Fatalf("creds = %+v, want AKIAFIXTURE/fixturesecret", creds)
	}
	if creds.SessionToken != "" {
		t.Fatalf("creds.SessionToken = %q, want empty for a static-key profile", creds.SessionToken)
	}
	if !creds.Expiration.IsZero() {
		t.Fatalf("creds.Expiration = %v, want zero for a static-key profile that cannot expire", creds.Expiration)
	}

	// The trace line must be byte-identical to what defaultRunAWSExportCredentials
	// renders for the subprocess path (root AGENTS.md's dry-run/audit contract:
	// the equivalent CLI invocation, regardless of execution mode).
	wantTrace := formatShellCommand("", "aws", awsConfigureExportCredentialsArgs("erun-test-export")...)
	if got := strings.TrimSpace(trace.String()); got != wantTrace {
		t.Fatalf("trace = %q, want %q", got, wantTrace)
	}
}

// TestAWSConfigureExportCredentialsArgsSharedByBothPaths locks the one argv
// builder defaultRunAWSExportCredentials (subprocess) and
// traceAWSExportCredentials (library) both call, so a profile can never
// render differently between execution modes.
func TestAWSConfigureExportCredentialsArgsSharedByBothPaths(t *testing.T) {
	got := awsConfigureExportCredentialsArgs("my-profile")
	want := []string{"configure", "export-credentials", "--format", "process", "--profile", "my-profile"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
}

func TestLibraryRunAWSExportCredentialsDryRunTracesWithoutResolvingCredentials(t *testing.T) {
	// No fixture set up: a dry run must never resolve credentials, so an
	// unconfigured profile would fail the test if it were reached.
	ctx, trace := traceCapturingContext()
	ctx.DryRun = true

	creds, err := libraryRunAWSExportCredentials(ctx, "my-profile")
	if err != nil {
		t.Fatalf("libraryRunAWSExportCredentials: %v", err)
	}
	if creds != (CloudProviderCredentials{}) {
		t.Fatalf("creds = %+v, want zero value on dry run", creds)
	}

	wantTrace := formatShellCommand("", "aws", awsConfigureExportCredentialsArgs("my-profile")...)
	if got := strings.TrimSpace(trace.String()); got != wantTrace {
		t.Fatalf("trace = %q, want %q", got, wantTrace)
	}
}

func TestLibraryRunAWSExportCredentialsMissingProfile(t *testing.T) {
	ctx, _ := traceCapturingContext()

	_, err := libraryRunAWSExportCredentials(ctx, "erun-cloud-aws-sdk-test-missing-profile")
	if err == nil {
		t.Fatal("expected an error for a profile absent from any shared config file")
	}
	if !strings.Contains(err.Error(), "export AWS credentials:") {
		t.Fatalf("err = %q, want it wrapped as \"export AWS credentials: ...\"", err)
	}
}
